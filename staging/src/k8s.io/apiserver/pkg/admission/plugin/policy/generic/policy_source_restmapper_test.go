/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package generic

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeResettableRESTMapper simulates a DeferredDiscoveryRESTMapper that was
// populated with a stale discovery result: RESTMapping fails until Reset() is
// called, at which point it succeeds. This mirrors the Mode B race condition
// where the apiserver restarts before the CRD controller finishes
// re-registering the API group.
type fakeResettableRESTMapper struct {
	mu       sync.Mutex
	resetted bool
	calls    int
}

var _ meta.RESTMapper = (*fakeResettableRESTMapper)(nil)

func (f *fakeResettableRESTMapper) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resetted = true
}

func (f *fakeResettableRESTMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if !f.resetted {
		return nil, &meta.NoResourceMatchError{PartialResource: schema.GroupVersionResource{
			Group:   gk.Group,
			Version: versions[0],
		}}
	}
	return &meta.RESTMapping{
		Resource:         schema.GroupVersionResource{Group: gk.Group, Version: versions[0], Resource: "parameters"},
		GroupVersionKind: schema.GroupVersionKind{Group: gk.Group, Version: versions[0], Kind: gk.Kind},
		Scope:            meta.RESTScopeRoot,
	}, nil
}

func (f *fakeResettableRESTMapper) KindFor(resource schema.GroupVersionResource) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}
func (f *fakeResettableRESTMapper) KindsFor(resource schema.GroupVersionResource) ([]schema.GroupVersionKind, error) {
	return nil, nil
}
func (f *fakeResettableRESTMapper) ResourceFor(input schema.GroupVersionResource) (schema.GroupVersionResource, error) {
	return schema.GroupVersionResource{}, nil
}
func (f *fakeResettableRESTMapper) ResourcesFor(input schema.GroupVersionResource) ([]schema.GroupVersionResource, error) {
	return nil, nil
}
func (f *fakeResettableRESTMapper) RESTMappings(gk schema.GroupKind, versions ...string) ([]*meta.RESTMapping, error) {
	return nil, nil
}
func (f *fakeResettableRESTMapper) ResourceSingularizer(resource string) (string, error) {
	return resource, nil
}

// fakeAlwaysFailRESTMapper always returns an error from RESTMapping, even
// after Reset(), simulating a CRD that genuinely does not exist.
type fakeAlwaysFailRESTMapper struct {
	mu      sync.Mutex
	calls   int
	failErr error
}

var _ meta.RESTMapper = (*fakeAlwaysFailRESTMapper)(nil)

func (f *fakeAlwaysFailRESTMapper) Reset() {}
func (f *fakeAlwaysFailRESTMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return nil, f.failErr
}
func (f *fakeAlwaysFailRESTMapper) KindFor(resource schema.GroupVersionResource) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}
func (f *fakeAlwaysFailRESTMapper) KindsFor(resource schema.GroupVersionResource) ([]schema.GroupVersionKind, error) {
	return nil, nil
}
func (f *fakeAlwaysFailRESTMapper) ResourceFor(input schema.GroupVersionResource) (schema.GroupVersionResource, error) {
	return schema.GroupVersionResource{}, nil
}
func (f *fakeAlwaysFailRESTMapper) ResourcesFor(input schema.GroupVersionResource) ([]schema.GroupVersionResource, error) {
	return nil, nil
}
func (f *fakeAlwaysFailRESTMapper) RESTMappings(gk schema.GroupKind, versions ...string) ([]*meta.RESTMapping, error) {
	return nil, nil
}
func (f *fakeAlwaysFailRESTMapper) ResourceSingularizer(resource string) (string, error) {
	return resource, nil
}

// newMinimalPolicySource creates a policySource with the fields needed for
// ensureParamsForPolicyLocked to function, using fake clients for the
// informer fallback path.
//
// The context is pre-cancelled so that any informer goroutines spawned by
// ensureParamsForPolicyLocked exit immediately without attempting a LIST
// against the fake dynamic client.
func newMinimalPolicySource(mapper meta.RESTMapper) *policySource[runtime.Object, runtime.Object, Evaluator] {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: informer goroutines stop before attempting LIST

	fakeClient := fake.NewSimpleClientset()
	fakeInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	return &policySource[runtime.Object, runtime.Object, Evaluator]{
		ctx:                  ctx,
		restMapper:           mapper,
		paramsCRDControllers: map[schema.GroupVersionKind]*paramInfo{},
		informerFactory:      fakeInformerFactory,
		dynamicClient:        fakeDynamicClient,
	}
}

// TestEnsureParamsForPolicyLocked_RESTMapperResetOnMiss verifies that when
// RESTMapping fails with a fresh-but-stale discovery cache, ensureParamsForPolicyLocked
// calls Reset() and retries, succeeding on the second attempt.
//
// This is the core Fix 3 behavior: the Mode B race window shrinks from ~30s
// (scheduled ticker) to ~1-5s (time for CRD re-registration).
func TestEnsureParamsForPolicyLocked_RESTMapperResetOnMiss(t *testing.T) {
	mapper := &fakeResettableRESTMapper{}
	s := newMinimalPolicySource(mapper)

	paramGVK := &schema.GroupVersionKind{
		Group:   "vap.datadoghq.com",
		Version: "v1",
		Kind:    "Parameter",
	}

	s.lock.Lock()
	informer, scope, err := s.ensureParamsForPolicyLocked(paramGVK)
	s.lock.Unlock()

	if err != nil {
		t.Fatalf("expected Reset()+retry to succeed, got error: %v", err)
	}
	if informer == nil {
		t.Error("expected non-nil informer after successful resolution")
	}
	if scope == nil {
		t.Error("expected non-nil scope after successful resolution")
	}
	if mapper.calls != 2 {
		t.Errorf("expected 2 RESTMapping calls (1 before Reset, 1 after), got %d", mapper.calls)
	}
	if !mapper.resetted {
		t.Error("expected Reset() to have been called after the first RESTMapping failure")
	}
}

// TestEnsureParamsForPolicyLocked_PermanentMissReturnsError verifies that
// when Reset() is called but RESTMapping still fails (CRD genuinely absent),
// the "failed to find resource referenced by paramKind" error is returned.
func TestEnsureParamsForPolicyLocked_PermanentMissReturnsError(t *testing.T) {
	mapper := &fakeAlwaysFailRESTMapper{
		failErr: errors.New("no matches for kind Parameter in group vap.datadoghq.com"),
	}
	s := newMinimalPolicySource(mapper)

	paramGVK := &schema.GroupVersionKind{
		Group:   "vap.datadoghq.com",
		Version: "v1",
		Kind:    "Parameter",
	}

	s.lock.Lock()
	_, _, err := s.ensureParamsForPolicyLocked(paramGVK)
	s.lock.Unlock()

	if err == nil {
		t.Fatal("expected error for permanently absent CRD, got nil")
	}

	const wantErrSubstr = "failed to find resource referenced by paramKind"
	if !strings.Contains(err.Error(), wantErrSubstr) {
		t.Errorf("expected error to contain %q, got: %v", wantErrSubstr, err)
	}

	// Reset() should have been called once, then the second call also failed.
	if mapper.calls != 2 {
		t.Errorf("expected 2 RESTMapping calls (initial + post-Reset retry), got %d", mapper.calls)
	}
}

// TestEnsureParamsForPolicyLocked_NonResettableMapperPropagatesError verifies
// that when the RESTMapper does not implement Reset(), the original error is
// propagated without panicking. The type-assertion guard must be a no-op.
func TestEnsureParamsForPolicyLocked_NonResettableMapperPropagatesError(t *testing.T) {
	// meta.DefaultRESTMapper does not implement Reset().
	mapper := meta.NewDefaultRESTMapper(nil)
	s := newMinimalPolicySource(mapper)

	paramGVK := &schema.GroupVersionKind{
		Group:   "vap.datadoghq.com",
		Version: "v1",
		Kind:    "Parameter",
	}

	s.lock.Lock()
	_, _, err := s.ensureParamsForPolicyLocked(paramGVK)
	s.lock.Unlock()

	if err == nil {
		t.Fatal("expected error when GVK is not registered, got nil")
	}

	const wantErrSubstr = "failed to find resource referenced by paramKind"
	if !strings.Contains(err.Error(), wantErrSubstr) {
		t.Errorf("expected error to contain %q, got: %v", wantErrSubstr, err)
	}
}

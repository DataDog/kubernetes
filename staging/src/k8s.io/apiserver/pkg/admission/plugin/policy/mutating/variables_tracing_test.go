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

package mutating

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	v1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/admission/plugin/cel"
	"k8s.io/apiserver/pkg/admission/plugin/policy/mutating/patch"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/client-go/openapi/openapitest"
	"k8s.io/component-base/tracing"
	"k8s.io/utils/ptr"
)

// TestVariablesLostThroughTracingSpan reproduces a bug where the Datadog
// OTel tracing patch wraps the context with tracing.Start() between the
// CompositionEnv.CreateContext() call and the mutation evaluator's
// ForInput() call. The mutatingEvaluator.ForInput() retrieves the
// composition context via a type assertion: compositionCtx, _ := ctx.(CompositionContext).
// tracing.Start() returns a new context that wraps the parent but is NOT
// a CompositionContext, so the type assertion fails, variables becomes nil,
// and any mutation expression referencing variables.* fails with
// "no such key: <varname>".
//
// This test creates a MAP with a variable and a JSONPatch mutation that
// references it, then evaluates the mutation with and without a tracing
// span wrapping the context. Without tracing, the mutation succeeds.
// With tracing, the mutation fails with "no such key".
func TestVariablesLostThroughTracingSpan(t *testing.T) {
	deploymentGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	deploymentGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tcManager := patch.NewTypeConverterManager(nil, openapitest.NewEmbeddedFileClient())
	go tcManager.Run(ctx)

	err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, time.Second, true, func(context.Context) (done bool, err error) {
		converter := tcManager.GetTypeConverter(deploymentGVK)
		return converter != nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// Policy with a variable and a JSONPatch that references it.
	p := mutations(
		variables(policy("test-vars-through-tracing"),
			v1.Variable{
				Name:       "replicas",
				Expression: "object.spec.replicas + 100",
			},
		),
		v1.Mutation{
			PatchType: v1.PatchTypeJSONPatch,
			JSONPatch: &v1.JSONPatch{
				Expression: `[
					JSONPatch{op: "replace", path: "/spec/replicas", value: variables.replicas}
				]`,
			},
		},
	)

	policyEvaluator := compilePolicy(p)
	if policyEvaluator.CompositedCompiler == nil {
		t.Fatal("expected CompositedCompiler to be set")
	}

	obj := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)}}
	obj.APIVersion = "apps/v1"
	obj.Kind = "Deployment"

	typeConverter := tcManager.GetTypeConverter(deploymentGVK)

	runPatch := func(t *testing.T, ctx context.Context) (runtime.Object, error) {
		vAttrs := &admission.VersionedAttributes{
			Attributes:         admission.NewAttributesRecord(obj, nil, deploymentGVK, "", "test", deploymentGVR, "", admission.Create, &metav1.CreateOptions{}, false, nil),
			VersionedKind:      deploymentGVK,
			VersionedObject:    admission.NewLazyObject(obj),
			VersionedOldObject: admission.NewLazyObject(nil),
		}
		r := patch.Request{
			MatchedResource:     deploymentGVR,
			VersionedAttributes: vAttrs,
			ObjectInterfaces:    admission.NewObjectInterfacesFromScheme(scheme),
			OptionalVariables:   cel.OptionalVariableBindings{VersionedParams: nil, Authorizer: fakeAuthorizer{}},
			TypeConverter:       typeConverter,
		}
		patcher := policyEvaluator.Mutators[0]
		return patcher.Patch(ctx, r, celconfig.RuntimeCELCostBudget)
	}

	// Without tracing: variables should work.
	t.Run("without_tracing", func(t *testing.T) {
		baseCtx := context.Background()
		compositionCtx := policyEvaluator.CompositedCompiler.CreateContext(baseCtx)

		result, err := runPatch(t, compositionCtx)
		if err != nil {
			t.Fatalf("mutation should succeed without tracing, got error: %v", err)
		}
		deploy, ok := result.(*appsv1.Deployment)
		if !ok {
			t.Fatalf("expected *appsv1.Deployment, got %T", result)
		}
		if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 101 {
			t.Fatalf("expected replicas=101 (1+100), got %v", deploy.Spec.Replicas)
		}
	})

	// With tracing: variables should now work after the fix.
	// CompositedEvaluator.ForInput calls CreateContext internally,
	// wrapping the tracing context so the CompositionContext type
	// assertion in mutatingEvaluator.ForInput succeeds.
	t.Run("with_tracing", func(t *testing.T) {
		baseCtx := context.Background()
		compositionCtx := policyEvaluator.CompositedCompiler.CreateContext(baseCtx)

		// Simulate the Datadog tracing patch: wrap the composition context
		// with tracing.Start, exactly as dispatcher.go does.
		mapCtx, mapSpan := tracing.Start(compositionCtx, "MAP mutate test-vars-through-tracing",
			attribute.String("policy", "test-vars-through-tracing"),
			attribute.String("binding", "test-binding"))
		defer mapSpan.End(500 * time.Millisecond)

		result, err := runPatch(t, mapCtx)
		if err != nil {
			t.Fatalf("mutation should succeed with tracing after fix, got error: %v", err)
		}
		deploy, ok := result.(*appsv1.Deployment)
		if !ok {
			t.Fatalf("expected *appsv1.Deployment, got %T", result)
		}
		if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 101 {
			t.Fatalf("expected replicas=101 (1+100), got %v", deploy.Spec.Replicas)
		}
	})
}

/*
Copyright 2022 The Kubernetes Authors.

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

package cel

import (
	"context"
	"reflect"
	"sync"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/cel/environment"
)

// unstructuredCacheKey is the context key under which a per-Validate()-call
// cache of object->unstructured conversions is stored. matchConditions,
// validations, messageExpression and auditAnnotations are each evaluated as
// separate ConditionEvaluator passes against the same object/oldObject/params,
// and without this cache each pass independently repeats the same
// reflection-based runtime.DefaultUnstructuredConverter.ToUnstructured() work.
type unstructuredCacheKey struct{}

// ContextWithUnstructuredCache returns a context that causes convertObjectToUnstructured
// to memoize conversions by object pointer identity for the lifetime of the returned
// context. Callers that invoke multiple ConditionEvaluator passes against the same
// versionedAttr/params within one logical evaluation (e.g. validator.Validate) should
// wrap their context with this once, upfront, so the passes share converted results.
func ContextWithUnstructuredCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, unstructuredCacheKey{}, &sync.Map{})
}

// conditionCompiler implement the interface ConditionCompiler.
type conditionCompiler struct {
	compiler Compiler
}

func NewConditionCompiler(env *environment.EnvSet) ConditionCompiler {
	return &conditionCompiler{compiler: NewCompiler(env)}
}

// CompileCondition compiles the cel expressions defined in the ExpressionAccessors into a ConditionEvaluator
func (c *conditionCompiler) CompileCondition(expressionAccessors []ExpressionAccessor, options OptionalVariableDeclarations, mode environment.Type) ConditionEvaluator {
	compilationResults := make([]CompilationResult, len(expressionAccessors))
	for i, expressionAccessor := range expressionAccessors {
		if expressionAccessor == nil {
			continue
		}
		compilationResults[i] = c.compiler.CompileCELExpression(expressionAccessor, options, mode)
	}
	return NewCondition(compilationResults)
}

// condition implements the ConditionEvaluator interface
type condition struct {
	compilationResults []CompilationResult
}

func NewCondition(compilationResults []CompilationResult) ConditionEvaluator {
	return &condition{
		compilationResults,
	}
}

func convertObjectToUnstructured(ctx context.Context, obj interface{}) (*unstructured.Unstructured, error) {
	if obj == nil || reflect.ValueOf(obj).IsNil() {
		return &unstructured.Unstructured{Object: nil}, nil
	}
	if cacheAny := ctx.Value(unstructuredCacheKey{}); cacheAny != nil {
		cache := cacheAny.(*sync.Map)
		// obj is always a pointer to a concrete runtime.Object/AdmissionRequest/Namespace,
		// so it's comparable and safe to use as a map key directly.
		if cached, ok := cache.Load(obj); ok {
			return cached.(*unstructured.Unstructured), nil
		}
		ret, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return nil, err
		}
		u := &unstructured.Unstructured{Object: ret}
		// LoadOrStore rather than Store: if two passes race to convert the same
		// object concurrently, keep whichever result was stored first so both
		// return the identical value.
		actual, _ := cache.LoadOrStore(obj, u)
		return actual.(*unstructured.Unstructured), nil
	}
	ret, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: ret}, nil
}

func objectToResolveVal(ctx context.Context, r runtime.Object) (interface{}, error) {
	if r == nil || reflect.ValueOf(r).IsNil() {
		return nil, nil
	}
	v, err := convertObjectToUnstructured(ctx, r)
	if err != nil {
		return nil, err
	}
	return v.Object, nil
}

// ForInput evaluates the compiled CEL expressions converting them into CELEvaluations
// errors per evaluation are returned on the Evaluation object
// runtimeCELCostBudget was added for testing purpose only. Callers should always use const RuntimeCELCostBudget from k8s.io/apiserver/pkg/apis/cel/config.go as input.
func (c *condition) ForInput(ctx context.Context, versionedAttr *admission.VersionedAttributes, request *admissionv1.AdmissionRequest, inputs OptionalVariableBindings, namespace *v1.Namespace, runtimeCELCostBudget int64) ([]EvaluationResult, int64, error) {
	// TODO: replace unstructured with ref.Val for CEL variables when native type support is available
	evaluations := make([]EvaluationResult, len(c.compilationResults))
	var err error

	// if this activation supports composition, we will need the compositionCtx. It may be nil.
	compositionCtx, _ := ctx.(CompositionContext)

	activation, err := newActivation(ctx, compositionCtx, versionedAttr, request, inputs, namespace)
	if err != nil {
		return nil, -1, err
	}

	remainingBudget := runtimeCELCostBudget
	for i, compilationResult := range c.compilationResults {
		evaluations[i], remainingBudget, err = activation.Evaluate(ctx, compositionCtx, compilationResult, remainingBudget)
		if err != nil {
			return nil, -1, err
		}
	}

	return evaluations, remainingBudget, nil
}

// TODO: to reuse https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/apiserver/pkg/admission/plugin/webhook/request/admissionreview.go#L154
func CreateAdmissionRequest(attr admission.Attributes, equivalentGVR metav1.GroupVersionResource, equivalentKind metav1.GroupVersionKind) *admissionv1.AdmissionRequest {
	// Attempting to use same logic as webhook for constructing resource
	// GVK, GVR, subresource
	// Use the GVK, GVR that the matcher decided was equivalent to that of the request
	// https://github.com/kubernetes/kubernetes/blob/90c362b3430bcbbf8f245fadbcd521dab39f1d7c/staging/src/k8s.io/apiserver/pkg/admission/plugin/webhook/generic/webhook.go#L182-L210
	gvk := equivalentKind
	gvr := equivalentGVR
	subresource := attr.GetSubresource()

	requestGVK := attr.GetKind()
	requestGVR := attr.GetResource()
	requestSubResource := attr.GetSubresource()

	aUserInfo := attr.GetUserInfo()
	var userInfo authenticationv1.UserInfo
	if aUserInfo != nil {
		userInfo = authenticationv1.UserInfo{
			Extra:    make(map[string]authenticationv1.ExtraValue),
			Groups:   aUserInfo.GetGroups(),
			UID:      aUserInfo.GetUID(),
			Username: aUserInfo.GetName(),
		}
		// Convert the extra information in the user object
		for key, val := range aUserInfo.GetExtra() {
			userInfo.Extra[key] = authenticationv1.ExtraValue(val)
		}
	}

	dryRun := attr.IsDryRun()

	return &admissionv1.AdmissionRequest{
		Kind: metav1.GroupVersionKind{
			Group:   gvk.Group,
			Kind:    gvk.Kind,
			Version: gvk.Version,
		},
		Resource: metav1.GroupVersionResource{
			Group:    gvr.Group,
			Resource: gvr.Resource,
			Version:  gvr.Version,
		},
		SubResource: subresource,
		RequestKind: &metav1.GroupVersionKind{
			Group:   requestGVK.Group,
			Kind:    requestGVK.Kind,
			Version: requestGVK.Version,
		},
		RequestResource: &metav1.GroupVersionResource{
			Group:    requestGVR.Group,
			Resource: requestGVR.Resource,
			Version:  requestGVR.Version,
		},
		RequestSubResource: requestSubResource,
		Name:               attr.GetName(),
		Namespace:          attr.GetNamespace(),
		Operation:          admissionv1.Operation(attr.GetOperation()),
		UserInfo:           userInfo,
		// Leave Object and OldObject unset since we don't provide access to them via request
		DryRun: &dryRun,
		Options: runtime.RawExtension{
			Object: attr.GetOperationOptions(),
		},
	}
}

// CreateNamespaceObject creates a Namespace object that is suitable for the CEL evaluation.
// If the namespace is nil, CreateNamespaceObject returns nil
func CreateNamespaceObject(namespace *v1.Namespace) *v1.Namespace {
	if namespace == nil {
		return nil
	}

	return &v1.Namespace{
		Status: namespace.Status,
		Spec:   namespace.Spec,
		ObjectMeta: metav1.ObjectMeta{
			Name:                       namespace.Name,
			GenerateName:               namespace.GenerateName,
			Namespace:                  namespace.Namespace,
			UID:                        namespace.UID,
			ResourceVersion:            namespace.ResourceVersion,
			Generation:                 namespace.Generation,
			CreationTimestamp:          namespace.CreationTimestamp,
			DeletionTimestamp:          namespace.DeletionTimestamp,
			DeletionGracePeriodSeconds: namespace.DeletionGracePeriodSeconds,
			Labels:                     namespace.Labels,
			Annotations:                namespace.Annotations,
			Finalizers:                 namespace.Finalizers,
		},
	}
}

// CompilationErrors returns a list of all the errors from the compilation of the mutatingEvaluator
func (c *condition) CompilationErrors() []error {
	compilationErrors := []error{}
	for _, result := range c.compilationResults {
		if result.Error != nil {
			compilationErrors = append(compilationErrors, result.Error)
		}
	}
	return compilationErrors
}

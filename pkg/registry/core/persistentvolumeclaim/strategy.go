/*
Copyright 2014 The Kubernetes Authors.

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

package persistentvolumeclaim

import (
	"context"
	"fmt"

	"github.com/google/go-cmp/cmp"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"sigs.k8s.io/structured-merge-diff/v4/fieldpath"

	"k8s.io/kubernetes/pkg/api/legacyscheme"
	pvcutil "k8s.io/kubernetes/pkg/api/persistentvolumeclaim"
	api "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/apis/core/validation"
	"k8s.io/kubernetes/pkg/features"
)

// persistentvolumeclaimStrategy implements behavior for PersistentVolumeClaim objects
type persistentvolumeclaimStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

// Strategy is the default logic that applies when creating and updating PersistentVolumeClaim
// objects via the REST API.
var Strategy = persistentvolumeclaimStrategy{legacyscheme.Scheme, names.SimpleNameGenerator}

func (persistentvolumeclaimStrategy) NamespaceScoped() bool {
	return true
}

// GetResetFields returns the set of fields that get reset by the strategy
// and should not be modified by the user.
func (persistentvolumeclaimStrategy) GetResetFields() map[fieldpath.APIVersion]*fieldpath.Set {
	fields := map[fieldpath.APIVersion]*fieldpath.Set{
		"v1": fieldpath.NewSet(
			fieldpath.MakePathOrDie("status"),
		),
	}

	return fields
}

// PrepareForCreate clears the Status field which is not allowed to be set by end users on creation.
func (persistentvolumeclaimStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
	pvc := obj.(*api.PersistentVolumeClaim)
	pvc.Status = api.PersistentVolumeClaimStatus{}
	pvcutil.DropDisabledFields(&pvc.Spec, nil)

	// For data sources, we need to do 2 things to implement KEP 1495

	// First drop invalid values from spec.dataSource (anything other than PVC or
	// VolumeSnapshot) if certain conditions are met.
	pvcutil.EnforceDataSourceBackwardsCompatibility(&pvc.Spec, nil)

	// Second copy dataSource -> dataSourceRef or dataSourceRef -> dataSource if one of them
	// is nil and the other is non-nil
	pvcutil.NormalizeDataSources(&pvc.Spec)
}

func (persistentvolumeclaimStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	pvc := obj.(*api.PersistentVolumeClaim)
	opts := validation.ValidationOptionsForPersistentVolumeClaim(pvc, nil)
	return validation.ValidatePersistentVolumeClaim(pvc, opts)
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (persistentvolumeclaimStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return pvcutil.GetWarningsForPersistentVolumeClaim(obj.(*api.PersistentVolumeClaim))
}

// Canonicalize normalizes the object after validation.
func (persistentvolumeclaimStrategy) Canonicalize(obj runtime.Object) {
}

func (persistentvolumeclaimStrategy) AllowCreateOnUpdate() bool {
	return false
}

// PrepareForUpdate sets the Status field which is not allowed to be set by end users on update
func (persistentvolumeclaimStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	newPvc := obj.(*api.PersistentVolumeClaim)
	oldPvc := old.(*api.PersistentVolumeClaim)
	newPvc.Status = oldPvc.Status

	pvcutil.DropDisabledFields(&newPvc.Spec, &oldPvc.Spec)

	// We need to use similar logic to PrepareForCreate here both to preserve backwards
	// compatibility with the old behavior (ignoring of garbage dataSources at both create
	// and update time) and also for compatibility with older clients, that might omit
	// the dataSourceRef field which we filled in automatically, so we have to fill it
	// in again here.
	pvcutil.EnforceDataSourceBackwardsCompatibility(&newPvc.Spec, &oldPvc.Spec)
	pvcutil.NormalizeDataSources(&newPvc.Spec)

	// We also normalize the data source fields of the old PVC, so that objects saved
	// from an earlier version will pass validation.
	pvcutil.NormalizeDataSources(&oldPvc.Spec)
}

func (persistentvolumeclaimStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	newPvc := obj.(*api.PersistentVolumeClaim)
	oldPvc := old.(*api.PersistentVolumeClaim)
	opts := validation.ValidationOptionsForPersistentVolumeClaim(newPvc, oldPvc)
	errorList := validation.ValidatePersistentVolumeClaim(newPvc, opts)
	return append(errorList, validation.ValidatePersistentVolumeClaimUpdateWithContext(ctx, newPvc, oldPvc, opts)...)
}

// WarningsOnUpdate returns warnings for the given update.
func (persistentvolumeclaimStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return pvcutil.GetWarningsForPersistentVolumeClaim(obj.(*api.PersistentVolumeClaim))
}

func (persistentvolumeclaimStrategy) AllowUnconditionalUpdate() bool {
	return true
}

type persistentvolumeclaimStatusStrategy struct {
	persistentvolumeclaimStrategy
}

var StatusStrategy = persistentvolumeclaimStatusStrategy{Strategy}

// GetResetFields returns the set of fields that get reset by the strategy
// and should not be modified by the user.
func (persistentvolumeclaimStatusStrategy) GetResetFields() map[fieldpath.APIVersion]*fieldpath.Set {
	fields := map[fieldpath.APIVersion]*fieldpath.Set{
		"v1": fieldpath.NewSet(
			fieldpath.MakePathOrDie("spec"),
		),
	}

	return fields
}

// PrepareForUpdate sets the Spec field which is not allowed to be changed when updating a PV's Status
func (persistentvolumeclaimStatusStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	newPVC := obj.(*api.PersistentVolumeClaim)
	oldPVC := old.(*api.PersistentVolumeClaim)
	newPVC.Spec = oldPVC.Spec
	pvcutil.DropDisabledFieldsFromStatus(newPVC, oldPVC)
}

func (persistentvolumeclaimStatusStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	newPvc := obj.(*api.PersistentVolumeClaim)
	oldPvc := old.(*api.PersistentVolumeClaim)
	opts := validation.ValidationOptionsForPersistentVolumeClaim(newPvc, oldPvc)
	return validation.ValidatePersistentVolumeClaimStatusUpdate(newPvc, oldPvc, opts)
}

// WarningsOnUpdate returns warnings for the given update.
func (persistentvolumeclaimStatusStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}

type persistentvolumeclaimStorageClassStrategy struct {
	persistentvolumeclaimStrategy
}

var StorageClassStrategy = persistentvolumeclaimStorageClassStrategy{Strategy}

// GetResetFields returns the set of fields that get reset by the strategy
// and should not be modified by the user. For storage class updates, we only allow
// storageClassName field to be modified.
func (persistentvolumeclaimStorageClassStrategy) GetResetFields() map[fieldpath.APIVersion]*fieldpath.Set {
	return Strategy.GetResetFields()
}

// PrepareForUpdate sets fields which are not allowed to be changed when updating a PVC's storage class
func (persistentvolumeclaimStorageClassStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	newPVC := obj.(*api.PersistentVolumeClaim)
	oldPVC := old.(*api.PersistentVolumeClaim)

	// Store the desired storage class name
	desiredStorageClassName := newPVC.Spec.StorageClassName

	// Start with the old object and only change the storage class
	*newPVC = *oldPVC
	newPVC.Spec.StorageClassName = desiredStorageClassName
}

func (persistentvolumeclaimStorageClassStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	newPvc := obj.(*api.PersistentVolumeClaim)
	oldPvc := old.(*api.PersistentVolumeClaim)

	allErrs := field.ErrorList{}

	// If storage class is not changing, this should not be a storage class update
	if apiequality.Semantic.DeepEqual(oldPvc.Spec.StorageClassName, newPvc.Spec.StorageClassName) {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "storageClassName"),
			"storage class is not changing; use the main PVC resource for other updates"))
	}

	opts := validation.ValidationOptionsForPersistentVolumeClaim(newPvc, oldPvc)
	allErrs = append(allErrs, validation.ValidatePersistentVolumeClaim(newPvc, opts)...)

	// Use a modified version of PVC update validation that doesn't check storage class changes
	return append(allErrs, validatePersistentVolumeClaimUpdateForStorageClassSubresource(newPvc, oldPvc, opts)...)
}

// WarningsOnUpdate returns warnings for the given update.
func (persistentvolumeclaimStorageClassStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}

// validatePersistentVolumeClaimUpdateForStorageClassSubresource validates a PVC update
// for the storageclass subresource, allowing storage class changes that would normally be forbidden
func validatePersistentVolumeClaimUpdateForStorageClassSubresource(newPvc, oldPvc *api.PersistentVolumeClaim, opts validation.PersistentVolumeClaimSpecValidationOptions) field.ErrorList {
	allErrs := validation.ValidateObjectMetaUpdate(&newPvc.ObjectMeta, &oldPvc.ObjectMeta, field.NewPath("metadata"))
	newPvcClone := newPvc.DeepCopy()
	oldPvcClone := oldPvc.DeepCopy()

	// PVController needs to update PVC.Spec w/ VolumeName.
	// Claims are immutable in order to enforce quota, range limits, etc. without gaming the system.
	if len(oldPvc.Spec.VolumeName) == 0 {
		// volumeName changes are allowed once.
		oldPvcClone.Spec.VolumeName = newPvcClone.Spec.VolumeName // +k8s:verify-mutation:reason=clone
	}

	// For storage class subresource, allow storage class changes
	// Skip the normal storage class immutability checks
	oldPvcClone.Spec.StorageClassName = newPvcClone.Spec.StorageClassName // +k8s:verify-mutation:reason=clone

	// lets make sure storage values are same.
	if newPvc.Status.Phase == api.ClaimBound && newPvcClone.Spec.Resources.Requests != nil {
		newPvcClone.Spec.Resources.Requests["storage"] = oldPvc.Spec.Resources.Requests["storage"] // +k8s:verify-mutation:reason=clone
	}
	// lets make sure volume attributes class name is same.
	if newPvc.Status.Phase == api.ClaimBound && newPvcClone.Spec.VolumeAttributesClassName != nil {
		newPvcClone.Spec.VolumeAttributesClassName = oldPvcClone.Spec.VolumeAttributesClassName // +k8s:verify-mutation:reason=clone
	}

	oldSize := oldPvc.Spec.Resources.Requests["storage"]
	newSize := newPvc.Spec.Resources.Requests["storage"]
	statusSize := oldPvc.Status.Capacity["storage"]

	if !apiequality.Semantic.DeepEqual(newPvcClone.Spec, oldPvcClone.Spec) {
		specDiff := cmp.Diff(oldPvcClone.Spec, newPvcClone.Spec)
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec"), fmt.Sprintf("spec is immutable after creation except resources.requests, volumeAttributesClassName for bound claims, and storageClassName via storageclass subresource\n%v", specDiff)))
	}
	if newSize.Cmp(oldSize) < 0 {
		if !opts.EnableRecoverFromExpansionFailure {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "resources", "requests", "storage"), "field can not be less than previous value"))
		} else {
			// This validation permits reducing pvc requested size up to capacity recorded in pvc.status
			// so that users can recover from volume expansion failure, but Kubernetes does not actually
			// support volume shrinking
			if newSize.Cmp(statusSize) <= 0 {
				allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "resources", "requests", "storage"), "field can not be less than status.capacity"))
			}
		}
	}

	allErrs = append(allErrs, validation.ValidateImmutableField(newPvc.Spec.VolumeMode, oldPvc.Spec.VolumeMode, field.NewPath("volumeMode"))...)

	if !apiequality.Semantic.DeepEqual(oldPvc.Spec.VolumeAttributesClassName, newPvc.Spec.VolumeAttributesClassName) {
		if !utilfeature.DefaultFeatureGate.Enabled(features.VolumeAttributesClass) {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "volumeAttributesClassName"), "update is forbidden when the VolumeAttributesClass feature gate is disabled"))
		}
		if opts.EnableVolumeAttributesClass {
			if oldPvc.Spec.VolumeAttributesClassName != nil {
				if newPvc.Spec.VolumeAttributesClassName == nil {
					allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "volumeAttributesClassName"), "update from non-nil value to nil is forbidden"))
				} else if len(*newPvc.Spec.VolumeAttributesClassName) == 0 {
					allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "volumeAttributesClassName"), "update from non-nil value to an empty string is forbidden"))
				}
			}
		}
	}

	return allErrs
}

// GetAttrs returns labels and fields of a given object for filtering purposes.
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	persistentvolumeclaimObj, ok := obj.(*api.PersistentVolumeClaim)
	if !ok {
		return nil, nil, fmt.Errorf("not a persistentvolumeclaim")
	}
	return labels.Set(persistentvolumeclaimObj.Labels), PersistentVolumeClaimToSelectableFields(persistentvolumeclaimObj), nil
}

// MatchPersistentVolumeClaim returns a generic matcher for a given label and field selector.
func MatchPersistentVolumeClaim(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// PersistentVolumeClaimToSelectableFields returns a field set that represents the object
func PersistentVolumeClaimToSelectableFields(persistentvolumeclaim *api.PersistentVolumeClaim) fields.Set {
	objectMetaFieldsSet := generic.ObjectMetaFieldsSet(&persistentvolumeclaim.ObjectMeta, true)
	specificFieldsSet := fields.Set{
		// This is a bug, but we need to support it for backward compatibility.
		"name": persistentvolumeclaim.Name,
	}
	return generic.MergeFieldsSets(objectMetaFieldsSet, specificFieldsSet)
}

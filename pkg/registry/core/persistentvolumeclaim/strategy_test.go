/*
Copyright 2015 The Kubernetes Authors.

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
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	apitesting "k8s.io/kubernetes/pkg/api/testing"
	api "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/features"

	// ensure types are installed
	_ "k8s.io/kubernetes/pkg/apis/core/install"
)

func validNewPersistentVolumeClaim(name, ns string) *api.PersistentVolumeClaim {
	pv := &api.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: api.PersistentVolumeClaimSpec{
			AccessModes: []api.PersistentVolumeAccessMode{api.ReadWriteOnce},
			Resources: api.VolumeResourceRequirements{
				Requests: api.ResourceList{
					api.ResourceName(api.ResourceStorage): resource.MustParse("10G"),
				},
			},
		},
		Status: api.PersistentVolumeClaimStatus{
			Phase: api.ClaimPending,
		},
	}
	return pv
}

func TestSelectableFieldLabelConversions(t *testing.T) {
	apitesting.TestSelectableFieldLabelConversionsOfKind(t,
		"v1",
		"PersistentVolumeClaim",
		PersistentVolumeClaimToSelectableFields(&api.PersistentVolumeClaim{}),
		map[string]string{"name": "metadata.name"},
	)
}

func TestDropConditions(t *testing.T) {
	ctx := genericapirequest.NewDefaultContext()
	pvcWithConditions := func() *api.PersistentVolumeClaim {
		return &api.PersistentVolumeClaim{
			Status: api.PersistentVolumeClaimStatus{
				Conditions: []api.PersistentVolumeClaimCondition{
					{Type: api.PersistentVolumeClaimResizing, Status: api.ConditionTrue},
				},
			},
		}
	}
	pvcWithoutConditions := func() *api.PersistentVolumeClaim {
		return &api.PersistentVolumeClaim{
			Status: api.PersistentVolumeClaimStatus{},
		}
	}

	pvcInfo := []struct {
		description   string
		hasConditions bool
		pvc           func() *api.PersistentVolumeClaim
	}{
		{
			description:   "has Conditions",
			hasConditions: true,
			pvc:           pvcWithConditions,
		},
		{
			description:   "does not have Conditions",
			hasConditions: false,
			pvc:           pvcWithoutConditions,
		},
	}

	for _, oldPvcInfo := range pvcInfo {
		for _, newPvcInfo := range pvcInfo {
			oldPvcHasConditins, oldPvc := oldPvcInfo.hasConditions, oldPvcInfo.pvc()
			newPvcHasConditions, newPvc := newPvcInfo.hasConditions, newPvcInfo.pvc()

			t.Run(fmt.Sprintf("old pvc %s, new pvc %s", oldPvcInfo.description, newPvcInfo.description), func(t *testing.T) {
				StatusStrategy.PrepareForUpdate(ctx, newPvc, oldPvc)

				// old pvc should never be changed
				if !reflect.DeepEqual(oldPvc, oldPvcInfo.pvc()) {
					t.Errorf("old pvc changed: %v", cmp.Diff(oldPvc, oldPvcInfo.pvc()))
				}

				switch {
				case oldPvcHasConditins || newPvcHasConditions:
					// new pvc should not be changed if the feature is enabled, or if the old pvc had Conditions
					if !reflect.DeepEqual(newPvc, newPvcInfo.pvc()) {
						t.Errorf("new pvc changed: %v", cmp.Diff(newPvc, newPvcInfo.pvc()))
					}
				default:
					// new pvc should not need to be changed
					if !reflect.DeepEqual(newPvc, newPvcInfo.pvc()) {
						t.Errorf("new pvc changed: %v", cmp.Diff(newPvc, newPvcInfo.pvc()))
					}
				}
			})
		}
	}

}

var (
	coreGroup    = ""
	snapGroup    = "snapshot.storage.k8s.io"
	genericGroup = "generic.storage.k8s.io"
	pvcKind      = "PersistentVolumeClaim"
	snapKind     = "VolumeSnapshot"
	genericKind  = "Generic"
	podKind      = "Pod"
)

func makeDataSource(apiGroup, kind, name string) *api.TypedLocalObjectReference {
	return &api.TypedLocalObjectReference{
		APIGroup: &apiGroup,
		Kind:     kind,
		Name:     name,
	}

}

func makeDataSourceRef(apiGroup, kind, name string, namespace *string) *api.TypedObjectReference {
	return &api.TypedObjectReference{
		APIGroup:  &apiGroup,
		Kind:      kind,
		Name:      name,
		Namespace: namespace,
	}
}

func TestPrepareForCreate(t *testing.T) {
	ctx := genericapirequest.NewDefaultContext()

	ns := "ns1"
	volumeDataSource := makeDataSource(coreGroup, pvcKind, "my-vol")
	volumeDataSourceRef := makeDataSourceRef(coreGroup, pvcKind, "my-vol", nil)
	xnsVolumeDataSourceRef := makeDataSourceRef(coreGroup, pvcKind, "my-vol", &ns)
	snapshotDataSource := makeDataSource(snapGroup, snapKind, "my-snap")
	snapshotDataSourceRef := makeDataSourceRef(snapGroup, snapKind, "my-snap", nil)
	xnsSnapshotDataSourceRef := makeDataSourceRef(snapGroup, snapKind, "my-snap", &ns)
	genericDataSource := makeDataSource(genericGroup, genericKind, "my-foo")
	genericDataSourceRef := makeDataSourceRef(genericGroup, genericKind, "my-foo", nil)
	xnsGenericDataSourceRef := makeDataSourceRef(genericGroup, genericKind, "my-foo", &ns)
	coreDataSource := makeDataSource(coreGroup, podKind, "my-pod")
	coreDataSourceRef := makeDataSourceRef(coreGroup, podKind, "my-pod", nil)
	xnsCoreDataSourceRef := makeDataSourceRef(coreGroup, podKind, "my-pod", &ns)

	var tests = map[string]struct {
		anyEnabled    bool
		xnsEnabled    bool
		dataSource    *api.TypedLocalObjectReference
		dataSourceRef *api.TypedObjectReference
		want          *api.TypedLocalObjectReference
		wantRef       *api.TypedObjectReference
	}{
		"any disabled with empty ds": {
			want: nil,
		},
		"any disabled with volume ds": {
			dataSource: volumeDataSource,
			want:       volumeDataSource,
		},
		"any disabled with snapshot ds": {
			dataSource: snapshotDataSource,
			want:       snapshotDataSource,
		},
		"any disabled with generic ds": {
			dataSource: genericDataSource,
			want:       nil,
		},
		"any disabled with invalid ds": {
			dataSource: coreDataSource,
			want:       nil,
		},
		"any disabled with volume ds ref": {
			dataSourceRef: volumeDataSourceRef,
		},
		"any disabled with snapshot ds ref": {
			dataSourceRef: snapshotDataSourceRef,
		},
		"any disabled with generic ds ref": {
			dataSourceRef: genericDataSourceRef,
		},
		"any disabled with invalid ds ref": {
			dataSourceRef: coreDataSourceRef,
		},
		"any enabled with empty ds": {
			anyEnabled: true,
			want:       nil,
		},
		"any enabled with volume ds": {
			dataSource: volumeDataSource,
			anyEnabled: true,
			want:       volumeDataSource,
			wantRef:    volumeDataSourceRef,
		},
		"any enabled with snapshot ds": {
			dataSource: snapshotDataSource,
			anyEnabled: true,
			want:       snapshotDataSource,
			wantRef:    snapshotDataSourceRef,
		},
		"any enabled with generic ds": {
			dataSource: genericDataSource,
			anyEnabled: true,
		},
		"any enabled with invalid ds": {
			dataSource: coreDataSource,
			anyEnabled: true,
		},
		"any enabled with volume ds ref": {
			dataSourceRef: volumeDataSourceRef,
			anyEnabled:    true,
			want:          volumeDataSource,
			wantRef:       volumeDataSourceRef,
		},
		"any enabled with snapshot ds ref": {
			dataSourceRef: snapshotDataSourceRef,
			anyEnabled:    true,
			want:          snapshotDataSource,
			wantRef:       snapshotDataSourceRef,
		},
		"any enabled with generic ds ref": {
			dataSourceRef: genericDataSourceRef,
			anyEnabled:    true,
			want:          genericDataSource,
			wantRef:       genericDataSourceRef,
		},
		"any enabled with invalid ds ref": {
			dataSourceRef: coreDataSourceRef,
			anyEnabled:    true,
			want:          coreDataSource,
			wantRef:       coreDataSourceRef,
		},
		"any enabled with mismatched data sources": {
			dataSource:    volumeDataSource,
			dataSourceRef: snapshotDataSourceRef,
			anyEnabled:    true,
			want:          volumeDataSource,
			wantRef:       snapshotDataSourceRef,
		},
		"both any and xns enabled with empty ds": {
			anyEnabled: true,
			xnsEnabled: true,
			want:       nil,
		},
		"both any and xns enabled with volume ds": {
			dataSource: volumeDataSource,
			anyEnabled: true,
			xnsEnabled: true,
			want:       volumeDataSource,
			wantRef:    volumeDataSourceRef,
		},
		"both any and xns enabled with snapshot ds": {
			dataSource: snapshotDataSource,
			anyEnabled: true,
			xnsEnabled: true,
			want:       snapshotDataSource,
			wantRef:    snapshotDataSourceRef,
		},
		"both any and xns enabled with generic ds": {
			dataSource: genericDataSource,
			anyEnabled: true,
			xnsEnabled: true,
		},
		"both any and xns enabled with invalid ds": {
			dataSource: coreDataSource,
			anyEnabled: true,
			xnsEnabled: true,
		},
		"both any and xns enabled with volume ds ref": {
			dataSourceRef: volumeDataSourceRef,
			anyEnabled:    true,
			xnsEnabled:    true,
			want:          volumeDataSource,
			wantRef:       volumeDataSourceRef,
		},
		"both any and xns enabled with snapshot ds ref": {
			dataSourceRef: snapshotDataSourceRef,
			anyEnabled:    true,
			xnsEnabled:    true,
			want:          snapshotDataSource,
			wantRef:       snapshotDataSourceRef,
		},
		"both any and xns enabled with generic ds ref": {
			dataSourceRef: genericDataSourceRef,
			anyEnabled:    true,
			xnsEnabled:    true,
			want:          genericDataSource,
			wantRef:       genericDataSourceRef,
		},
		"both any and xns enabled with invalid ds ref": {
			dataSourceRef: coreDataSourceRef,
			anyEnabled:    true,
			xnsEnabled:    true,
			want:          coreDataSource,
			wantRef:       coreDataSourceRef,
		},
		"both any and xns enabled with mismatched data sources": {
			dataSource:    volumeDataSource,
			dataSourceRef: snapshotDataSourceRef,
			anyEnabled:    true,
			xnsEnabled:    true,
			want:          volumeDataSource,
			wantRef:       snapshotDataSourceRef,
		},
		"both any and xns enabled with volume xns ds ref": {
			dataSourceRef: xnsVolumeDataSourceRef,
			anyEnabled:    true,
			xnsEnabled:    true,
			wantRef:       xnsVolumeDataSourceRef,
		},
		"both any and xns enabled with snapshot xns ds ref": {
			dataSourceRef: xnsSnapshotDataSourceRef,
			anyEnabled:    true,
			xnsEnabled:    true,
			wantRef:       xnsSnapshotDataSourceRef,
		},
		"both any and xns enabled with generic xns ds ref": {
			dataSourceRef: xnsGenericDataSourceRef,
			anyEnabled:    true,
			xnsEnabled:    true,
			wantRef:       xnsGenericDataSourceRef,
		},
		"both any and xns enabled with invalid xns ds ref": {
			dataSourceRef: xnsCoreDataSourceRef,
			anyEnabled:    true,
			xnsEnabled:    true,
			wantRef:       xnsCoreDataSourceRef,
		},
		"only xns enabled with snapshot xns ds ref": {
			dataSourceRef: xnsSnapshotDataSourceRef,
			xnsEnabled:    true,
		},
	}

	for testName, test := range tests {
		t.Run(testName, func(t *testing.T) {
			featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.AnyVolumeDataSource, test.anyEnabled)
			featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.CrossNamespaceVolumeDataSource, test.xnsEnabled)
			pvc := api.PersistentVolumeClaim{
				Spec: api.PersistentVolumeClaimSpec{
					DataSource:    test.dataSource,
					DataSourceRef: test.dataSourceRef,
				},
			}

			// Method under test
			Strategy.PrepareForCreate(ctx, &pvc)

			if !reflect.DeepEqual(pvc.Spec.DataSource, test.want) {
				t.Errorf("data source does not match, test: %s, anyEnabled: %v, dataSource: %v, expected: %v",
					testName, test.anyEnabled, test.dataSource, test.want)
			}
			if !reflect.DeepEqual(pvc.Spec.DataSourceRef, test.wantRef) {
				t.Errorf("data source ref does not match, test: %s, anyEnabled: %v, dataSourceRef: %v, expected: %v",
					testName, test.anyEnabled, test.dataSourceRef, test.wantRef)
			}
		})
	}
}

func TestStorageClassStrategy(t *testing.T) {
	ctx := genericapirequest.NewDefaultContext()

	if !StorageClassStrategy.NamespaceScoped() {
		t.Errorf("PersistentVolumeClaim must be namespace scoped")
	}
	if StorageClassStrategy.AllowCreateOnUpdate() {
		t.Errorf("PersistentVolumeClaim should not allow create on update")
	}

	// Test PrepareForUpdate - security enforcement
	oldPvc := validNewPersistentVolumeClaim("test-pvc", "default")
	oldPvc.Spec.StorageClassName = stringPtr("fast")
	oldPvc.Spec.VolumeName = "existing-volume"
	oldPvc.Status.Phase = api.ClaimBound

	newPvc := oldPvc.DeepCopy()
	newPvc.Spec.AccessModes = []api.PersistentVolumeAccessMode{api.ReadWriteMany} // changed
	newPvc.Spec.Resources = api.VolumeResourceRequirements{
		Requests: api.ResourceList{
			api.ResourceName(api.ResourceStorage): resource.MustParse("20G"), // changed
		},
	}
	newPvc.Spec.StorageClassName = stringPtr("slow") // changed
	newPvc.Spec.VolumeName = "new-volume"            // changed
	newPvc.Status.Phase = api.ClaimPending           // changed

	// Test PrepareForUpdate
	StorageClassStrategy.PrepareForUpdate(ctx, newPvc, oldPvc)

	// Only storage class should be allowed to change, everything else should be reset
	if !reflect.DeepEqual(newPvc.Spec.AccessModes, oldPvc.Spec.AccessModes) {
		t.Errorf("access modes should be reset to old value, got %v, want %v", newPvc.Spec.AccessModes, oldPvc.Spec.AccessModes)
	}
	if !reflect.DeepEqual(newPvc.Spec.Resources, oldPvc.Spec.Resources) {
		t.Errorf("resources should be reset to old value, got %v, want %v", newPvc.Spec.Resources, oldPvc.Spec.Resources)
	}
	if newPvc.Spec.VolumeName != oldPvc.Spec.VolumeName {
		t.Errorf("volume name should be reset to old value, got %v, want %v", newPvc.Spec.VolumeName, oldPvc.Spec.VolumeName)
	}
	if !reflect.DeepEqual(newPvc.Status, oldPvc.Status) {
		t.Errorf("status should be reset to old value, got %v, want %v", newPvc.Status, oldPvc.Status)
	}
	// Storage class should remain changed
	if newPvc.Spec.StorageClassName == nil || *newPvc.Spec.StorageClassName != "slow" {
		t.Errorf("storage class should remain changed, got %v, want %v", newPvc.Spec.StorageClassName, stringPtr("slow"))
	}

	// Test ValidateUpdate - valid case
	validPvc := validNewPersistentVolumeClaim("test-pvc", "default")
	validPvc.Spec.StorageClassName = stringPtr("slow")
	validPvc.ResourceVersion = "1"
	validOldPvc := validNewPersistentVolumeClaim("test-pvc", "default")
	validOldPvc.Spec.StorageClassName = stringPtr("fast")
	validOldPvc.ResourceVersion = "1"
	errs := StorageClassStrategy.ValidateUpdate(ctx, validPvc, validOldPvc)
	if len(errs) != 0 {
		t.Errorf("expected no validation errors for valid storage class change, got: %v", errs)
	}

	// Test ValidateUpdate - invalid case (no change)
	invalidPvc := validNewPersistentVolumeClaim("test-pvc", "default")
	invalidPvc.Spec.StorageClassName = stringPtr("fast")
	invalidPvc.ResourceVersion = "1"
	invalidOldPvc := validNewPersistentVolumeClaim("test-pvc", "default")
	invalidOldPvc.Spec.StorageClassName = stringPtr("fast") // same as new
	invalidOldPvc.ResourceVersion = "1"
	errs = StorageClassStrategy.ValidateUpdate(ctx, invalidPvc, invalidOldPvc)
	if len(errs) == 0 {
		t.Errorf("expected validation errors when storage class is not changing")
	}
}

func stringPtr(s string) *string {
	return &s
}

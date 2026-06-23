// Datadog: **NOT FROM UPSTREAM K8s**

package noderesources

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2/ktesting"
	_ "k8s.io/klog/v2/ktesting/init"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	plfeature "k8s.io/kubernetes/pkg/scheduler/framework/plugins/feature"
)

func regularPods(count int64) []*v1.Pod {
	var pods []*v1.Pod
	for range count {
		pods = append(pods, &v1.Pod{})
	}
	return pods
}

func makeNodeInfoWithPodCapacity(maxPods int64, pods ...*v1.Pod) *framework.NodeInfo {
	ni := framework.NewNodeInfo(pods...)
	ni.SetNode(&v1.Node{
		Status: v1.NodeStatus{
			Capacity: v1.ResourceList{
				v1.ResourcePods: *resource.NewQuantity(maxPods, resource.DecimalSI),
			},
			Allocatable: v1.ResourceList{
				v1.ResourcePods: *resource.NewQuantity(maxPods, resource.DecimalSI),
			},
		},
	})
	return ni
}

func podOverflowAllowedPod() *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				PodOverflowAnnotationKey: "allow",
			},
		},
		Spec: v1.PodSpec{HostNetwork: true},
	}
}

func podFitsNodeResources(t *testing.T, pod *v1.Pod, nodeInfo *framework.NodeInfo) bool {
	t.Helper()

	_, ctx := ktesting.NewTestContext(t)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	p, err := NewFit(ctx, &config.NodeResourcesFitArgs{ScoringStrategy: defaultScoringStrategy}, nil, plfeature.Features{})
	if err != nil {
		t.Fatal(err)
	}

	cycleState := framework.NewCycleState()

	_, preFilterStatus := p.(fwk.PreFilterPlugin).PreFilter(ctx, cycleState, pod, nil)
	if !preFilterStatus.IsSuccess() {
		t.Fatalf("PreFilter failed: %v", preFilterStatus)
	}

	return p.(fwk.FilterPlugin).Filter(ctx, cycleState, pod, nodeInfo).IsSuccess()
}

func TestPodOverflowAllowed(t *testing.T) {
	const maxPods = 2

	tests := []struct {
		name        string
		existing    []*v1.Pod
		newPod      *v1.Pod
		wantSuccess bool
	}{
		{
			name:        "pod without overflow annotation rejected at capacity",
			existing:    regularPods(maxPods),
			newPod:      &v1.Pod{},
			wantSuccess: false,
		},
		{
			name:        "hostNetwork pod with pod-overflow allow accepted at capacity",
			existing:    regularPods(maxPods),
			newPod:      podOverflowAllowedPod(),
			wantSuccess: true,
		},
		{
			name:     "pod-overflow value other than allow rejected",
			existing: regularPods(maxPods),
			newPod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						PodOverflowAnnotationKey: "true",
					},
				},
				Spec: v1.PodSpec{HostNetwork: true},
			},
			wantSuccess: false,
		},
		{
			name:     "pod-overflow allow without hostNetwork rejected",
			existing: regularPods(maxPods),
			newPod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						PodOverflowAnnotationKey: "allow",
					},
				},
			},
			wantSuccess: false,
		},
		{
			name:        "second overflow-allowed pod accepted past capacity",
			existing:    append(regularPods(maxPods), podOverflowAllowedPod()),
			newPod:      podOverflowAllowedPod(),
			wantSuccess: true,
		},
		{
			name:        "resident overflow-allowed pod still contributes to later regular pod count",
			existing:    append(regularPods(maxPods-1), podOverflowAllowedPod()),
			newPod:      &v1.Pod{},
			wantSuccess: false,
		},
		{
			name:        "regular pod rejected when total pods are past capacity",
			existing:    append(regularPods(maxPods), podOverflowAllowedPod()),
			newPod:      &v1.Pod{},
			wantSuccess: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodeInfo := makeNodeInfoWithPodCapacity(maxPods, tc.existing...)

			gotSuccess := podFitsNodeResources(t, tc.newPod, nodeInfo)
			if tc.wantSuccess && !gotSuccess {
				t.Errorf("expected schedulable, got unschedulable")
			}
			if !tc.wantSuccess && gotSuccess {
				t.Errorf("expected unschedulable, got success")
			}
		})
	}
}

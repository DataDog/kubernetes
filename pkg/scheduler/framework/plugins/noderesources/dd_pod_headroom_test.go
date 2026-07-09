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
	"k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	plfeature "k8s.io/kubernetes/pkg/scheduler/framework/plugins/feature"
)

func ddRegularPods(count int64) []*v1.Pod {
	var pods []*v1.Pod
	for range count {
		pods = append(pods, &v1.Pod{})
	}
	return pods
}

func ddHeadroomPod(annotations map[string]string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: annotations,
		},
	}
}

func ddNodeInfo(maxPods, milliCPU int64, memory string, pods ...*v1.Pod) *framework.NodeInfo {
	ni := framework.NewNodeInfo(pods...)
	ni.SetNode(&v1.Node{
		Status: v1.NodeStatus{
			Capacity: v1.ResourceList{
				v1.ResourcePods:   *resource.NewQuantity(maxPods, resource.DecimalSI),
				v1.ResourceCPU:    *resource.NewMilliQuantity(milliCPU, resource.DecimalSI),
				v1.ResourceMemory: resource.MustParse(memory),
			},
			Allocatable: v1.ResourceList{
				v1.ResourcePods:   *resource.NewQuantity(maxPods, resource.DecimalSI),
				v1.ResourceCPU:    *resource.NewMilliQuantity(milliCPU, resource.DecimalSI),
				v1.ResourceMemory: resource.MustParse(memory),
			},
		},
	})
	return ni
}

func ddPodFitsNodeResources(t *testing.T, pod *v1.Pod, nodeInfo *framework.NodeInfo) bool {
	t.Helper()

	_, ctx := ktesting.NewTestContext(t)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	p, err := NewFit(ctx, &config.NodeResourcesFitArgs{ScoringStrategy: defaultScoringStrategy}, nil, plfeature.Features{})
	if err != nil {
		t.Fatal(err)
	}

	cycleState := framework.NewCycleState()

	_, preFilterStatus := p.(framework.PreFilterPlugin).PreFilter(ctx, cycleState, pod, nil)
	if !preFilterStatus.IsSuccess() {
		t.Fatalf("PreFilter failed: %v", preFilterStatus)
	}

	return p.(framework.FilterPlugin).Filter(ctx, cycleState, pod, nodeInfo).IsSuccess()
}

func TestPodHeadroom(t *testing.T) {
	tests := []struct {
		name        string
		maxPods     int64
		milliCPU    int64
		memory      string
		existing    []*v1.Pod
		newPod      *v1.Pod
		wantSuccess bool
	}{
		{
			name:     "pod headroom rejected when only one pod slot remains",
			maxPods:  2,
			milliCPU: 1000,
			memory:   "1Gi",
			existing: ddRegularPods(1),
			newPod: ddHeadroomPod(map[string]string{
				PodHeadroomPodsAnnotationKey: "1",
			}),
			wantSuccess: false,
		},
		{
			name:     "pod headroom accepted when two pod slots remain",
			maxPods:  3,
			milliCPU: 1000,
			memory:   "1Gi",
			existing: ddRegularPods(1),
			newPod: ddHeadroomPod(map[string]string{
				PodHeadroomPodsAnnotationKey: "1",
			}),
			wantSuccess: true,
		},
		{
			name:     "resident pod headroom does not affect later pod count checks",
			maxPods:  3,
			milliCPU: 1000,
			memory:   "1Gi",
			existing: append(ddRegularPods(1), ddHeadroomPod(map[string]string{
				PodHeadroomPodsAnnotationKey: "1",
			})),
			newPod:      &v1.Pod{},
			wantSuccess: true,
		},
		{
			name:     "cpu headroom rejected when it exceeds allocatable cpu",
			maxPods:  10,
			milliCPU: 100,
			memory:   "1Gi",
			newPod: ddHeadroomPod(map[string]string{
				PodHeadroomCPUAnnotationKey: "101m",
			}),
			wantSuccess: false,
		},
		{
			name:     "cpu headroom accepted when it fits allocatable cpu",
			maxPods:  10,
			milliCPU: 100,
			memory:   "1Gi",
			newPod: ddHeadroomPod(map[string]string{
				PodHeadroomCPUAnnotationKey: "100m",
			}),
			wantSuccess: true,
		},
		{
			name:     "memory headroom rejected when it exceeds allocatable memory",
			maxPods:  10,
			milliCPU: 1000,
			memory:   "128Mi",
			newPod: ddHeadroomPod(map[string]string{
				PodHeadroomMemoryAnnotationKey: "129Mi",
			}),
			wantSuccess: false,
		},
		{
			name:     "invalid headroom annotations are ignored",
			maxPods:  1,
			milliCPU: 0,
			memory:   "0",
			newPod: ddHeadroomPod(map[string]string{
				PodHeadroomPodsAnnotationKey:   "not-an-int",
				PodHeadroomCPUAnnotationKey:    "not-a-quantity",
				PodHeadroomMemoryAnnotationKey: "also-not-a-quantity",
			}),
			wantSuccess: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodeInfo := ddNodeInfo(tc.maxPods, tc.milliCPU, tc.memory, tc.existing...)

			gotSuccess := ddPodFitsNodeResources(t, tc.newPod, nodeInfo)
			if tc.wantSuccess && !gotSuccess {
				t.Errorf("expected schedulable, got unschedulable")
			}
			if !tc.wantSuccess && gotSuccess {
				t.Errorf("expected unschedulable, got success")
			}
		})
	}
}

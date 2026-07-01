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

// makeAtCapNodeInfo returns a NodeInfo with maxPods existing pods already scheduled,
// putting the node at its pod capacity.
func makeAtCapNodeInfo(maxPods int64) *framework.NodeInfo {
	var pods []*v1.Pod
	for range maxPods {
		pods = append(pods, &v1.Pod{})
	}
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

func exemptPod() *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ExcludeFromMaxPodCountAnnotationKey: "true",
			},
		},
		Spec: v1.PodSpec{HostNetwork: true},
	}
}

func TestPodCountExemption(t *testing.T) {
	const maxPods = 2

	tests := []struct {
		name        string
		pod         *v1.Pod
		wantSuccess bool
	}{
		{
			name:        "non-exempt pod rejected at capacity",
			pod:         &v1.Pod{},
			wantSuccess: false,
		},
		{
			name:        "exempt pod (hostNetwork + annotation) accepted at capacity",
			pod:         exemptPod(),
			wantSuccess: true,
		},
		{
			name: "wrong annotation value rejected",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ExcludeFromMaxPodCountAnnotationKey: "True",
					},
				},
				Spec: v1.PodSpec{HostNetwork: true},
			},
			wantSuccess: false,
		},
		{
			name: "annotation without hostNetwork rejected",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						ExcludeFromMaxPodCountAnnotationKey: "true",
					},
				},
			},
			wantSuccess: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ctx := ktesting.NewTestContext(t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			p, err := NewFit(ctx, &config.NodeResourcesFitArgs{ScoringStrategy: defaultScoringStrategy}, nil, plfeature.Features{})
			if err != nil {
				t.Fatal(err)
			}

			nodeInfo := makeAtCapNodeInfo(maxPods)
			cycleState := framework.NewCycleState()

			_, preFilterStatus := p.(framework.PreFilterPlugin).PreFilter(ctx, cycleState, tc.pod, nil)
			if !preFilterStatus.IsSuccess() {
				t.Fatalf("PreFilter failed: %v", preFilterStatus)
			}

			got := p.(framework.FilterPlugin).Filter(ctx, cycleState, tc.pod, nodeInfo)
			if tc.wantSuccess && !got.IsSuccess() {
				t.Errorf("expected schedulable, got: %v", got)
			}
			if !tc.wantSuccess && got.IsSuccess() {
				t.Errorf("expected unschedulable, got success")
			}
		})
	}
}

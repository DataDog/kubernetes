// Datadog: **NOT FROM UPSTREAM K8s**

package noderesources

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

const (
	PodOverflowAnnotationKey   = "kubelet.datadoghq.com/pod-overflow"
	podOverflowAnnotationValue = "allow"
)

func podOverflowAllowed(pod *v1.Pod) bool {
	if pod == nil {
		return false
	}
	// Must be host networked to qualify for exclusion from max pod count.
	allowed := pod.Spec.HostNetwork &&
		pod.Annotations[PodOverflowAnnotationKey] == podOverflowAnnotationValue
	if allowed {
		klog.V(4).InfoS("pod allowed to overflow max-pod-count cap", "pod", klog.KObj(pod))
	}
	return allowed
}

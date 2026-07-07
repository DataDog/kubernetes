// Datadog: **NOT FROM UPSTREAM K8s**

package noderesources

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

const (
	ExcludeFromMaxPodCountAnnotationKey   = "kubelet.datadoghq.com/exclude-from-max-pods"
	excludeFromMaxPodCountAnnotationValue = "true"
)

func isExcludedFromMaxPodCount(pod *v1.Pod) bool {
	exempt := podExcludedFromMaxPodCount(pod)
	if exempt {
		klog.V(4).InfoS("pod opted out of max-pod-count cap", "pod", klog.KObj(pod))
	}
	return exempt
}

func podExcludedFromMaxPodCount(pod *v1.Pod) bool {
	if pod == nil {
		return false
	}
	// Must be host networked to qualify for exclusion from max pod count.
	return pod.Spec.HostNetwork &&
		pod.Annotations[ExcludeFromMaxPodCountAnnotationKey] == excludeFromMaxPodCountAnnotationValue
}

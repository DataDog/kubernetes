// Datadog: **NOT FROM UPSTREAM K8s**

package noderesources

import (
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
)

const (
	PodHeadroomPodsAnnotationKey   = "kubelet.datadoghq.com/headroom-pods"
	PodHeadroomCPUAnnotationKey    = "kubelet.datadoghq.com/headroom-cpu"
	PodHeadroomMemoryAnnotationKey = "kubelet.datadoghq.com/headroom-memory"
)

type podHeadroom struct {
	Pods     int
	MilliCPU int64
	Memory   int64
}

func podRequestedHeadroom(pod *v1.Pod) podHeadroom {
	var headroom podHeadroom
	if pod == nil {
		return headroom
	}

	if value, ok := pod.Annotations[PodHeadroomPodsAnnotationKey]; ok {
		pods, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			klog.V(4).InfoS("ignoring invalid pod headroom annotation", "pod", klog.KObj(pod), "annotation", PodHeadroomPodsAnnotationKey, "value", value, "err", err)
		} else if pods > 0 {
			headroom.Pods = pods
		}
	}

	if value, ok := pod.Annotations[PodHeadroomCPUAnnotationKey]; ok {
		cpu, err := resource.ParseQuantity(strings.TrimSpace(value))
		if err != nil {
			klog.V(4).InfoS("ignoring invalid pod headroom annotation", "pod", klog.KObj(pod), "annotation", PodHeadroomCPUAnnotationKey, "value", value, "err", err)
		} else if cpu.MilliValue() > 0 {
			headroom.MilliCPU = cpu.MilliValue()
		}
	}

	if value, ok := pod.Annotations[PodHeadroomMemoryAnnotationKey]; ok {
		memory, err := resource.ParseQuantity(strings.TrimSpace(value))
		if err != nil {
			klog.V(4).InfoS("ignoring invalid pod headroom annotation", "pod", klog.KObj(pod), "annotation", PodHeadroomMemoryAnnotationKey, "value", value, "err", err)
		} else if memory.Value() > 0 {
			headroom.Memory = memory.Value()
		}
	}

	return headroom
}

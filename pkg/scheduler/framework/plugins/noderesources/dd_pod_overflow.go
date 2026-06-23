/*
Copyright The Kubernetes Authors.

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

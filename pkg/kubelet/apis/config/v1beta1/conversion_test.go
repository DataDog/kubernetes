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

package v1beta1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	configv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/kubelet/apis/config"
)

func TestPodStatusUpdateBatchWindow(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		window  time.Duration
		want    time.Duration
	}{
		{name: "disabled"},
		{name: "enabled default", enabled: true, want: time.Second},
		{name: "minimum window", enabled: true, window: 250 * time.Millisecond, want: 250 * time.Millisecond},
		{name: "maximum window", enabled: true, window: 5 * time.Second, want: 5 * time.Second},
		{name: "disabled explicit window preserved for validation", window: time.Second, want: time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			versioned := &configv1beta1.KubeletConfiguration{
				FeatureGates:               map[string]bool{string(features.PodStatusBatchUpdates): tt.enabled},
				PodStatusUpdateBatchWindow: metav1.Duration{Duration: tt.window},
			}
			SetDefaults_KubeletConfiguration(versioned)
			if got := versioned.PodStatusUpdateBatchWindow.Duration; got != tt.want {
				t.Fatalf("defaulted batch window = %v, want %v", got, tt.want)
			}

			internal := &config.KubeletConfiguration{}
			if err := Convert_v1beta1_KubeletConfiguration_To_config_KubeletConfiguration(versioned, internal, nil); err != nil {
				t.Fatal(err)
			}
			if got := internal.PodStatusUpdateBatchWindow.Duration; got != tt.want {
				t.Fatalf("internal batch window = %v, want %v", got, tt.want)
			}

			roundTrip := &configv1beta1.KubeletConfiguration{}
			if err := Convert_config_KubeletConfiguration_To_v1beta1_KubeletConfiguration(internal, roundTrip, nil); err != nil {
				t.Fatal(err)
			}
			if got := roundTrip.PodStatusUpdateBatchWindow.Duration; got != tt.want {
				t.Fatalf("round-tripped batch window = %v, want %v", got, tt.want)
			}
		})
	}
}

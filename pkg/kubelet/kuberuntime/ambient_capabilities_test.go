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

package kuberuntime

import (
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/kubernetes/pkg/features"
)

func TestAmbientCapabilitiesCRIConversion(t *testing.T) {
	require.Nil(t, convertToRuntimeCapabilities(nil))
	for _, caps := range [][]v1.Capability{
		nil,
		{"NET_BIND_SERVICE"},
		{"ALL", "net_bind_service", "NET_BIND_SERVICE", "UNKNOWN"},
	} {
		got := convertToRuntimeCapabilities(&v1.Capabilities{Add: caps, Drop: []v1.Capability{"ALL", "CHOWN"}, Ambient: caps})
		require.Equal(t, got.AddCapabilities, got.AddAmbientCapabilities)
		require.Len(t, got.AddAmbientCapabilities, len(caps))
		for i, capability := range caps {
			require.Equal(t, string(capability), got.AddAmbientCapabilities[i])
		}
		require.Equal(t, []string{"ALL", "CHOWN"}, got.DropCapabilities)
	}
	require.Empty(t, convertToRuntimeCapabilities(&v1.Capabilities{Add: []v1.Capability{"CHOWN"}}).AddAmbientCapabilities)
}

func TestAmbientCapabilitiesKubeletGateDisabled(t *testing.T) {
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.AmbientCapabilities, false)
	m := &kubeGenericRuntimeManager{}
	container := &v1.Container{Name: "ambient", SecurityContext: &v1.SecurityContext{Capabilities: &v1.Capabilities{Ambient: []v1.Capability{"NET_BIND_SERVICE"}}}}
	_, err := m.determineEffectiveSecurityContext(&v1.Pod{}, container, nil, "")
	require.ErrorContains(t, err, "AmbientCapabilities feature gate is disabled")
}

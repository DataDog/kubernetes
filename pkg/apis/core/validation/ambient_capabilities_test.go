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

package validation

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/utils/ptr"
)

// Ambient capabilities follow the existing Add field's name-validation rules.
// Runtime-specific names and ALL/add/drop resolution belong to the runtime.
func TestValidateAmbientCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name       string
		caps, drop []core.Capability
	}{
		{name: "individual", caps: []core.Capability{"NET_BIND_SERVICE"}},
		{name: "reset and add", caps: []core.Capability{"ALL", "NET_BIND_SERVICE"}, drop: []core.Capability{"ALL"}},
		{name: "empty name", caps: []core.Capability{""}},
		{name: "lowercase", caps: []core.Capability{"net_bind_service"}},
		{name: "unknown", caps: []core.Capability{"UNKNOWN"}},
		{name: "CAP prefix", caps: []core.Capability{"CAP_NET_BIND_SERVICE"}},
		{name: "duplicates", caps: []core.Capability{"CHOWN", "CHOWN"}},
		{name: "individual drop", caps: []core.Capability{"CHOWN"}, drop: []core.Capability{"CHOWN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, allowEscalation := range []bool{true, false} {
				ordinary := &core.SecurityContext{Capabilities: &core.Capabilities{Add: tc.caps, Drop: tc.drop}, AllowPrivilegeEscalation: ptr.To(allowEscalation)}
				ambient := &core.SecurityContext{Capabilities: &core.Capabilities{Ambient: tc.caps, Drop: tc.drop}, AllowPrivilegeEscalation: ptr.To(allowEscalation)}
				ordinaryErrors := ValidateSecurityContext(ordinary, field.NewPath("securityContext"), true)
				ambientErrors := ValidateSecurityContext(ambient, field.NewPath("securityContext"), true)
				require.Len(t, ambientErrors, len(ordinaryErrors), "allowPrivilegeEscalation=%v: ordinary=%v ambient=%v", allowEscalation, ordinaryErrors, ambientErrors)
			}
		})
	}
}

func TestValidateAmbientCapabilitiesPrivilegeEscalation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		ambient, drop  []core.Capability
		grantsSysAdmin bool
	}{
		{"explicit", []core.Capability{"SYS_ADMIN"}, nil, true},
		{"lowercase", []core.Capability{"sys_admin"}, nil, true},
		{"all", []core.Capability{"ALL"}, nil, true},
		{"lowercase all", []core.Capability{"all"}, nil, true},
		{"individual drop", []core.Capability{"SYS_ADMIN"}, []core.Capability{"SYS_ADMIN"}, false},
		{"all minus sys admin", []core.Capability{"ALL"}, []core.Capability{"sys_admin"}, false},
		{"all reset", []core.Capability{"ALL"}, []core.Capability{"all"}, false},
		{"explicit after reset", []core.Capability{"ALL", "SYS_ADMIN"}, []core.Capability{"ALL"}, true},
		{"explicit dropped after reset", []core.Capability{"ALL", "SYS_ADMIN"}, []core.Capability{"ALL", "SYS_ADMIN"}, false},
		{"unrelated capability", []core.Capability{"NET_BIND_SERVICE"}, []core.Capability{"ALL"}, false},
		{"unknown prefixed name", []core.Capability{"CAP_SYS_ADMIN"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, allowEscalation := range []*bool{nil, ptr.To(true), ptr.To(false)} {
				sc := &core.SecurityContext{Capabilities: &core.Capabilities{Ambient: tc.ambient, Drop: tc.drop}, AllowPrivilegeEscalation: allowEscalation}
				errs := ValidateSecurityContext(sc, field.NewPath("securityContext"), true)
				if allowEscalation != nil && !*allowEscalation && tc.grantsSysAdmin {
					require.Len(t, errs, 1)
					require.Equal(t, "securityContext.capabilities.ambient", errs[0].Field)
				} else {
					require.Empty(t, errs)
				}
			}
		})
	}
}

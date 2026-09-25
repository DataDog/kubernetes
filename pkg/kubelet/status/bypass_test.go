/*
Copyright 2026 The Kubernetes Authors.

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

package status

import (
	"testing"

	"github.com/stretchr/testify/assert"

	v1 "k8s.io/api/core/v1"
)

func podStatusWithConditions(conditions ...v1.PodCondition) v1.PodStatus {
	return v1.PodStatus{Conditions: conditions}
}

func podStatusWithPhase(phase v1.PodPhase) v1.PodStatus {
	return v1.PodStatus{Phase: phase}
}

func TestShouldBypass(t *testing.T) {
	ready := func(s v1.ConditionStatus) v1.PodCondition {
		return v1.PodCondition{Type: v1.PodReady, Status: s}
	}
	containersReady := func(s v1.ConditionStatus) v1.PodCondition {
		return v1.PodCondition{Type: v1.ContainersReady, Status: s}
	}

	tests := []struct {
		name        string
		forceUpdate bool
		old, new    v1.PodStatus
		want        bool
	}{
		{name: "force update always bypasses", forceUpdate: true, want: true},

		{name: "no change", old: podStatusWithConditions(ready(v1.ConditionFalse)), new: podStatusWithConditions(ready(v1.ConditionFalse)), want: false},

		{name: "PodReady False→True", old: podStatusWithConditions(ready(v1.ConditionFalse)), new: podStatusWithConditions(ready(v1.ConditionTrue)), want: true},
		{name: "PodReady True→False", old: podStatusWithConditions(ready(v1.ConditionTrue)), new: podStatusWithConditions(ready(v1.ConditionFalse)), want: true},
		{name: "PodReady Unknown→True", old: podStatusWithConditions(ready(v1.ConditionUnknown)), new: podStatusWithConditions(ready(v1.ConditionTrue)), want: true},
		{name: "PodReady first report True", old: v1.PodStatus{}, new: podStatusWithConditions(ready(v1.ConditionTrue)), want: true},
		{name: "PodReady first report False is no transition", old: v1.PodStatus{}, new: podStatusWithConditions(ready(v1.ConditionFalse)), want: false},
		{name: "PodReady unchanged True", old: podStatusWithConditions(ready(v1.ConditionTrue)), new: podStatusWithConditions(ready(v1.ConditionTrue)), want: false},

		{name: "ContainersReady False→True", old: podStatusWithConditions(containersReady(v1.ConditionFalse)), new: podStatusWithConditions(containersReady(v1.ConditionTrue)), want: true},
		{name: "ContainersReady True→False", old: podStatusWithConditions(containersReady(v1.ConditionTrue)), new: podStatusWithConditions(containersReady(v1.ConditionFalse)), want: true},
		{name: "ContainersReady unchanged", old: podStatusWithConditions(containersReady(v1.ConditionTrue)), new: podStatusWithConditions(containersReady(v1.ConditionTrue)), want: false},

		// Missing conditions default to ConditionFalse: removal of a True condition
		// is observed as True→False and must bypass.
		{name: "PodReady removed from conditions", old: podStatusWithConditions(ready(v1.ConditionTrue)), new: v1.PodStatus{}, want: true},
		{name: "ContainersReady removed from conditions", old: podStatusWithConditions(containersReady(v1.ConditionTrue)), new: v1.PodStatus{}, want: true},

		{name: "phase Running→Failed", old: podStatusWithPhase(v1.PodRunning), new: podStatusWithPhase(v1.PodFailed), want: true},
		{name: "phase Running→Succeeded", old: podStatusWithPhase(v1.PodRunning), new: podStatusWithPhase(v1.PodSucceeded), want: true},
		{name: "phase already terminal", old: podStatusWithPhase(v1.PodFailed), new: podStatusWithPhase(v1.PodFailed), want: false},
		{name: "phase Pending→Running (non-terminal)", old: podStatusWithPhase(v1.PodPending), new: podStatusWithPhase(v1.PodRunning), want: false},
		// Rule is "new phase is terminal", not "transitioned into terminal":
		// backward transitions do not bypass; terminal→terminal changes do.
		{name: "phase Failed→Running (backward)", old: podStatusWithPhase(v1.PodFailed), new: podStatusWithPhase(v1.PodRunning), want: false},
		{name: "phase Failed→Succeeded (terminal→terminal)", old: podStatusWithPhase(v1.PodFailed), new: podStatusWithPhase(v1.PodSucceeded), want: true},

		{
			name: "readiness change and terminal phase combined",
			old:  v1.PodStatus{Phase: v1.PodRunning, Conditions: []v1.PodCondition{ready(v1.ConditionTrue)}},
			new:  v1.PodStatus{Phase: v1.PodFailed, Conditions: []v1.PodCondition{ready(v1.ConditionFalse)}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldBypass(tt.forceUpdate, &tt.old, &tt.new))
		})
	}
}

func TestBypassReason(t *testing.T) {
	tests := []struct {
		name        string
		forceUpdate bool
		old, new    v1.PodStatus
		want        string
	}{
		{"force", true, v1.PodStatus{}, v1.PodStatus{}, "force"},
		{"pod_ready", false,
			podStatusWithConditions(v1.PodCondition{Type: v1.PodReady, Status: v1.ConditionFalse}),
			podStatusWithConditions(v1.PodCondition{Type: v1.PodReady, Status: v1.ConditionTrue}),
			"pod_ready"},
		{"containers_ready", false,
			podStatusWithConditions(v1.PodCondition{Type: v1.ContainersReady, Status: v1.ConditionFalse}),
			podStatusWithConditions(v1.PodCondition{Type: v1.ContainersReady, Status: v1.ConditionTrue}),
			"containers_ready"},
		{"terminal", false,
			podStatusWithPhase(v1.PodRunning),
			podStatusWithPhase(v1.PodFailed),
			"terminal"},
		// Ordering contract: when PodReady and phase-terminal both transition,
		// "pod_ready" wins because bypassReason evaluates conditions first.
		{"pod_ready wins over terminal", false,
			v1.PodStatus{Phase: v1.PodRunning, Conditions: []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionTrue}}},
			v1.PodStatus{Phase: v1.PodFailed, Conditions: []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionFalse}}},
			"pod_ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, bypassReason(tt.forceUpdate, &tt.old, &tt.new))
		})
	}
}

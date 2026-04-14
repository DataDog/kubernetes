/*
Copyright 2024 The Kubernetes Authors.

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

func podStatusWithReady(status v1.ConditionStatus) v1.PodStatus {
	return v1.PodStatus{
		Conditions: []v1.PodCondition{
			{Type: v1.PodReady, Status: status},
		},
	}
}

func podStatusWithPhase(phase v1.PodPhase) v1.PodStatus {
	return v1.PodStatus{Phase: phase}
}

func TestShouldBypass_ForceUpdate(t *testing.T) {
	old := v1.PodStatus{}
	new := v1.PodStatus{}
	assert.True(t, shouldBypass(true, &old, &new), "forceUpdate=true should always bypass")
}

func TestShouldBypass_NoChange(t *testing.T) {
	old := podStatusWithReady(v1.ConditionFalse)
	new := podStatusWithReady(v1.ConditionFalse)
	assert.False(t, shouldBypass(false, &old, &new), "no change should not bypass")
}

func TestShouldBypass_PodBecameReady(t *testing.T) {
	old := podStatusWithReady(v1.ConditionFalse)
	new := podStatusWithReady(v1.ConditionTrue)
	assert.True(t, shouldBypass(false, &old, &new), "PodReady False→True should bypass")
}

func TestShouldBypass_PodBecameUnready(t *testing.T) {
	old := podStatusWithReady(v1.ConditionTrue)
	new := podStatusWithReady(v1.ConditionFalse)
	assert.True(t, shouldBypass(false, &old, &new), "PodReady True→False should bypass")
}

func TestShouldBypass_PodReadyFromUnknown(t *testing.T) {
	old := podStatusWithReady(v1.ConditionUnknown)
	new := podStatusWithReady(v1.ConditionTrue)
	assert.True(t, shouldBypass(false, &old, &new), "PodReady Unknown→True should bypass")
}

func TestShouldBypass_PodReadyFirstReport(t *testing.T) {
	old := v1.PodStatus{}
	new := podStatusWithReady(v1.ConditionTrue)
	assert.True(t, shouldBypass(false, &old, &new), "first report with PodReady=True should bypass")
}

func TestShouldBypass_PodReadyFirstReportNotReady(t *testing.T) {
	old := v1.PodStatus{}
	new := podStatusWithReady(v1.ConditionFalse)
	// First report with PodReady=False — old has no PodReady condition (implicitly not-ready).
	// No transition occurred, so no bypass.
	assert.False(t, shouldBypass(false, &old, &new), "first report with PodReady=False should not bypass (no transition)")
}

func TestShouldBypass_PodReadyUnchangedTrue(t *testing.T) {
	old := podStatusWithReady(v1.ConditionTrue)
	new := podStatusWithReady(v1.ConditionTrue)
	assert.False(t, shouldBypass(false, &old, &new), "PodReady unchanged (True→True) should not bypass")
}

func TestShouldBypass_TerminalPhaseFailed(t *testing.T) {
	old := podStatusWithPhase(v1.PodRunning)
	new := podStatusWithPhase(v1.PodFailed)
	assert.True(t, shouldBypass(false, &old, &new), "phase transition to Failed should bypass")
}

func TestShouldBypass_TerminalPhaseSucceeded(t *testing.T) {
	old := podStatusWithPhase(v1.PodRunning)
	new := podStatusWithPhase(v1.PodSucceeded)
	assert.True(t, shouldBypass(false, &old, &new), "phase transition to Succeeded should bypass")
}

func TestShouldBypass_TerminalPhaseAlreadyTerminal(t *testing.T) {
	old := podStatusWithPhase(v1.PodFailed)
	new := podStatusWithPhase(v1.PodFailed)
	assert.False(t, shouldBypass(false, &old, &new), "unchanged terminal phase should not bypass")
}

func TestShouldBypass_NonTerminalPhaseChange(t *testing.T) {
	old := podStatusWithPhase(v1.PodPending)
	new := podStatusWithPhase(v1.PodRunning)
	assert.False(t, shouldBypass(false, &old, &new), "Pending→Running should not bypass")
}

func TestShouldBypass_CombinedReadyAndTerminal(t *testing.T) {
	old := v1.PodStatus{
		Phase:      v1.PodRunning,
		Conditions: []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionTrue}},
	}
	new := v1.PodStatus{
		Phase:      v1.PodFailed,
		Conditions: []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionFalse}},
	}
	assert.True(t, shouldBypass(false, &old, &new), "both readiness change and terminal phase should bypass")
}

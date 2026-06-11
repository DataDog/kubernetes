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
	"math/rand/v2"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/kubernetes/pkg/kubelet/metrics"
	"k8s.io/utils/clock"
)

// statusBatcher debounces pod status updates within a configurable time window.
// Lock ordering: callers must acquire podStatusesLock before batcher.mu if both
// are needed. The timer callback only acquires batcher.mu, and notify must not
// re-enter podStatusesLock.
type statusBatcher struct {
	clock  clock.WithDelayedExecution
	window time.Duration

	mu          sync.Mutex
	timers      map[types.UID]clock.Timer
	firstChange map[types.UID]time.Time

	notify func()
}

func newStatusBatcher(c clock.WithDelayedExecution, window time.Duration, notify func()) *statusBatcher {
	return &statusBatcher{
		clock:       c,
		window:      window,
		timers:      make(map[types.UID]clock.Timer),
		firstChange: make(map[types.UID]time.Time),
		notify:      notify,
	}
}

// PendingCount returns the number of pods with pending batched updates.
// Safe to call from any goroutine.
func (b *statusBatcher) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.timers)
}

func (b *statusBatcher) Schedule(uid types.UID) {
	if b.window == 0 {
		b.notify()
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clock.Now()

	// Bound coalescing: if the first pending change is older than 3 windows,
	// flush immediately so a chatty pod cannot starve its readiness signals
	// indefinitely. Worst-case staleness is ~3*window + jitter.
	if first, ok := b.firstChange[uid]; ok {
		if now.Sub(first) >= 3*b.window {
			b.flushLocked(uid)
			return
		}
	} else {
		b.firstChange[uid] = now
	}

	// 0-20% upper-bound jitter desynchronizes per-pod flushes across nodes to
	// avoid a thundering-herd at the API server. window/5 is guaranteed > 0 by
	// validation enforcing window >= 250ms; EnableBatching guards window > 0.
	delay := b.window + time.Duration(rand.Int64N(int64(b.window/5)))

	if t, ok := b.timers[uid]; ok {
		if t.Reset(delay) {
			// Timer was still pending; the existing callback now fires at the
			// new deadline. This is the only true coalesce path.
			metrics.PodStatusBatchCoalesced.Inc()
			return
		}
		// Timer has already fired. clock.AfterFunc semantics: Reset on a fired
		// timer also schedules the same closure to run again at now+delay, and
		// the original invocation is in-flight (blocked on b.mu). We drop the
		// map entry and install a fresh timer below; the stale closures detect
		// the swap via the timer-identity check inside the new closure.
		delete(b.timers, uid)
	}

	firstChange := b.firstChange[uid]
	var newTimer clock.Timer
	newTimer = b.clock.AfterFunc(delay, func() {
		b.mu.Lock()
		if b.timers[uid] != newTimer {
			// Stale callback: either a newer Schedule (or Cancel/Flush) replaced
			// us, or this is an extra fire from a Reset(false) on an already-
			// expired timer. Either way the current owner is responsible for
			// emitting the notify and the delay observation.
			b.mu.Unlock()
			return
		}
		delete(b.timers, uid)
		delete(b.firstChange, uid)
		metrics.PodStatusBatchPending.Set(float64(len(b.timers)))
		b.mu.Unlock()
		metrics.PodStatusBatchDelay.Observe(time.Since(firstChange).Seconds())
		b.notify()
	})
	b.timers[uid] = newTimer
	metrics.PodStatusBatchPending.Set(float64(len(b.timers)))
}

func (b *statusBatcher) Flush(uid types.UID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.flushLocked(uid)
}

func (b *statusBatcher) flushLocked(uid types.UID) {
	if t, ok := b.timers[uid]; ok {
		t.Stop()
		delete(b.timers, uid)
		metrics.PodStatusBatchPending.Set(float64(len(b.timers)))
	}
	delete(b.firstChange, uid)
	b.notify()
}

// Cancel drops any pending batched update for uid without flushing it. The
// caller must ensure the discarded state is no longer needed (e.g. the pod is
// being removed). Returns true if a pending entry was discarded.
func (b *statusBatcher) Cancel(uid types.UID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.timers[uid]
	if ok {
		t.Stop()
		delete(b.timers, uid)
		metrics.PodStatusBatchPending.Set(float64(len(b.timers)))
	}
	delete(b.firstChange, uid)
	return ok
}

// shouldBypass reports whether a pod status update must skip batching to
// preserve low-latency signals to downstream controllers (e.g. endpoints,
// service readiness). The bypass set is intentionally narrow: readiness
// condition transitions (PodReady, ContainersReady), terminal phase
// transitions, and explicit forceUpdate calls (e.g. TerminatePod). Any
// change to this contract is visible to controllers that depend on prompt
// status propagation.
func shouldBypass(forceUpdate bool, old, new *v1.PodStatus) bool {
	return forceUpdate ||
		conditionChanged(old, new, v1.PodReady) ||
		conditionChanged(old, new, v1.ContainersReady) ||
		phaseIsTerminal(old, new)
}

func conditionChanged(old, new *v1.PodStatus, condType v1.PodConditionType) bool {
	return getConditionStatus(old, condType) != getConditionStatus(new, condType)
}

// getConditionStatus returns the status of the named condition, treating a
// missing condition as ConditionFalse. The False default (rather than Unknown)
// is deliberate: removal of a True condition is observed as True -> False,
// which conditionChanged then reports as a transition that bypasses batching.
// Switching this default to Unknown would silently delay condition-removal
// events that downstream controllers rely on.
func getConditionStatus(status *v1.PodStatus, condType v1.PodConditionType) v1.ConditionStatus {
	for _, c := range status.Conditions {
		if c.Type == condType {
			return c.Status
		}
	}
	return v1.ConditionFalse
}

// bypassReason returns a stable label value identifying why an update bypassed
// batching. The label values are part of the metric contract and must stay in
// sync with the priority order in shouldBypass.
func bypassReason(forceUpdate bool, old, new *v1.PodStatus) string {
	switch {
	case forceUpdate:
		return "force"
	case conditionChanged(old, new, v1.PodReady):
		return "pod_ready"
	case conditionChanged(old, new, v1.ContainersReady):
		return "containers_ready"
	case phaseIsTerminal(old, new):
		return "terminal"
	default:
		return "unknown"
	}
}

func phaseIsTerminal(old, new *v1.PodStatus) bool {
	return old.Phase != new.Phase && podutil.IsPodPhaseTerminal(new.Phase)
}

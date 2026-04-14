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
	"math/rand"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet/metrics"
	"k8s.io/utils/clock"
)

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

func (b *statusBatcher) Schedule(uid types.UID) {
	if b.window == 0 {
		b.notify()
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clock.Now()

	if first, ok := b.firstChange[uid]; ok {
		if now.Sub(first) >= 3*b.window {
			b.flushLocked(uid)
			return
		}
	} else {
		b.firstChange[uid] = now
	}

	delay := b.window + time.Duration(rand.Int63n(int64(b.window/5)))

	if t, ok := b.timers[uid]; ok {
		t.Reset(delay)
		metrics.PodStatusBatchCoalesced.Inc()
		return
	}

	firstChange := b.firstChange[uid]
	b.timers[uid] = b.clock.AfterFunc(delay, func() {
		b.mu.Lock()
		delete(b.timers, uid)
		delete(b.firstChange, uid)
		pending := len(b.timers)
		b.mu.Unlock()
		metrics.PodStatusBatchDelay.Observe(time.Since(firstChange).Seconds())
		metrics.PodStatusBatchPending.Set(float64(pending))
		b.notify()
	})
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
	}
	delete(b.firstChange, uid)
	b.notify()
}

func (b *statusBatcher) Cancel(uid types.UID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.timers[uid]; ok {
		t.Stop()
		delete(b.timers, uid)
	}
	delete(b.firstChange, uid)
}

func shouldBypass(forceUpdate bool, old, new *v1.PodStatus) bool {
	if forceUpdate {
		return true
	}
	if podReadyChanged(old, new) {
		return true
	}
	if phaseIsTerminal(old, new) {
		return true
	}
	return false
}

func podReadyChanged(old, new *v1.PodStatus) bool {
	oldReady := getPodReadyStatus(old)
	newReady := getPodReadyStatus(new)
	return oldReady != newReady
}

func getPodReadyStatus(status *v1.PodStatus) v1.ConditionStatus {
	for _, c := range status.Conditions {
		if c.Type == v1.PodReady {
			return c.Status
		}
	}
	return v1.ConditionFalse
}

func bypassReason(forceUpdate bool, old, new *v1.PodStatus) string {
	if forceUpdate {
		return "force"
	}
	if podReadyChanged(old, new) {
		return "ready"
	}
	if phaseIsTerminal(old, new) {
		return "terminal"
	}
	return "unknown"
}

func phaseIsTerminal(old, new *v1.PodStatus) bool {
	if old.Phase == new.Phase {
		return false
	}
	return new.Phase == v1.PodFailed || new.Phase == v1.PodSucceeded
}

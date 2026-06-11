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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/types"
	testingclock "k8s.io/utils/clock/testing"
)

func newTestBatcher(t *testing.T, window time.Duration) (*statusBatcher, *testingclock.FakeClock, *atomic.Int32) {
	t.Helper()
	fakeClock := testingclock.NewFakeClock(time.Now())
	notifyCount := &atomic.Int32{}
	notify := func() {
		notifyCount.Add(1)
	}
	b := newStatusBatcher(fakeClock, window, notify)
	return b, fakeClock, notifyCount
}

func TestBatcherScheduleCoalesces(t *testing.T) {
	b, fakeClock, notifyCount := newTestBatcher(t, 1*time.Second)

	uid := types.UID("pod-1")

	b.Schedule(uid)
	b.Schedule(uid)
	b.Schedule(uid)

	assert.Equal(t, int32(0), notifyCount.Load(), "no notification before window expires")
	assert.Equal(t, 1, fakeClock.Waiters(), "exactly one timer should be active")

	// Window is 1s + up to 200ms jitter, so step 1.3s to be safe.
	fakeClock.Step(1300 * time.Millisecond)

	require.Eventually(t, func() bool {
		return notifyCount.Load() == 1
	}, time.Second, 10*time.Millisecond, "exactly one notification after window expires")

	b.mu.Lock()
	_, hasTimer := b.timers[uid]
	_, hasFirstChange := b.firstChange[uid]
	b.mu.Unlock()
	assert.False(t, hasTimer, "timer should be cleaned up")
	assert.False(t, hasFirstChange, "firstChange should be cleaned up")
}

func TestBatcherScheduleResetsTimer(t *testing.T) {
	b, fakeClock, notifyCount := newTestBatcher(t, 1*time.Second)

	uid := types.UID("pod-1")

	b.Schedule(uid)
	fakeClock.Step(800 * time.Millisecond)
	assert.Equal(t, int32(0), notifyCount.Load(), "no notification before window")

	// Reset the timer by scheduling again.
	b.Schedule(uid)
	fakeClock.Step(800 * time.Millisecond)
	assert.Equal(t, int32(0), notifyCount.Load(), "timer was reset, still no notification")

	// Window is 1s + up to 200ms jitter from the reset point. Step enough to cover max jitter.
	fakeClock.Step(500 * time.Millisecond)
	require.Eventually(t, func() bool {
		return notifyCount.Load() == 1
	}, time.Second, 10*time.Millisecond, "notification fires after reset window expires")
}

func TestBatcherMaxCoalesceCap(t *testing.T) {
	window := 1 * time.Second
	b, fakeClock, notifyCount := newTestBatcher(t, window)

	uid := types.UID("pod-1")

	b.Schedule(uid)

	// Keep resetting the timer, but the max coalesce cap (3x window) should force a flush.
	for i := 0; i < 5; i++ {
		fakeClock.Step(900 * time.Millisecond)
		if notifyCount.Load() > 0 {
			break
		}
		b.Schedule(uid)
	}

	require.Eventually(t, func() bool {
		return notifyCount.Load() == 1
	}, time.Second, 10*time.Millisecond, "notification should fire due to max coalesce cap (3x window)")
}

func TestBatcherFlushImmediate(t *testing.T) {
	b, _, notifyCount := newTestBatcher(t, 1*time.Second)

	uid := types.UID("pod-1")

	b.Schedule(uid)
	assert.Equal(t, int32(0), notifyCount.Load())

	b.Flush(uid)
	assert.Equal(t, int32(1), notifyCount.Load(), "Flush should notify immediately")

	b.mu.Lock()
	_, hasTimer := b.timers[uid]
	_, hasFirstChange := b.firstChange[uid]
	b.mu.Unlock()
	assert.False(t, hasTimer, "timer should be cancelled")
	assert.False(t, hasFirstChange, "firstChange should be cleaned up")
}

func TestBatcherFlushWithoutSchedule(t *testing.T) {
	b, _, notifyCount := newTestBatcher(t, 1*time.Second)

	uid := types.UID("pod-1")
	b.Flush(uid)
	assert.Equal(t, int32(1), notifyCount.Load(), "Flush without Schedule should still notify")
}

func TestBatcherCancel(t *testing.T) {
	b, fakeClock, notifyCount := newTestBatcher(t, 1*time.Second)

	uid := types.UID("pod-1")

	b.Schedule(uid)
	b.Cancel(uid)

	// Timer should be removed — no waiters.
	assert.Equal(t, 0, fakeClock.Waiters(), "cancelled timer should have no waiters")
	assert.Equal(t, int32(0), notifyCount.Load(), "Cancel should prevent notification")

	b.mu.Lock()
	_, hasTimer := b.timers[uid]
	_, hasFirstChange := b.firstChange[uid]
	b.mu.Unlock()
	assert.False(t, hasTimer)
	assert.False(t, hasFirstChange)
}

func TestBatcherCancelWithoutSchedule(t *testing.T) {
	b, _, notifyCount := newTestBatcher(t, 1*time.Second)

	uid := types.UID("pod-1")
	b.Cancel(uid)
	assert.Equal(t, int32(0), notifyCount.Load(), "Cancel without Schedule should be a no-op")
}

func TestBatcherMultiplePods(t *testing.T) {
	b, fakeClock, notifyCount := newTestBatcher(t, 1*time.Second)

	uid1 := types.UID("pod-1")
	uid2 := types.UID("pod-2")

	b.Schedule(uid1)
	b.Schedule(uid2)

	assert.Equal(t, 2, fakeClock.Waiters(), "two timers should be active")

	fakeClock.Step(1300 * time.Millisecond)

	require.Eventually(t, func() bool {
		return notifyCount.Load() == 2
	}, time.Second, 10*time.Millisecond, "both pods should notify")
}

func TestBatcherFlushOneCancelOther(t *testing.T) {
	b, fakeClock, notifyCount := newTestBatcher(t, 1*time.Second)

	uid1 := types.UID("pod-1")
	uid2 := types.UID("pod-2")

	b.Schedule(uid1)
	b.Schedule(uid2)

	b.Flush(uid1)
	b.Cancel(uid2)

	assert.Equal(t, int32(1), notifyCount.Load(), "only flush should notify")
	assert.Equal(t, 0, fakeClock.Waiters(), "all timers should be gone")
}

func TestBatcherDisabledWhenZeroWindow(t *testing.T) {
	b, _, notifyCount := newTestBatcher(t, 0)

	uid := types.UID("pod-1")
	b.Schedule(uid)

	// With zero window, Schedule should flush immediately.
	assert.Equal(t, int32(1), notifyCount.Load(), "zero window should notify immediately")
}

func TestBatcherJitterRange(t *testing.T) {
	fakeClock := testingclock.NewFakeClock(time.Now())
	window := 1 * time.Second

	for i := 0; i < 10; i++ {
		notifyCount := &atomic.Int32{}
		b := newStatusBatcher(fakeClock, window, func() {
			notifyCount.Add(1)
		})

		uid := types.UID("pod-jitter")
		b.Schedule(uid)

		// Window + max jitter = 1s + 200ms = 1.2s. Step 1.3s to be safe.
		fakeClock.Step(1300 * time.Millisecond)
		require.Eventually(t, func() bool {
			return notifyCount.Load() == 1
		}, time.Second, 10*time.Millisecond, "timer should fire within jitter range (iteration %d)", i)
	}
}

func TestBatcherPendingCount(t *testing.T) {
	b, _, _ := newTestBatcher(t, 1*time.Second)

	assert.Equal(t, 0, b.PendingCount())

	b.Schedule(types.UID("pod-1"))
	assert.Equal(t, 1, b.PendingCount())

	b.Schedule(types.UID("pod-2"))
	assert.Equal(t, 2, b.PendingCount())

	b.Cancel(types.UID("pod-1"))
	assert.Equal(t, 1, b.PendingCount())

	b.Flush(types.UID("pod-2"))
	assert.Equal(t, 0, b.PendingCount())
}

func TestBatcherScheduleAfterCancel(t *testing.T) {
	b, fakeClock, notifyCount := newTestBatcher(t, 1*time.Second)

	uid := types.UID("pod-1")

	b.Schedule(uid)
	b.Cancel(uid)
	assert.Equal(t, int32(0), notifyCount.Load())

	// Schedule again — should create a fresh timer.
	b.Schedule(uid)
	assert.Equal(t, 1, fakeClock.Waiters(), "new timer should be active")

	fakeClock.Step(1300 * time.Millisecond)
	require.Eventually(t, func() bool {
		return notifyCount.Load() == 1
	}, time.Second, 10*time.Millisecond, "new timer should fire")
}

func TestBatcherScheduleAfterFlush(t *testing.T) {
	b, fakeClock, notifyCount := newTestBatcher(t, 1*time.Second)

	uid := types.UID("pod-1")

	b.Schedule(uid)
	b.Flush(uid)
	assert.Equal(t, int32(1), notifyCount.Load())

	// Schedule again — should create a fresh timer with fresh firstChange.
	b.Schedule(uid)
	assert.Equal(t, 1, fakeClock.Waiters())

	fakeClock.Step(1300 * time.Millisecond)
	require.Eventually(t, func() bool {
		return notifyCount.Load() == 2
	}, time.Second, 10*time.Millisecond, "second timer should fire independently")
}

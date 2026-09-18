// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package mokkacontroller

import (
	"sort"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/util/workqueue"

	"github.com/stretchr/testify/require"
)

const (
	testStatusDebounce = 100 * time.Millisecond
	testStatusProgress = 4 * testStatusDebounce
)

func TestStatusCoalescerBoundsHundredThousandProjectionNotifications(t *testing.T) {
	coalescer, queue, fakeClock := newTestStatusCoalescer(t, testStatusDebounce)
	key := testInventoryStatusKey()

	for range 100_000 {
		coalescer.dirty(key)
	}
	require.Equal(t, 1, fakeClock.Waiters())
	require.Zero(t, queue.Len())

	fakeClock.Step(testStatusDebounce - time.Nanosecond)
	require.Zero(t, queue.Len())
	fakeClock.Step(time.Nanosecond)
	require.Equal(t, []statusKey{key}, drainQueue(queue))
}

func TestStatusCoalescerMakesBoundedProgressDuringSustainedChanges(t *testing.T) {
	coalescer, queue, fakeClock := newTestStatusCoalescer(t, testStatusDebounce)
	key := testInventoryStatusKey()

	coalescer.dirty(key)
	for range 7 {
		fakeClock.Step(testStatusDebounce / 2)
		coalescer.dirty(key)
		require.Zero(t, queue.Len(), "a busy stream must wait for its progress deadline")
	}
	fakeClock.Step(testStatusDebounce / 2)

	queued := startStatusWork(t, coalescer, queue)
	require.Equal(t, key, queued)
	coalescer.dirty(key)
	finishStatusWork(coalescer, queue, queued, true)

	for range 7 {
		fakeClock.Step(testStatusDebounce / 2)
		coalescer.dirty(key)
		require.Zero(t, queue.Len())
	}
	fakeClock.Step(testStatusDebounce / 2)
	require.Equal(t, []statusKey{key}, drainQueue(queue),
		"a sustained stream must emit progress once per progress interval")
}

func TestStatusCoalescerDeliversFinalTrailingChange(t *testing.T) {
	coalescer, queue, fakeClock := newTestStatusCoalescer(t, testStatusDebounce)
	key := testInventoryStatusKey()

	coalescer.dirty(key)
	fakeClock.Step(testStatusDebounce)
	queued := startStatusWork(t, coalescer, queue)
	coalescer.dirty(key)
	finishStatusWork(coalescer, queue, queued, true)

	fakeClock.Step(testStatusDebounce / 2)
	coalescer.dirty(key)
	fakeClock.Step(testStatusDebounce / 2)
	require.Zero(t, queue.Len(), "a later dirty notification must reset the quiet deadline")
	fakeClock.Step(testStatusDebounce / 2)
	trailing := startStatusWork(t, coalescer, queue)
	require.Equal(t, key, trailing)
	finishStatusWork(coalescer, queue, trailing, true)

	fakeClock.Step(10 * testStatusDebounce)
	require.Zero(t, queue.Len())
	require.Zero(t, fakeClock.Waiters())
}

func TestStatusCoalescerTracksDistinctKeysIndependently(t *testing.T) {
	coalescer, queue, fakeClock := newTestStatusCoalescer(t, testStatusDebounce)
	inventory := testInventoryStatusKey()
	rack := statusKey{kind: statusRack, name: "rack", uid: "rack-uid"}

	coalescer.dirty(inventory)
	fakeClock.Step(testStatusDebounce / 2)
	coalescer.dirty(rack)
	fakeClock.Step(testStatusDebounce / 2)
	require.Equal(t, []statusKey{inventory}, drainQueue(queue))
	fakeClock.Step(testStatusDebounce / 2)
	require.Equal(t, []statusKey{rack}, drainQueue(queue))
}

func TestStatusCoalescerRetainsChangesWhileQueuedOrInFlight(t *testing.T) {
	for _, phase := range []string{"queued", "in-flight"} {
		t.Run(phase, func(t *testing.T) {
			coalescer, queue, fakeClock := newTestStatusCoalescer(t, testStatusDebounce)
			key := testInventoryStatusKey()

			coalescer.dirty(key)
			fakeClock.Step(testStatusDebounce)
			if phase == "in-flight" {
				startStatusWork(t, coalescer, queue)
			}
			for range 10 {
				coalescer.dirty(key)
			}
			if phase == "queued" {
				startStatusWork(t, coalescer, queue)
			}
			finishStatusWork(coalescer, queue, key, true)

			fakeClock.Step(testStatusDebounce)
			require.Equal(t, []statusKey{key}, drainQueue(queue))
		})
	}
}

func TestStatusCoalescerZeroDebouncePreservesImmediateQueueSemantics(t *testing.T) {
	coalescer, queue, fakeClock := newTestStatusCoalescer(t, 0)
	key := testInventoryStatusKey()

	coalescer.dirty(key)
	coalescer.dirty(key)
	require.Equal(t, 1, queue.Len())
	require.Zero(t, fakeClock.Waiters())

	queued := startStatusWork(t, coalescer, queue)
	coalescer.dirty(key)
	finishStatusWork(coalescer, queue, queued, true)
	require.Equal(t, []statusKey{key}, drainQueue(queue))
}

func TestStatusCoalescerRetainsDirtyStateAcrossFailedReconcile(t *testing.T) {
	coalescer, queue, fakeClock := newTestStatusCoalescer(t, testStatusDebounce)
	key := testInventoryStatusKey()

	coalescer.dirty(key)
	fakeClock.Step(testStatusDebounce)
	queued := startStatusWork(t, coalescer, queue)
	coalescer.dirty(key)
	finishStatusWork(coalescer, queue, queued, false)

	fakeClock.Step(10 * testStatusDebounce)
	require.Zero(t, queue.Len(), "the existing rate limiter owns failed reconciliation retries")
	queue.Add(key) // Model the existing rate limiter delivering its retry.
	retry := startStatusWork(t, coalescer, queue)
	finishStatusWork(coalescer, queue, retry, true)
	fakeClock.Step(0)
	require.Equal(t, []statusKey{key}, drainQueue(queue),
		"a successful retry must still deliver changes observed during the failed attempt")
}

func TestStatusCoalescerShutdownCancelsTimersAndRejectsChanges(t *testing.T) {
	fakeClock := newFakeStatusScheduler(time.Unix(1, 0))
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[statusKey]())
	coalescer := newStatusCoalescer(queue, testStatusDebounce, testStatusProgress, fakeClock)

	coalescer.dirty(testInventoryStatusKey())
	coalescer.dirty(statusKey{kind: statusRack, name: "rack", uid: "rack-uid"})
	require.Equal(t, 2, fakeClock.Waiters())
	coalescer.shutdown()
	require.Zero(t, fakeClock.Waiters())

	queue.ShutDown()
	coalescer.dirty(testInventoryStatusKey())
	fakeClock.Step(testStatusDebounce)
	require.Zero(t, queue.Len())
}

func TestStatusCoalescerShutdownWaitsForDispatchAndNeverEnqueuesAfterReturn(t *testing.T) {
	key := testInventoryStatusKey()
	queue := newBlockingStatusQueue()
	t.Cleanup(queue.ShutDown)
	coalescer := newStatusCoalescer(queue, testStatusDebounce, testStatusProgress, realStatusScheduler{})
	coalescer.states[key] = &statusSchedule{dirty: true, timerSequence: 1}

	dispatchDone := make(chan struct{})
	go func() {
		coalescer.dispatch(key, 1)
		close(dispatchDone)
	}()
	<-queue.addStarted

	shutdownDone := make(chan struct{})
	go func() {
		coalescer.shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned before an active dispatch finished")
	default:
	}
	close(queue.releaseAdd)
	<-dispatchDone
	<-shutdownDone

	coalescer.dispatch(key, 1)
	require.Equal(t, 1, queue.addCalls())
}

func newTestStatusCoalescer(
	t *testing.T,
	debounce time.Duration,
) (*statusCoalescer, workqueue.TypedRateLimitingInterface[statusKey], *fakeStatusScheduler) {
	t.Helper()
	fakeClock := newFakeStatusScheduler(time.Unix(1, 0))
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[statusKey]())
	coalescer := newStatusCoalescer(queue, debounce, testStatusProgress, fakeClock)
	t.Cleanup(func() {
		coalescer.shutdown()
		queue.ShutDown()
	})
	return coalescer, queue, fakeClock
}

func startStatusWork(
	t *testing.T,
	coalescer *statusCoalescer,
	queue workqueue.TypedRateLimitingInterface[statusKey],
) statusKey {
	t.Helper()
	key, shutdown := queue.Get()
	require.False(t, shutdown)
	coalescer.start(key)
	return key
}

func finishStatusWork(
	coalescer *statusCoalescer,
	queue workqueue.TypedRateLimitingInterface[statusKey],
	key statusKey,
	success bool,
) {
	coalescer.finish(key, success)
	if success {
		queue.Forget(key)
	}
	queue.Done(key)
}

func testInventoryStatusKey() statusKey {
	return statusKey{kind: statusInventory, name: "inventory", uid: "inventory-uid"}
}

type blockingStatusQueue struct {
	workqueue.TypedInterface[statusKey]
	addStarted chan struct{}
	releaseAdd chan struct{}
	once       sync.Once
	mu         sync.Mutex
	adds       int
}

func newBlockingStatusQueue() *blockingStatusQueue {
	return &blockingStatusQueue{
		TypedInterface: workqueue.NewTyped[statusKey](),
		addStarted:     make(chan struct{}),
		releaseAdd:     make(chan struct{}),
	}
}

func (q *blockingStatusQueue) Add(key statusKey) {
	q.once.Do(func() { close(q.addStarted) })
	<-q.releaseAdd
	q.mu.Lock()
	q.adds++
	q.mu.Unlock()
	q.TypedInterface.Add(key)
}

func (q *blockingStatusQueue) addCalls() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.adds
}

type fakeStatusScheduler struct {
	mu     sync.Mutex
	now    time.Time
	nextID int
	timers map[int]fakeStatusTimerEntry
}

type fakeStatusTimerEntry struct {
	at time.Time
	fn func()
}

type fakeStatusTimer struct {
	scheduler *fakeStatusScheduler
	id        int
}

func newFakeStatusScheduler(now time.Time) *fakeStatusScheduler {
	return &fakeStatusScheduler{now: now, timers: make(map[int]fakeStatusTimerEntry)}
}

func (f *fakeStatusScheduler) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeStatusScheduler) AfterFunc(delay time.Duration, fn func()) statusTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.timers[f.nextID] = fakeStatusTimerEntry{at: f.now.Add(delay), fn: fn}
	return &fakeStatusTimer{scheduler: f, id: f.nextID}
}

func (f *fakeStatusScheduler) Step(duration time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(duration)
	due := make([]int, 0, len(f.timers))
	for id, timer := range f.timers {
		if !timer.at.After(f.now) {
			due = append(due, id)
		}
	}
	sort.Ints(due)
	callbacks := make([]func(), 0, len(due))
	for _, id := range due {
		callbacks = append(callbacks, f.timers[id].fn)
		delete(f.timers, id)
	}
	f.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

func (f *fakeStatusScheduler) Waiters() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

func (t *fakeStatusTimer) Stop() bool {
	t.scheduler.mu.Lock()
	defer t.scheduler.mu.Unlock()
	if _, exists := t.scheduler.timers[t.id]; !exists {
		return false
	}
	delete(t.scheduler.timers, t.id)
	return true
}

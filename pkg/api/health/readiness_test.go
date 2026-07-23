package health

import (
	"sync"
	"testing"
)

func TestTrackerDefaultsFailClosed(t *testing.T) {
	t.Parallel()

	var zero Tracker
	if got := zero.Snapshot(); got != (Readiness{Reason: ReasonUnavailable}) {
		t.Fatalf("zero Tracker snapshot = %+v", got)
	}

	tracker := NewTracker()
	if got := tracker.Snapshot(); got != (Readiness{Reason: ReasonRuntimeUnavailable}) {
		t.Fatalf("NewTracker() snapshot = %+v", got)
	}
}

func TestTrackerStoresOnlyBoundedStates(t *testing.T) {
	t.Parallel()

	tracker := NewTracker()
	tests := []struct {
		readiness Readiness
		want      Readiness
	}{
		{readiness: Readiness{Ready: true, Reason: ReasonFailed}, want: Readiness{Ready: true}},
		{readiness: Readiness{Reason: ReasonRecovering}, want: Readiness{Reason: ReasonRecovering}},
		{readiness: Readiness{Reason: ReasonDraining}, want: Readiness{Reason: ReasonDraining}},
		{readiness: Readiness{Reason: ReasonFailed}, want: Readiness{Reason: ReasonFailed}},
		{readiness: Readiness{Reason: Reason("provider-secret")}, want: Readiness{Reason: ReasonUnavailable}},
	}
	for _, test := range tests {
		tracker.Store(test.readiness)
		if got := tracker.Snapshot(); got != test.want {
			t.Errorf("Snapshot() = %+v, want %+v", got, test.want)
		}
	}
}

func TestTrackerIsRaceSafe(t *testing.T) {
	t.Parallel()

	tracker := NewTracker()
	var group sync.WaitGroup
	for index := 0; index < 64; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			if index%2 == 0 {
				tracker.Store(Readiness{Ready: true})
			} else {
				tracker.Store(Readiness{Reason: ReasonRecovering})
			}
			_ = tracker.Snapshot()
		}(index)
	}
	group.Wait()
}

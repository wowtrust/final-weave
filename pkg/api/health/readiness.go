// Package health defines framework-neutral operational health state.
//
// Readiness is local runtime state, not consensus finality or protocol evidence.
package health

import "sync/atomic"

// Reason is a stable, non-sensitive explanation for a non-ready process.
type Reason string

const (
	ReasonUnavailable        Reason = "unavailable"
	ReasonRuntimeUnavailable Reason = "runtime_unavailable"
	ReasonRecovering         Reason = "recovering"
	ReasonDraining           Reason = "draining"
	ReasonFailed             Reason = "failed"
)

// Readiness is a point-in-time process readiness result. Ready must only become
// true after the runtime owner has completed every required recovery, identity,
// capacity, storage, and per-ledger gate.
type Readiness struct {
	Ready  bool
	Reason Reason
}

// Tracker is a lock-free readiness projection shared by transport adapters.
// Request paths only load its atomic snapshot and therefore never call runtime
// I/O, acquire runtime locks, or wait for recovery work.
//
// The zero value is usable and represents a non-ready unavailable process.
// A Tracker must not be copied after first use.
type Tracker struct {
	state atomic.Uint32
}

const (
	stateUnavailable uint32 = iota
	stateRuntimeUnavailable
	stateRecovering
	stateDraining
	stateFailed
	stateReady
)

// NewTracker returns a non-ready tracker whose reason is runtime_unavailable.
func NewTracker() *Tracker {
	tracker := &Tracker{}
	tracker.state.Store(stateRuntimeUnavailable)
	return tracker
}

// Store atomically replaces the current readiness projection. Unknown reasons
// are reduced to unavailable so arbitrary local error text cannot cross an API.
func (t *Tracker) Store(readiness Readiness) {
	if t == nil {
		panic("health: Tracker must not be nil")
	}
	t.state.Store(encode(readiness))
}

// Snapshot returns the current readiness without blocking or allocation.
func (t *Tracker) Snapshot() Readiness {
	if t == nil {
		panic("health: Tracker must not be nil")
	}
	return decode(t.state.Load())
}

func encode(readiness Readiness) uint32 {
	if readiness.Ready {
		return stateReady
	}
	switch readiness.Reason {
	case ReasonRuntimeUnavailable:
		return stateRuntimeUnavailable
	case ReasonRecovering:
		return stateRecovering
	case ReasonDraining:
		return stateDraining
	case ReasonFailed:
		return stateFailed
	default:
		return stateUnavailable
	}
}

func decode(state uint32) Readiness {
	switch state {
	case stateRuntimeUnavailable:
		return Readiness{Reason: ReasonRuntimeUnavailable}
	case stateRecovering:
		return Readiness{Reason: ReasonRecovering}
	case stateDraining:
		return Readiness{Reason: ReasonDraining}
	case stateFailed:
		return Readiness{Reason: ReasonFailed}
	case stateReady:
		return Readiness{Ready: true}
	default:
		return Readiness{Reason: ReasonUnavailable}
	}
}

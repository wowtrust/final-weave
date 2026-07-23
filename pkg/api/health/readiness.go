// Package health defines framework-neutral operational health contracts.
//
// Readiness is local runtime state, not consensus finality or protocol evidence.
package health

import "context"

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

// Provider supplies current process readiness to transport adapters.
type Provider interface {
	Readiness(context.Context) Readiness
}

// ProviderFunc adapts a function to Provider.
type ProviderFunc func(context.Context) Readiness

// Readiness calls f.
func (f ProviderFunc) Readiness(ctx context.Context) Readiness {
	return f(ctx)
}

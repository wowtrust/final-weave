// Package types contains small, reusable protocol value types and invariants.
// It must not depend on process configuration, transports, databases, or APIs.
package types

import (
	"errors"
	"fmt"
)

const (
	// MinValidatorCount is the smallest FinalWeave v1 validator set.
	MinValidatorCount = 4
	// MaxValidatorCount is the largest set supported by the v1 GF(2^8)
	// availability profile.
	MaxValidatorCount = 253
)

var (
	// ErrValidatorCountOutOfRange reports a validator count outside the v1
	// absolute bounds.
	ErrValidatorCountOutOfRange = errors.New("validator count is outside FinalWeave v1 bounds")
	// ErrValidatorCountNotThreeFPlusOne reports a validator count that cannot
	// be expressed exactly as n = 3f + 1.
	ErrValidatorCountNotThreeFPlusOne = errors.New("validator count is not exactly 3f+1")
)

// QuorumParameters is a validated, immutable FinalWeave v1 n/f/q/k tuple.
// Its zero value is invalid. Every accessor panics on an invalid value instead
// of returning fail-open thresholds; construct values with
// NewQuorumParameters and always handle its error.
type QuorumParameters struct {
	validatorCount    int
	maxByzantine      int
	quorum            int
	recoveryThreshold int
}

// NewQuorumParameters derives the v1 thresholds for an exact validator count.
// It accepts int so callers can pass len(validators) without narrowing first.
func NewQuorumParameters(validatorCount int) (QuorumParameters, error) {
	if validatorCount < MinValidatorCount || validatorCount > MaxValidatorCount {
		return QuorumParameters{}, fmt.Errorf(
			"%w: got %d, want %d..%d",
			ErrValidatorCountOutOfRange,
			validatorCount,
			MinValidatorCount,
			MaxValidatorCount,
		)
	}
	if (validatorCount-1)%3 != 0 {
		return QuorumParameters{}, fmt.Errorf(
			"%w: got %d",
			ErrValidatorCountNotThreeFPlusOne,
			validatorCount,
		)
	}

	maxByzantine := (validatorCount - 1) / 3
	return QuorumParameters{
		validatorCount:    validatorCount,
		maxByzantine:      maxByzantine,
		quorum:            2*maxByzantine + 1,
		recoveryThreshold: maxByzantine + 1,
	}, nil
}

// ValidatorCount returns n.
func (p QuorumParameters) ValidatorCount() int {
	p.mustBeValid()
	return p.validatorCount
}

// MaxByzantine returns f.
func (p QuorumParameters) MaxByzantine() int {
	p.mustBeValid()
	return p.maxByzantine
}

// Quorum returns q, the number of distinct valid members required by a v1
// quorum certificate.
func (p QuorumParameters) Quorum() int {
	p.mustBeValid()
	return p.quorum
}

// RecoveryThreshold returns k, the number of valid fragments required to
// reconstruct a v1 batch body.
func (p QuorumParameters) RecoveryThreshold() int {
	p.mustBeValid()
	return p.recoveryThreshold
}

// MinimumQuorumIntersection returns the lower bound for the intersection of
// any two quorums in the same validator set.
func (p QuorumParameters) MinimumQuorumIntersection() int {
	p.mustBeValid()
	return 2*p.quorum - p.validatorCount
}

func (p QuorumParameters) mustBeValid() {
	if p.validatorCount < MinValidatorCount ||
		p.validatorCount > MaxValidatorCount ||
		(p.validatorCount-1)%3 != 0 ||
		p.maxByzantine != (p.validatorCount-1)/3 ||
		p.quorum != 2*p.maxByzantine+1 ||
		p.recoveryThreshold != p.maxByzantine+1 {
		panic("types: invalid QuorumParameters; use NewQuorumParameters and handle its error")
	}
}

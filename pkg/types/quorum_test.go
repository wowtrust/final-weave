package types

import (
	"errors"
	"testing"
)

func TestNewQuorumParameters(t *testing.T) {
	tests := []struct {
		name string
		n    int
		f    int
		q    int
		k    int
	}{
		{name: "minimum", n: 4, f: 1, q: 3, k: 2},
		{name: "seven", n: 7, f: 2, q: 5, k: 3},
		{name: "ten", n: 10, f: 3, q: 7, k: 4},
		{name: "maximum", n: 253, f: 84, q: 169, k: 85},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewQuorumParameters(tt.n)
			if err != nil {
				t.Fatalf("NewQuorumParameters(%d) error = %v", tt.n, err)
			}
			if got.ValidatorCount() != tt.n || got.MaxByzantine() != tt.f ||
				got.Quorum() != tt.q || got.RecoveryThreshold() != tt.k {
				t.Fatalf(
					"NewQuorumParameters(%d) = n/f/q/k %d/%d/%d/%d, want %d/%d/%d/%d",
					tt.n,
					got.ValidatorCount(),
					got.MaxByzantine(),
					got.Quorum(),
					got.RecoveryThreshold(),
					tt.n,
					tt.f,
					tt.q,
					tt.k,
				)
			}
			if got.MinimumQuorumIntersection() != tt.k {
				t.Fatalf("minimum quorum intersection = %d, want %d", got.MinimumQuorumIntersection(), tt.k)
			}
		})
	}
}

func TestNewQuorumParametersRejectsInvalidCounts(t *testing.T) {
	tests := []struct {
		n       int
		wantErr error
	}{
		{n: -1, wantErr: ErrValidatorCountOutOfRange},
		{n: 0, wantErr: ErrValidatorCountOutOfRange},
		{n: 3, wantErr: ErrValidatorCountOutOfRange},
		{n: 5, wantErr: ErrValidatorCountNotThreeFPlusOne},
		{n: 252, wantErr: ErrValidatorCountNotThreeFPlusOne},
		{n: 254, wantErr: ErrValidatorCountOutOfRange},
		{n: 65_540, wantErr: ErrValidatorCountOutOfRange},
		{n: int(^uint(0) >> 1), wantErr: ErrValidatorCountOutOfRange},
	}

	for _, tt := range tests {
		_, err := NewQuorumParameters(tt.n)
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("NewQuorumParameters(%d) error = %v, want errors.Is(%v)", tt.n, err, tt.wantErr)
		}
	}
}

func TestQuorumParametersExhaustiveV1Domain(t *testing.T) {
	for n := -1; n <= MaxValidatorCount+1; n++ {
		params, err := NewQuorumParameters(n)
		valid := n >= MinValidatorCount && n <= MaxValidatorCount && (n-1)%3 == 0
		if valid != (err == nil) {
			t.Fatalf("n=%d validity mismatch: error=%v", n, err)
		}
		if !valid {
			continue
		}
		if params.ValidatorCount() != 3*params.MaxByzantine()+1 {
			t.Fatalf("n=%d does not satisfy n=3f+1", n)
		}
		if params.Quorum() != 2*params.MaxByzantine()+1 {
			t.Fatalf("n=%d does not satisfy q=2f+1", n)
		}
		if params.RecoveryThreshold() != params.MaxByzantine()+1 {
			t.Fatalf("n=%d does not satisfy k=f+1", n)
		}
		if params.MinimumQuorumIntersection() != params.RecoveryThreshold() {
			t.Fatalf("n=%d quorum intersection is below k", n)
		}
	}
}

func FuzzNewQuorumParameters(f *testing.F) {
	for _, n := range []int{-1, 0, 3, 4, 5, 7, 10, 253, 254, 65_540, int(^uint(0) >> 1)} {
		f.Add(n)
	}

	f.Fuzz(func(t *testing.T, n int) {
		params, err := NewQuorumParameters(n)
		valid := n >= MinValidatorCount && n <= MaxValidatorCount && (n-1)%3 == 0
		if !valid {
			if err == nil {
				t.Fatalf("NewQuorumParameters(%d) succeeded for an invalid v1 count", n)
			}
			return
		}
		if err != nil {
			t.Fatalf("NewQuorumParameters(%d) error = %v", n, err)
		}
		if params.ValidatorCount() != 3*params.MaxByzantine()+1 ||
			params.Quorum() != 2*params.MaxByzantine()+1 ||
			params.RecoveryThreshold() != params.MaxByzantine()+1 {
			t.Fatalf("NewQuorumParameters(%d) returned inconsistent parameters", n)
		}
	})
}

func TestQuorumParametersZeroValueFailsClosed(t *testing.T) {
	var zero QuorumParameters
	accessors := []struct {
		name string
		call func() int
	}{
		{name: "validator count", call: zero.ValidatorCount},
		{name: "maximum Byzantine", call: zero.MaxByzantine},
		{name: "quorum", call: zero.Quorum},
		{name: "recovery threshold", call: zero.RecoveryThreshold},
		{name: "minimum quorum intersection", call: zero.MinimumQuorumIntersection},
	}

	for _, accessor := range accessors {
		t.Run(accessor.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid zero value accessor did not panic")
				}
			}()
			_ = accessor.call()
		})
	}
}

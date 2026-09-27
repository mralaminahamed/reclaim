package main

import (
	"testing"

	"github.com/mralaminahamed/reclaim/internal/discover"
)

// The threshold may only be lowered. Lowering it holds more caches back, which
// narrows a run; raising it would let a default run reach a larger cache, and
// nothing in the environment is allowed to widen what a run deletes.
func TestHeavyThresholdCanOnlyBeLowered(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int64
	}{
		{"", discover.HeavyThreshold},
		{"1M", 1 << 20},
		{"10G", discover.HeavyThreshold},
		{"nonsense", discover.HeavyThreshold},
		{"0", discover.HeavyThreshold},
	} {
		got := heavyThreshold(func(string) string { return c.env })
		if got != c.want {
			t.Errorf("RECLAIM_HEAVY_THRESHOLD=%q: got %d, want %d", c.env, got, c.want)
		}
	}
}

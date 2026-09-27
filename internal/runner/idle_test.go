package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// An idle entry is chosen at planning time. Something that starts using it
// before the deletion reaches it has made it not idle, and it stays.
func TestIdleUnitLeavesAnEntryUsedSincePlanning(t *testing.T) {
	dir := t.TempDir()
	idle, busy := filepath.Join(dir, "idle"), filepath.Join(dir, "busy")
	for _, p := range []string{idle, busy} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(busy, future, future); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	u := &unit.Unit{ID: "tmp", Kind: unit.KindPaths, Paths: []string{idle, busy},
		IdleFor: 10 * time.Millisecond, Reversible: true}

	res := (&Runner{Apply: true}).Run([]*unit.Unit{u})

	if exists(idle) {
		t.Error("idle entry survived")
	}
	if !exists(busy) {
		t.Fatal("entry used since planning was deleted")
	}
	if len(res[0].Skipped) != 1 || res[0].Skipped[0].Path != busy {
		t.Errorf("skipped %v, want the busy entry reported", res[0].Skipped)
	}
	if res[0].Err != nil {
		t.Errorf("err %v: a skip is not a failure", res[0].Err)
	}
}

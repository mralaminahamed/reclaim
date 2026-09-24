//go:build linux

package apps

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// On a noatime mount an old access time says nothing, so there is no answer.
func TestAtimeIsNoAnswerOnANoatimeMount(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(f, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := atime(f, []mount{{path: "/", atime: false}}); ok {
		t.Error("answered on a noatime mount")
	}
	if _, ok := atime(f, []mount{{path: "/", atime: true}}); !ok {
		t.Error("no answer on a mount that records atime")
	}
}

// The most specific mount decides.
func TestAtimeUsesTheInnermostMount(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bin")
	if err := os.WriteFile(f, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	ms := []mount{{path: "/", atime: true}, {path: real, atime: false}}
	if _, ok := atime(f, ms); ok {
		t.Error("outer relatime mount overrode inner noatime one")
	}
}

func TestShellSeenReadsGnomeState(t *testing.T) {
	p := filepath.Join(t.TempDir(), "application_state")
	body := `<application-state><context id="">
    <application id="org.gnome.Geary.desktop" score="20" last-seen="1790228378"/>
  </context></application-state>`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := shellSeen(p)
	if !got["org.gnome.Geary.desktop"].Equal(time.Unix(1790228378, 0)) {
		t.Errorf("got %v", got)
	}
}

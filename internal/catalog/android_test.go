package catalog

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// An AVD is an emulated phone, not a cache: wiping one is the same claim
// coresimulator-devices makes for the iOS simulator, so --android-avd stays
// its own flag, lossy, and never reached by escalation alone.
func TestAndroidAVDsAreLossyAndNamedByDevice(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".android/avd/Pixel_8.avd"))
	mkdir(t, filepath.Join(home, ".android/avd/Tablet.avd"))

	r := Build(Env{Home: home, Has: func(string) bool { return false }})
	u, ok := r.Get("android-avd")
	if !ok {
		t.Fatal("android-avd not registered though AVDs exist")
	}
	if u.Flag != "--android-avd" || u.Reversible || u.Tier != unit.TierLossy {
		t.Errorf("flag %q reversible %v tier %v", u.Flag, u.Reversible, u.Tier)
	}
	if len(u.Paths) != 2 {
		t.Fatalf("paths = %v, want 2 AVDs", u.Paths)
	}
	joined := strings.Join(u.Detail, " ")
	if !strings.Contains(joined, "Pixel_8") || !strings.Contains(joined, "Tablet") {
		t.Errorf("detail %v does not name both devices", u.Detail)
	}
}

func TestAndroidAVDsAbsentWithoutAnyDevice(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".android"))
	r := Build(Env{Home: home, Has: func(string) bool { return false }})
	if _, ok := r.Get("android-avd"); ok {
		t.Error("registered android-avd with no .avd directories present")
	}
}

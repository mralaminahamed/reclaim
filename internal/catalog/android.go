package catalog

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// androidAVDs registers each Android Virtual Device under ~/.android/avd.
//
// An AVD directory is not a cache: it is an emulated phone, holding its own
// disk image, installed apps and user data. Wiping one is the same claim
// coresimulator-devices makes for the iOS simulator, so it gets its own flag
// rather than reusing --simulators -- a person clearing one platform's devices
// has said nothing about the other.
func (b *builder) androidAVDs() {
	root := filepath.Join(b.env.Home, ".android", "avd")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var paths, detail []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), ".avd") {
			continue
		}
		paths = append(paths, filepath.Join(root, e.Name()))
		detail = append(detail, strings.TrimSuffix(e.Name(), ".avd"))
	}
	if len(paths) == 0 {
		return
	}
	b.r.Add(&unit.Unit{ID: "android-avd", Tier: unit.TierLossy, Reversible: false,
		Label: "Android emulator devices", Kind: unit.KindPaths, Paths: paths, Detail: detail,
		Flag: "--android-avd", MountHint: b.env.Home})
}

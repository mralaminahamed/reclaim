package catalog

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

func build(t *testing.T, home string, dirs ...string) *unit.Registry {
	t.Helper()
	for _, d := range dirs {
		mkdir(t, filepath.Join(home, d))
	}
	return Build(Env{Home: home, Has: func(string) bool { return false }})
}

// Chrome downloads a multi-gigabyte on-device model into its profile
// directory, not its cache. It comes back on demand, over the network: a model
// store, and gated like the others.
func TestChromeOnDeviceModelIsAModelStore(t *testing.T) {
	r := build(t, t.TempDir(), ".config/google-chrome/OptGuideOnDeviceModel")
	u, ok := r.Get("chrome-ai-model")
	if !ok {
		t.Fatal("chrome-ai-model not registered")
	}
	if u.Flag != "--models" || u.Tier != unit.TierColdReload || !u.Reversible {
		t.Errorf("flag %q tier %v reversible %v", u.Flag, u.Tier, u.Reversible)
	}
}

func TestWPCLIAndYarnMetadataAreCaches(t *testing.T) {
	home := t.TempDir()
	r := build(t, home, ".wp-cli/cache", ".wp-cli/packages", ".yarn/berry/metadata")
	u, ok := r.Get("wp-cli-cache")
	if !ok || u.Tier != unit.TierPkgCache || u.Flag != "" {
		t.Fatalf("wp-cli cache: %+v", u)
	}
	for _, p := range u.Paths {
		if strings.Contains(p, "packages") {
			t.Errorf("wp-cli packages are installed commands, not cache: %s", p)
		}
	}
	y, ok := r.Get("yarn-berry")
	if !ok || !strings.Contains(strings.Join(y.Paths, " "), ".yarn/berry/metadata") {
		t.Errorf("yarn berry metadata not claimed: %+v", y)
	}
}

// Every JetBrains upgrade leaves the previous version's directories behind.
// Only a version with a newer one of the same product beside it is obsolete;
// the newest of each product is what the IDE is using.
func TestJetBrainsOldVersionsAreObsolete(t *testing.T) {
	home := t.TempDir()
	r := build(t, home,
		".config/JetBrains/RustRover2025.2", ".config/JetBrains/RustRover2025.3",
		".config/JetBrains/WebStorm2025.3", ".config/JetBrains/analyzer",
		".cache/JetBrains/PhpStorm2025.1", ".cache/JetBrains/PhpStorm2026.2",
		".local/share/JetBrains/PhpStorm2025.1", ".local/share/JetBrains/PhpStorm2026.2")

	data, ok := r.Get("jetbrains-old-data")
	if !ok {
		t.Fatal("jetbrains-old-data not registered")
	}
	got := strings.Join(data.Paths, " ")
	if !strings.Contains(got, ".local/share/JetBrains/PhpStorm2025.1") {
		t.Errorf("old data misses PhpStorm2025.1: %s", got)
	}
	// .cache/JetBrains belongs whole to jetbrains-cache; claiming part of it
	// again would count those bytes twice.
	if strings.Contains(got, ".cache/") {
		t.Errorf("old data reaches into the cache jetbrains-cache owns: %s", got)
	}
	if strings.Contains(got, "2026.2") {
		t.Errorf("takes the current version: %s", got)
	}
	if !data.Reversible || data.Flag != "--obsolete" {
		t.Errorf("old data reversible %v flag %q", data.Reversible, data.Flag)
	}

	conf, ok := r.Get("jetbrains-old-config")
	if !ok {
		t.Fatal("jetbrains-old-config not registered")
	}
	if strings.Join(conf.Paths, " ") != filepath.Join(home, ".config/JetBrains/RustRover2025.2") {
		t.Errorf("old config %v, want only RustRover2025.2", conf.Paths)
	}
	// Settings: an upgrade imports them, but only once and only if asked.
	if conf.Reversible {
		t.Error("old settings are the only copy of what was not imported; must be lossy")
	}
}

// Versions compare numerically: 2025.10 is newer than 2025.9.
func TestJetBrainsVersionsCompareNumerically(t *testing.T) {
	home := t.TempDir()
	r := build(t, home, ".local/share/JetBrains/GoLand2025.9", ".local/share/JetBrains/GoLand2025.10")
	u, ok := r.Get("jetbrains-old-data")
	if !ok || len(u.Paths) != 1 || !strings.HasSuffix(u.Paths[0], "GoLand2025.9") {
		t.Errorf("want only GoLand2025.9, got %+v", u)
	}
}

func TestJetBrainsSingleVersionIsNotObsolete(t *testing.T) {
	r := build(t, t.TempDir(), ".local/share/JetBrains/PhpStorm2026.2", ".config/JetBrains/PhpStorm2026.2")
	if _, ok := r.Get("jetbrains-old-data"); ok {
		t.Error("the only version is not obsolete")
	}
	if _, ok := r.Get("jetbrains-old-config"); ok {
		t.Error("the only version is not obsolete")
	}
}

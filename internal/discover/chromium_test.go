package discover

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ids(r *unit.Registry) map[string]*unit.Unit {
	out := map[string]*unit.Unit{}
	for _, u := range r.All() {
		out[u.ID] = u
	}
	return out
}

func TestNewerChromiumCacheNamesAreCaches(t *testing.T) {
	for _, n := range []string{"GraphiteDawnCache", "DawnWebGPUCache", "extensions_crx_cache", "CachedExtensionVSIXs", "CachedProfilesData"} {
		if !IsCacheName(n) {
			t.Errorf("%s is a Chromium/VS Code cache and should be claimable", n)
		}
	}
}

// Postman keeps each workspace's browser state under Partitions/<id>/, and a
// browser keeps GPU and code caches per profile. Both are too deep for the
// one-level rule, and both belong to a Chromium user-data directory, which
// "Local State" identifies.
func TestChromiumProfilesAndPartitionsAreSwept(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Postman")
	touch(t, filepath.Join(app, "Local State"))
	touch(t, filepath.Join(app, "Partitions", "0a06", "Cache", "Cache_Data", "blob"))
	touch(t, filepath.Join(app, "Partitions", "0a06", "Local Storage", "leveldb", "000003.log"))
	brave := filepath.Join(root, "BraveSoftware", "Brave-Browser")
	touch(t, filepath.Join(brave, "Local State"))
	touch(t, filepath.Join(brave, "Default", "Preferences"))
	touch(t, filepath.Join(brave, "Default", "GPUCache", "data_0"))
	touch(t, filepath.Join(brave, "Default", "Cookies"))

	r := unit.NewRegistry()
	ChromiumCaches(r, []string{root})

	got := ids(r)
	for _, want := range []string{"disc-Postman-Partitions-0a06-Cache", "disc-Brave-Browser-Default-GPUCache"} {
		if got[want] == nil {
			t.Errorf("%s not claimed; got %v", want, keys(got))
		}
	}
	for id, u := range got {
		for _, p := range u.Paths {
			if filepath.Base(p) == "Local Storage" || filepath.Base(p) == "Cookies" {
				t.Errorf("%s claims protected state %s", id, p)
			}
		}
	}
}

// Without "Local State" the directory is not a Chromium profile, and a
// deeper directory named "Cache" could be anybody's data.
func TestDeepCachesOutsideChromiumAreLeftAlone(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "tool", "projects", "p1", "Cache", "blob"))

	r := unit.NewRegistry()
	ChromiumCaches(r, []string{root})

	if len(r.All()) != 0 {
		t.Errorf("claimed outside a Chromium profile: %v", keys(ids(r)))
	}
}

// Service Worker CacheStorage is offline data for web apps: deleting it can
// log the user out or make them re-download everything. Offer it, lossy and
// behind its own flag, never as a cache.
func TestServiceWorkerStorageIsLossyAndOptIn(t *testing.T) {
	root := t.TempDir()
	slack := filepath.Join(root, "Slack")
	touch(t, filepath.Join(slack, "Local State"))
	touch(t, filepath.Join(slack, "Service Worker", "CacheStorage", "abc", "blob"))

	r := unit.NewRegistry()
	SiteData(r, []string{root})

	u := ids(r)["site-Slack-CacheStorage"]
	if u == nil {
		t.Fatalf("CacheStorage not offered; got %v", keys(ids(r)))
	}
	if u.Reversible || u.Tier != unit.TierLossy || u.Flag != "--site-data" {
		t.Errorf("tier=%v reversible=%v flag=%q; want lossy, irreversible, --site-data", u.Tier, u.Reversible, u.Flag)
	}
}

func keys(m map[string]*unit.Unit) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Electron apps such as Postman write no "Local State", but the pair of
// Chromium's own "Preferences" and "Network Persistent State" marks the same
// directory shape.
func TestElectronDataDirWithoutLocalStateIsRecognised(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Postman")
	touch(t, filepath.Join(app, "Preferences"))
	touch(t, filepath.Join(app, "Network Persistent State"))
	touch(t, filepath.Join(app, "Partitions", "0a06", "GPUCache", "data_0"))

	r := unit.NewRegistry()
	ChromiumCaches(r, []string{root})

	if ids(r)["disc-Postman-Partitions-0a06-GPUCache"] == nil {
		t.Errorf("Postman partition cache not claimed; got %v", keys(ids(r)))
	}
}

// "Preferences" alone is an ordinary file name and proves nothing.
func TestPreferencesAloneIsNotChromium(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "tool", "Preferences"))
	touch(t, filepath.Join(root, "tool", "Partitions", "x", "Cache", "blob"))

	r := unit.NewRegistry()
	ChromiumCaches(r, []string{root})

	if len(r.All()) != 0 {
		t.Errorf("claimed without a Chromium marker: %v", keys(ids(r)))
	}
}

package discover

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// chromiumDepth bounds the search for Chromium user-data directories below
// each root: ~/.config/BraveSoftware/Brave-Browser is two levels down.
const chromiumDepth = 3

// SiteDataFlag opts in to web apps' offline storage.
const SiteDataFlag = "--site-data"

// ChromiumCaches sweeps Chromium and Electron user-data directories more
// deeply than NestedCaches can afford to sweep arbitrary ones.
//
// A user-data directory is recognised by its "Local State" file, which
// Chromium writes and nothing else does. Inside one the layout is known:
// caches sit in the directory itself, in each profile (a subdirectory holding
// "Preferences"), and in each Partitions/<id>/ -- Postman keeps a whole
// browser per workspace there. Only cache names are claimed; the protected
// names stay protected.
//
// These are discovered units, so like NestedCaches this runs only under
// --discover.
func ChromiumCaches(r *unit.Registry, roots []string) {
	eachContainer(roots, func(app, rel, c string) { claimCaches(r, app, rel, c) })
}

// SiteData offers each Chromium user-data directory's Service Worker
// CacheStorage, lossy and behind --site-data. It is web apps' offline data,
// not a cache in the sense of the rest: deleting it can log the user out or
// make an app re-download everything it stored for offline use. Registering
// it is harmless -- nothing runs without the flag -- so it does not wait for
// --discover.
func SiteData(r *unit.Registry, roots []string) {
	eachContainer(roots, func(app, rel, c string) { offerSiteData(r, app, rel, c) })
}

func eachContainer(roots []string, fn func(app, rel, container string)) {
	for _, root := range roots {
		for _, ud := range userDataDirs(root, 0) {
			app := filepath.Base(ud)
			for _, c := range containers(ud) {
				rel, _ := filepath.Rel(ud, c)
				fn(app, rel, c)
			}
		}
	}
}

// userDataDirs finds directories holding "Local State" up to chromiumDepth
// below dir. It does not look inside one it has found.
func userDataDirs(dir string, depth int) []string {
	if depth >= chromiumDepth {
		return nil
	}
	var out []string
	for _, d := range subdirs(dir) {
		if isUserDataDir(d) {
			out = append(out, d)
			continue
		}
		out = append(out, userDataDirs(d, depth+1)...)
	}
	return out
}

// isUserDataDir recognises a Chromium user-data directory: "Local State", or
// for Electron apps that write none, Chromium's "Preferences" together with
// its "Network Persistent State". Either name alone is too ordinary to go on.
func isUserDataDir(d string) bool {
	return isFile(filepath.Join(d, "Local State")) ||
		(isFile(filepath.Join(d, "Preferences")) && isFile(filepath.Join(d, "Network Persistent State")))
}

// containers are the directories inside a user-data directory that hold
// caches: itself, its profiles and its partitions.
func containers(ud string) []string {
	out := []string{ud}
	for _, d := range subdirs(ud) {
		if isFile(filepath.Join(d, "Preferences")) {
			out = append(out, d)
		}
	}
	out = append(out, subdirs(filepath.Join(ud, "Partitions"))...)
	return out
}

func claimCaches(r *unit.Registry, app, rel, container string) {
	for _, d := range subdirs(container) {
		name := filepath.Base(d)
		if !IsCacheName(name) || r.Claimed(d) {
			continue
		}
		r.Add(&unit.Unit{
			ID:         unitID("disc", app, rel, name),
			Tier:       unit.TierArtifact,
			Reversible: true,
			Discovered: true,
			Label:      filepath.Join(app, rel, name),
			Kind:       unit.KindPaths,
			Paths:      []string{d},
		})
	}
}

func offerSiteData(r *unit.Registry, app, rel, container string) {
	d := filepath.Join(container, "Service Worker", "CacheStorage")
	if !isDir(d) || r.Claimed(d) {
		return
	}
	r.Add(&unit.Unit{
		ID:         unitID("site", app, rel, "CacheStorage"),
		Tier:       unit.TierLossy,
		Reversible: false,
		Label:      filepath.Join(app, rel) + " offline site data",
		Kind:       unit.KindPaths,
		Paths:      []string{d},
		Flag:       SiteDataFlag,
	})
}

// unitID joins the parts of an id, leaving out an empty or "." relative path
// so that a cache in the user-data directory itself gets the same id
// NestedCaches would give it.
func unitID(prefix, app, rel, name string) string {
	parts := []string{prefix, app}
	if rel != "" && rel != "." {
		parts = append(parts, strings.ReplaceAll(rel, string(filepath.Separator), "-"))
	}
	parts = append(parts, name)
	return strings.ReplaceAll(strings.Join(parts, "-"), " ", "-")
}

func isFile(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}

func isDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

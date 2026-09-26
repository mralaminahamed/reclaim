package catalog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// steamRoots lists every place a Steam library lives across install methods.
// Only Linux runs Proton, so unlike steam-shadercache this has no macOS entry.
func steamRoots(home string) []string {
	return []string{
		filepath.Join(home, ".local/share/Steam"),
		filepath.Join(home, ".steam/steam"),
		filepath.Join(home, ".var/app/com.valvesoftware.Steam/.local/share/Steam"),
	}
}

var acfName = regexp.MustCompile(`(?m)^\s*"name"\s+"([^"]*)"`)

// appManifestNames maps an appid to its game name, read from Steam's own
// per-app manifests. A compatdata folder is named by appid alone, and naming
// the game is what turns "delete this" into something a person can consent to.
func appManifestNames(steamapps string) map[string]string {
	out := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(steamapps, "appmanifest_*.acf"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		m := acfName.FindSubmatch(data)
		if m == nil {
			continue
		}
		appid := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "appmanifest_"), ".acf")
		out[appid] = string(m[1])
	}
	return out
}

// steamCompatData registers each game's Proton prefix under compatdata. Unlike
// shader cache, a prefix is a Wine-style filesystem that can hold a game's
// saves or settings nowhere else -- the same "may hold something real" claim
// docker-volumes makes -- so this is opt-in and lossy, never picked up by
// --discover or by escalating tiers alone.
func (b *builder) steamCompatData() {
	var paths, detail []string
	for _, root := range steamRoots(b.env.Home) {
		steamapps := filepath.Join(root, "steamapps")
		entries, err := os.ReadDir(filepath.Join(steamapps, "compatdata"))
		if err != nil {
			continue
		}
		names := appManifestNames(steamapps)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			paths = append(paths, filepath.Join(steamapps, "compatdata", e.Name()))
			if name, ok := names[e.Name()]; ok {
				detail = append(detail, name+" ("+e.Name()+")")
			} else {
				detail = append(detail, e.Name())
			}
		}
	}
	if len(paths) == 0 {
		return
	}
	b.r.Add(&unit.Unit{ID: "steam-compatdata", Tier: unit.TierIrreplaceable, Reversible: false,
		Label: "Steam Proton prefixes", Kind: unit.KindPaths, Paths: paths, Detail: detail,
		Flag: "--steam-compatdata", MountHint: b.env.Home})
}

package lock

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mralaminahamed/reclaim/internal/fsutil"
	"github.com/mralaminahamed/reclaim/internal/unit"
)

// FlatpakUnit is the catalog's unit holding every flatpak app's cache.
const FlatpakUnit = "flatpak-app-caches"

// FlatpakRunning returns the running flatpak apps by id, with a pid where one
// can be read.
//
// A sandboxed process's command line names a path inside the sandbox, so the
// process table cannot say which app it is. Flatpak writes a directory per
// running instance under $XDG_RUNTIME_DIR/.flatpak whose "info" file names
// the application, and whose bwrapinfo.json gives the sandbox's pid. An
// instance whose pid is dead is a leftover. One with no readable pid counts
// as running: parking a cache for nothing costs a re-run, and the other
// mistake costs a live app its cache. Directories without an application --
// WebKit's sandboxes, bare runtimes -- are not apps.
func FlatpakRunning(runtimeDir string, alive func(int) bool) map[string]int {
	out := map[string]int{}
	dirs, err := os.ReadDir(filepath.Join(runtimeDir, ".flatpak"))
	if err != nil {
		return out
	}
	for _, d := range dirs {
		dir := filepath.Join(runtimeDir, ".flatpak", d.Name())
		id := flatpakApp(filepath.Join(dir, "info"))
		if id == "" {
			continue
		}
		pid := flatpakPid(filepath.Join(dir, "bwrapinfo.json"))
		if pid > 0 && !alive(pid) {
			continue
		}
		if _, seen := out[id]; !seen || pid > 0 {
			out[id] = pid
		}
	}
	return out
}

// flatpakApp reads "name" from the [Application] group of an info keyfile.
func flatpakApp(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	group := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			group = line[1 : len(line)-1]
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && group == "Application" && strings.TrimSpace(k) == "name" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func flatpakPid(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var info struct {
		ChildPid int `json:"child-pid"`
	}
	if json.Unmarshal(data, &info) != nil {
		return 0
	}
	return info.ChildPid
}

// Flatpak parks the caches of running flatpak apps. Each running app's cache
// leaves the shared unit for a locked unit of its own, so it is reported
// with its app and pid, and the others stay cleanable -- one open browser
// must not park every flatpak's cache. Both units are measured again, since
// probing ran before anything was known to be running.
func Flatpak(r *unit.Registry, appRoot string, running map[string]int) {
	all, ok := r.Get(FlatpakUnit)
	if !ok || len(running) == 0 {
		return
	}
	var keep []string
	for _, p := range all.Paths {
		rel, err := filepath.Rel(appRoot, p)
		id, _, _ := strings.Cut(rel, string(filepath.Separator))
		pid, live := running[id]
		if err != nil || !live {
			keep = append(keep, p)
			continue
		}
		u := &unit.Unit{ID: "flatpak-cache-" + id, Tier: all.Tier, Reversible: all.Reversible,
			Label: id + " cache", Kind: unit.KindPaths, Paths: []string{p}, Flag: all.Flag,
			MountHint: all.MountHint, Mount: all.Mount, LockedBy: "flatpak " + id, PID: pid}
		measure(u)
		r.Add(u)
	}
	if len(keep) == len(all.Paths) {
		return
	}
	all.Paths = keep
	measure(all)
}

func measure(u *unit.Unit) {
	m := fsutil.Measure(u.Paths)
	u.Bytes, u.Shared, u.Apparent, u.Unreadable = m.Allocated, m.Shared, m.Apparent, m.Unreadable
}

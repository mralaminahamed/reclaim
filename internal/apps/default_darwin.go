//go:build darwin

package apps

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mralaminahamed/reclaim/internal/fsutil"
)

// Cask is an app Homebrew's Caskroom installed and can remove.
const Cask Source = "cask"

// DefaultEnv returns an Env with no application sources: Find walks .desktop
// launchers, and a macOS bundle is not one. FindIdle is this platform's real
// implementation.
func DefaultEnv(home string) Env {
	return Env{Home: home, Now: time.Now()}
}

// darwinEnv is Env's counterpart for bundles, with every probe injected for
// testing.
type darwinEnv struct {
	Home     string
	Now      time.Time
	Bundles  func(dir string) []string
	LastUsed func(bundle string) (time.Time, bool)
	CaskApps func() map[string]string
	DirBytes func(path string) int64
}

// FindIdle reports installed applications idle for at least idle.
func FindIdle(home string, idle time.Duration) Result {
	return findIdle(darwinEnv{
		Home:     home,
		Now:      time.Now(),
		Bundles:  bundlesIn,
		LastUsed: lastUsed,
		CaskApps: caskApps,
		DirBytes: func(p string) int64 { n, _ := fsutil.PathBytes(p); return n },
	}, idle)
}

func findIdle(env darwinEnv, idle time.Duration) Result {
	var res Result
	cutoff := env.Now.Add(-idle)

	// /System/Applications is never scanned: it holds the OS's own apps.
	dirs := []string{"/Applications"}
	if env.Home != "" {
		dirs = append(dirs, filepath.Join(env.Home, "Applications"))
	}

	casks := map[string]string{}
	if env.CaskApps != nil {
		casks = env.CaskApps()
	}

	for _, d := range dirs {
		for _, bundle := range env.Bundles(d) {
			last, ok := env.LastUsed(bundle)
			if !ok {
				res.Unknown++
				continue
			}
			if last.After(cutoff) {
				continue
			}
			app := App{Name: strings.TrimSuffix(filepath.Base(bundle), ".app"), LastUsed: last}
			if token, isCask := casks[filepath.Base(bundle)]; isCask {
				app.Source, app.Package = Cask, token
				app.Remove = "brew uninstall --cask " + token
			} else {
				app.Source, app.Package = Local, bundle
				app.Remove = "rm -rf " + bundle
			}
			if env.DirBytes != nil {
				app.Bytes = env.DirBytes(bundle)
			}
			res.Apps = append(res.Apps, app)
		}
	}
	sort.SliceStable(res.Apps, func(i, j int) bool { return res.Apps[i].Bytes > res.Apps[j].Bytes })
	return res
}

// bundlesIn lists the top-level application bundles directly inside dir.
func bundlesIn(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.app"))
	return matches
}

// lastUsed reads kMDItemLastUsedDate from the Spotlight index -- what Finder
// and Launchpad use to sort "recently used".
func lastUsed(bundle string) (time.Time, bool) {
	out, err := exec.Command("mdls", "-raw", "-name", "kMDItemLastUsedDate", bundle).Output()
	if err != nil {
		return time.Time{}, false
	}
	return parseMDLS(string(out))
}

// parseMDLS reads mdls's date output, treating its literal "(null)" as no
// answer rather than the zero time.
func parseMDLS(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "(null)" {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02 15:04:05 -0700", s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// caskInfo is the part of `brew info --json=v2` this package reads.
type caskInfo struct {
	Casks []struct {
		Token     string `json:"token"`
		Artifacts []struct {
			App []string `json:"app"`
		} `json:"artifacts"`
	} `json:"casks"`
}

// caskApps maps each cask-installed app's bundle filename to its token, from
// one "brew info" call rather than one per app.
func caskApps() map[string]string {
	out := map[string]string{}
	if _, err := exec.LookPath("brew"); err != nil {
		return out
	}
	data, err := exec.Command("brew", "info", "--json=v2", "--installed").Output()
	if err != nil {
		return out
	}
	var info caskInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return out
	}
	for _, c := range info.Casks {
		for _, a := range c.Artifacts {
			for _, app := range a.App {
				out[app] = c.Token
			}
		}
	}
	return out
}

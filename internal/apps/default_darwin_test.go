//go:build darwin

package apps

import (
	"strings"
	"testing"
	"time"
)

var dnow = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

const dday = 24 * time.Hour

func denv(bundles []string, lastUsed map[string]time.Duration, casks map[string]string) darwinEnv {
	return darwinEnv{
		Now: dnow,
		Bundles: func(string) []string {
			return bundles
		},
		LastUsed: func(b string) (time.Time, bool) {
			age, ok := lastUsed[b]
			return dnow.Add(-age), ok
		},
		CaskApps: func() map[string]string { return casks },
		DirBytes: func(string) int64 { return 100 },
	}
}

func dnames(as []App) string {
	var n []string
	for _, a := range as {
		n = append(n, a.Name)
	}
	return strings.Join(n, " ")
}

func TestFindIdleReportsOnlyBundlesIdlePastTheBound(t *testing.T) {
	env := denv(
		[]string{"/Applications/Old.app", "/Applications/New.app"},
		map[string]time.Duration{"/Applications/Old.app": 200 * dday, "/Applications/New.app": 2 * dday},
		nil,
	)
	got := findIdle(env, 90*dday).Apps
	if dnames(got) != "Old" {
		t.Fatalf("idle apps %q, want Old", dnames(got))
	}
	if got[0].Remove != "rm -rf /Applications/Old.app" {
		t.Errorf("remove %q", got[0].Remove)
	}
}

func TestFindIdleNeverReportsABundleWithoutEvidence(t *testing.T) {
	env := denv([]string{"/Applications/Mystery.app"}, map[string]time.Duration{}, nil)
	res := findIdle(env, 90*dday)
	if len(res.Apps) != 0 {
		t.Errorf("reported %q without evidence", dnames(res.Apps))
	}
	if res.Unknown != 1 {
		t.Errorf("unknown %d, want 1", res.Unknown)
	}
}

func TestFindIdleUsesCaskUninstallWhenBrewOwnsTheApp(t *testing.T) {
	env := denv(
		[]string{"/Applications/Context.app"},
		map[string]time.Duration{"/Applications/Context.app": 400 * dday},
		map[string]string{"Context.app": "context"},
	)
	got := findIdle(env, 90*dday).Apps
	if len(got) != 1 || got[0].Source != Cask || got[0].Remove != "brew uninstall --cask context" {
		t.Errorf("got %+v", got)
	}
}

func TestFindIdleSortsLargestFirst(t *testing.T) {
	env := denv(
		[]string{"/Applications/Small.app", "/Applications/Big.app"},
		map[string]time.Duration{"/Applications/Small.app": 400 * dday, "/Applications/Big.app": 400 * dday},
		nil,
	)
	env.DirBytes = func(p string) int64 {
		if p == "/Applications/Big.app" {
			return 1 << 30
		}
		return 1
	}
	if got := findIdle(env, 90*dday).Apps; dnames(got) != "Big Small" {
		t.Errorf("order %q", dnames(got))
	}
}

func TestFindIdleSearchesHomeApplicationsToo(t *testing.T) {
	env := darwinEnv{
		Now: dnow,
		Bundles: func(dir string) []string {
			if dir == "/home/Applications" {
				return []string{"/home/Applications/Mine.app"}
			}
			return nil
		},
		Home:     "/home",
		LastUsed: func(string) (time.Time, bool) { return dnow.Add(-400 * dday), true },
		CaskApps: func() map[string]string { return nil },
		DirBytes: func(string) int64 { return 1 },
	}
	if got := findIdle(env, 90*dday).Apps; dnames(got) != "Mine" {
		t.Errorf("got %q", dnames(got))
	}
}

func TestLastUsedTreatsNullAsNoEvidence(t *testing.T) {
	if _, ok := parseMDLS("(null)"); ok {
		t.Error("(null) answered as evidence")
	}
	if _, ok := parseMDLS(""); ok {
		t.Error("empty output answered as evidence")
	}
	got, ok := parseMDLS("2024-06-01 10:23:45 +0000")
	if !ok || !got.Equal(time.Date(2024, 6, 1, 10, 23, 45, 0, time.UTC)) {
		t.Errorf("got %v, %v", got, ok)
	}
}

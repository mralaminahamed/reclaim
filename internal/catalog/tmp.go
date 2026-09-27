package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mralaminahamed/reclaim/internal/fsutil"
	"github.com/mralaminahamed/reclaim/internal/runner"
	"github.com/mralaminahamed/reclaim/internal/unit"
)

// defaultTmpIdle is how long nothing may have touched a scratch entry before
// it is taken: a week, well inside systemd-tmpfiles' own 30 days, and long
// past anything a running job is still writing.
const defaultTmpIdle = 7 * 24 * time.Hour

// tmpRoots are the scratch directories swept: RECLAIM_TMP_ROOTS when set, a
// colon-separated list the test harness uses to keep runs off the real /tmp,
// otherwise /tmp, /var/tmp and $TMPDIR (macOS gives each user their own).
// Relative entries are ignored.
func tmpRoots(getenv func(string) string) []string {
	var cands []string
	if v := getenv("RECLAIM_TMP_ROOTS"); v != "" {
		cands = strings.Split(v, ":")
	} else {
		cands = []string{"/tmp", "/var/tmp", getenv("TMPDIR")}
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range cands {
		if !filepath.IsAbs(c) {
			continue
		}
		c = filepath.Clean(c)
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// tmpIdle is defaultTmpIdle, lowered by RECLAIM_TMP_IDLE and never raised:
// the variable exists so a test can make a fresh file idle, not so a typo can
// widen what counts as idle.
func tmpIdle(getenv func(string) string) time.Duration {
	if d, err := time.ParseDuration(getenv("RECLAIM_TMP_IDLE")); err == nil && d > 0 && d < defaultTmpIdle {
		return d
	}
	return defaultTmpIdle
}

// tmpCacheNames are compile caches tools keep in the temporary directory.
// Each regenerates on the next run.
func tmpCacheNames(uid int) []string {
	return []string{"node-compile-cache", "v8-compile-cache-" + strconv.Itoa(uid), "jest_rs", "phpstan"}
}

// tmp offers what the user left in scratch directories. Known caches go
// whatever their age. The user's own other entries go once nothing inside
// has been used for the idle period.
//
// Scratch is disposable by contract -- nothing may assume a file there
// outlives the program that wrote it, and the system expires it anyway -- so
// both units are reversible and run by default. Only the user's own entries
// are taken, never the directory itself, and the runner checks again that an
// idle entry is still idle when it gets to it.
func (b *builder) tmp() {
	uid := os.Geteuid()
	var caches []string
	for _, root := range b.env.TmpRoots {
		for _, name := range tmpCacheNames(uid) {
			p := filepath.Join(root, name)
			if exists(p) && fsutil.OwnedBy(p, uint32(uid)) && runner.CheckSafe(p, b.env.Home) == nil {
				caches = append(caches, p)
			}
		}
	}
	if len(caches) > 0 {
		b.r.Add(&unit.Unit{ID: "tmp-caches", Tier: unit.TierArtifact, Reversible: true,
			Label: "compile caches in the temporary directory", Kind: unit.KindPaths,
			Paths: caches, MountHint: b.env.TmpRoots[0]})
	}

	idle := b.env.TmpIdle
	if idle <= 0 {
		return
	}
	cutoff := time.Now().Add(-idle)
	var entries []string
	for _, root := range b.env.TmpRoots {
		for _, p := range fsutil.IdleEntries(root, cutoff, uint32(uid)) {
			if b.r.Claimed(p) || runner.CheckSafe(p, b.env.Home) != nil {
				continue
			}
			entries = append(entries, p)
		}
	}
	if len(entries) == 0 {
		return
	}
	b.r.Add(&unit.Unit{ID: "tmp-idle", Tier: unit.TierArtifact, Reversible: true,
		Label: "idle temporary files", Kind: unit.KindPaths, Paths: entries, IdleFor: idle,
		MountHint: b.env.TmpRoots[0],
		Detail: []string{fmt.Sprintf("%d of your entries in %s untouched for %s or more",
			len(entries), strings.Join(b.env.TmpRoots, ", "), humanDays(idle))}})
}

func humanDays(d time.Duration) string {
	if d >= 24*time.Hour {
		return strconv.Itoa(int(d/(24*time.Hour))) + " days"
	}
	return d.String()
}

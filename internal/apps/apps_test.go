package apps

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

func desktop(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func app(name, exec string) string {
	return "[Desktop Entry]\nType=Application\nName=" + name + "\nExec=" + exec + "\n"
}

// debEnv is a machine where every launcher belongs to a package named after
// its file, and bins maps each binary to when it was last read.
func debEnv(t *testing.T, bins map[string]time.Duration) (Env, string) {
	dir := t.TempDir()
	return Env{
		Home: t.TempDir(),
		Dirs: []Dir{{Path: dir, Source: Deb}},
		Now:  now,
		Owner: func(p string) string {
			return strings.TrimSuffix(filepath.Base(p), ".desktop")
		},
		Auto:     func(string) bool { return false },
		PkgBytes: func(Source, string) int64 { return 100 },
		Used: func(p string) (time.Time, bool) {
			age, ok := bins[p]
			return now.Add(-age), ok
		},
		LookPath: func(n string) (string, error) { return "/usr/bin/" + n, nil },
	}, dir
}

func names(as []App) string {
	var n []string
	for _, a := range as {
		n = append(n, a.Package)
	}
	return strings.Join(n, " ")
}

func TestFindReportsOnlyAppsIdlePastTheBound(t *testing.T) {
	env, dir := debEnv(t, map[string]time.Duration{
		"/usr/bin/filezilla": 200 * day,
		"/usr/bin/geary":     2 * day,
	})
	desktop(t, dir, "filezilla.desktop", app("FileZilla", "filezilla %U"))
	desktop(t, dir, "geary.desktop", app("Geary", "geary %U"))

	got := Find(env, 90*day).Apps
	if names(got) != "filezilla" {
		t.Fatalf("idle apps %q, want filezilla", names(got))
	}
	if got[0].Remove != "sudo apt remove filezilla" {
		t.Errorf("remove %q", got[0].Remove)
	}
}

// No evidence is not evidence of disuse. A binary on a noatime mount, or one
// that cannot be found, says nothing, and the app is counted as unknown rather
// than reported.
func TestFindNeverReportsAnAppWithoutEvidence(t *testing.T) {
	env, dir := debEnv(t, map[string]time.Duration{})
	desktop(t, dir, "mystery.desktop", app("Mystery", "mystery"))
	env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	desktop(t, dir, "gone.desktop", app("Gone", "gone"))

	res := Find(env, 90*day)
	if len(res.Apps) != 0 {
		t.Errorf("reported %q without evidence", names(res.Apps))
	}
	if res.Unknown != 2 {
		t.Errorf("unknown %d, want 2", res.Unknown)
	}
}

// A package installed as a dependency was not the user's choice; listing it
// as an app to remove would pull on whatever needs it.
func TestFindSkipsAutoInstalledPackages(t *testing.T) {
	env, dir := debEnv(t, map[string]time.Duration{"/usr/bin/yelp": 400 * day})
	env.Auto = func(pkg string) bool { return pkg == "yelp" }
	desktop(t, dir, "yelp.desktop", app("Help", "yelp"))

	if got := Find(env, 90*day).Apps; len(got) != 0 {
		t.Errorf("reported auto-installed %q", names(got))
	}
}

func TestFindSkipsHiddenAndNonApplicationEntries(t *testing.T) {
	env, dir := debEnv(t, map[string]time.Duration{"/usr/bin/x": 400 * day})
	desktop(t, dir, "hidden.desktop", app("H", "x")+"NoDisplay=true\n")
	desktop(t, dir, "link.desktop", "[Desktop Entry]\nType=Link\nName=L\nExec=x\n")

	if got := Find(env, 90*day).Apps; len(got) != 0 {
		t.Errorf("reported %q", names(got))
	}
}

// The shell's own record only ever makes an app look more used.
func TestShellRecordOverridesAnOldAtime(t *testing.T) {
	env, dir := debEnv(t, map[string]time.Duration{"/usr/bin/zed": 400 * day})
	env.ShellSeen = map[string]time.Time{"zed.desktop": now.Add(-day)}
	desktop(t, dir, "zed.desktop", app("Zed", "zed"))

	if got := Find(env, 90*day).Apps; len(got) != 0 {
		t.Errorf("reported %q though the shell saw it yesterday", names(got))
	}
}

// Two launchers from one package: the package is as used as its most used
// launcher, and is listed once.
func TestFindMergesLaunchersOfOnePackage(t *testing.T) {
	env, dir := debEnv(t, map[string]time.Duration{
		"/usr/bin/lo-writer": 400 * day,
		"/usr/bin/lo-calc":   400 * day,
	})
	env.Owner = func(string) string { return "libreoffice" }
	desktop(t, dir, "writer.desktop", app("Writer", "lo-writer"))
	desktop(t, dir, "calc.desktop", app("Calc", "lo-calc"))

	got := Find(env, 90*day).Apps
	if len(got) != 1 {
		t.Fatalf("got %d entries, want one for the package", len(got))
	}
}

func TestResolveSkipsEnvPrefixes(t *testing.T) {
	env := Env{LookPath: func(n string) (string, error) { return "/usr/bin/" + n, nil }}
	if got := resolve(env, `env GDK_BACKEND=x11 "slack" %U`); got != "/usr/bin/slack" {
		t.Errorf("resolve %q", got)
	}
	if got := resolve(env, "/opt/app/run --flag"); got != "/opt/app/run" {
		t.Errorf("resolve %q", got)
	}
}

func TestSnapEvidenceIsItsDataDirectory(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	desktop(t, dir, "slack_slack.desktop", app("Slack", "/snap/bin/slack"))
	data := filepath.Join(home, "snap", "slack", "12")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-300 * day)
	for _, p := range []string{data, filepath.Dir(data)} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	env := Env{Home: home, Now: now, Dirs: []Dir{{Path: dir, Source: Snap}}}

	got := Find(env, 90*day).Apps
	if names(got) != "slack" || got[0].Remove != "sudo snap remove slack" {
		t.Errorf("got %+v", got)
	}
}

func TestFindSortsLargestFirst(t *testing.T) {
	env, dir := debEnv(t, map[string]time.Duration{
		"/usr/bin/small": 400 * day, "/usr/bin/big": 400 * day,
	})
	env.PkgBytes = func(_ Source, p string) int64 {
		if p == "big" {
			return 1 << 30
		}
		return 1
	}
	desktop(t, dir, "small.desktop", app("S", "small"))
	desktop(t, dir, "big.desktop", app("B", "big"))

	if got := Find(env, 90*day).Apps; names(got) != "big small" {
		t.Errorf("order %q", names(got))
	}
}

// Removing a package something else depends on pulls on that package too:
// removing ibus removes ubuntu-desktop. Those are counted, not listed.
func TestFindSkipsPackagesOthersDependOn(t *testing.T) {
	env, dir := debEnv(t, map[string]time.Duration{
		"/usr/bin/ibus-setup": 400 * day, "/usr/bin/shotwell": 400 * day,
	})
	env.Needed = func(pkg string) bool { return pkg == "ibus" }
	desktop(t, dir, "ibus.desktop", app("IBus", "ibus-setup"))
	desktop(t, dir, "shotwell.desktop", app("Shotwell", "shotwell"))

	res := Find(env, 90*day)
	if names(res.Apps) != "shotwell" || res.Needed != 1 {
		t.Errorf("apps %q needed %d", names(res.Apps), res.Needed)
	}
}

// An app unpacked into /opt is its directory, not its launcher: that is what
// removing it frees and what the hint must name.
func TestLocalAppUnderOptNamesItsDirectory(t *testing.T) {
	dir := t.TempDir()
	desktop(t, dir, "Postman.desktop", app("Postman", "/opt/Postman/app/Postman %U"))
	env := Env{
		Home: t.TempDir(), Now: now, Dirs: []Dir{{Path: dir, Source: Local}},
		Used: func(string) (time.Time, bool) { return now.Add(-400 * day), true },
		DirBytes: func(p string) int64 {
			if p == "/opt/Postman" {
				return 380 << 20
			}
			return 0
		},
	}
	got := Find(env, 90*day).Apps
	if len(got) != 1 || got[0].Bytes != 380<<20 ||
		!strings.HasPrefix(got[0].Remove, "sudo rm -rf /opt/Postman && rm ") {
		t.Errorf("got %+v", got)
	}
}

// A launcher that runs a PATH symlink into /opt is still an /opt app.
func TestLocalAppFollowsSymlinksIntoOpt(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "Postman")
	if err := os.Symlink("/opt/NoSuchApp-reclaim-test/bin/x", bin); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	desktop(t, dir, "Postman.desktop", app("Postman", "Postman"))
	env := Env{
		Home: t.TempDir(), Now: now, Dirs: []Dir{{Path: dir, Source: Local}},
		LookPath: func(string) (string, error) { return bin, nil },
		Used:     func(string) (time.Time, bool) { return now.Add(-400 * day), true },
		DirBytes: func(string) int64 { return 1 },
	}
	got := Find(env, 90*day).Apps
	if len(got) != 1 || !strings.HasPrefix(got[0].Remove, "sudo rm -rf /opt/NoSuchApp-reclaim-test ") {
		t.Errorf("got %+v", got)
	}
}

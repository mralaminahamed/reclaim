package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// apt-get clean empties the archive directory and also drops the two binary
// package indexes beside it. Those are routinely 130MB between them, and a dry
// run that counted only the archive reported 0B for a cache that was not empty.
func TestAptUnitMeasuresThePackageIndexes(t *testing.T) {
	r := unit.NewRegistry()
	Add(r, Env{Has: onlyHas("apt-get")})
	u, _ := r.Get("system-apt")
	got := strings.Join(u.SizePaths, " ")
	for _, want := range []string{"/var/cache/apt/pkgcache.bin", "/var/cache/apt/srcpkgcache.bin"} {
		if !strings.Contains(got, want) {
			t.Errorf("apt unit does not measure %s: %v", want, u.SizePaths)
		}
	}
}

// The vacuum keeps a bounded window, so what it frees is the journal minus that
// window. Measuring the whole journal would overstate it; measuring nothing, as
// before, reported 0B for a gigabyte of logs.
func TestJournalUnitMeasuresWhatItWouldFree(t *testing.T) {
	r := unit.NewRegistry()
	Add(r, Env{Has: onlyHas("journalctl"), JournalKeep: "200M"})
	u, ok := r.Get("system-journal")
	if !ok {
		t.Fatal("journal unit missing")
	}
	if len(u.SizePaths) == 0 {
		t.Error("journal unit measures nothing")
	}
	if u.SizeKeep != 200<<20 {
		t.Errorf("SizeKeep %d, want the 200M the vacuum leaves behind", u.SizeKeep)
	}
}

// --- kernel residue ---

// residueEnv models a machine with kernel packages removed but not purged: dpkg
// still lists them as "rc", and /lib/modules keeps the module indexes depmod
// regenerated on the way out.
func residueEnv(t *testing.T, installed, residual []string, running string, dirs ...string) Env {
	t.Helper()
	mods := t.TempDir()
	for _, d := range dirs {
		p := filepath.Join(mods, d)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "modules.dep"), make([]byte, 4096), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Env{
		Has:            onlyHas("apt-get", "dpkg"),
		KernelPackages: func() []string { return installed },
		RunningKernel:  func() string { return running },
		Residual:       func() []Residue { return residues(residual...) },
		ModulesRoot:    mods,
		BootDir:        t.TempDir(),
	}
}

func residues(pkgs ...string) []Residue {
	out := make([]Residue, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, Residue{Package: p})
	}
	return out
}

func TestKernelResidueTakesModuleDirsOfPurgedKernels(t *testing.T) {
	env := residueEnv(t,
		[]string{"linux-image-7.0.0-34-generic", "linux-modules-7.0.0-34-generic"},
		[]string{"linux-image-7.0.0-30-generic", "linux-modules-7.0.0-30-generic"},
		"7.0.0-34-generic",
		"7.0.0-30-generic", "7.0.0-34-generic")
	r := unit.NewRegistry()
	Add(r, env)

	u, ok := r.Get("system-kernel-residue")
	if !ok {
		t.Fatal("kernel residue not registered")
	}
	gone := filepath.Join(env.ModulesRoot, "7.0.0-30-generic")
	if !strings.Contains(u.Command, gone) {
		t.Errorf("command does not remove %s: %q", gone, u.Command)
	}
	if strings.Contains(u.Command, "7.0.0-34") {
		t.Errorf("command touches the running kernel: %q", u.Command)
	}
	if !strings.Contains(u.Command, "dpkg --purge") ||
		!strings.Contains(u.Command, "linux-modules-7.0.0-30-generic") {
		t.Errorf("command does not purge the residual packages: %q", u.Command)
	}
	if u.Flag != "--kernels" || !u.Reversible || !u.NeedsRoot {
		t.Errorf("flag %q reversible %v root %v, want --kernels true true",
			u.Flag, u.Reversible, u.NeedsRoot)
	}
	if strings.Join(u.SizePaths, " ") != gone {
		t.Errorf("measures %v, want only %s", u.SizePaths, gone)
	}
}

// A module directory is only residue when nothing owns that version any more.
// If modules-extra was purged while the image is still installed, the directory
// is the live kernel's and removing it would leave that kernel without modules.
func TestKernelResidueSparesAVersionStillInstalled(t *testing.T) {
	env := residueEnv(t,
		[]string{"linux-image-7.0.0-30-generic", "linux-image-7.0.0-34-generic"},
		[]string{"linux-modules-extra-7.0.0-30-generic"},
		"7.0.0-34-generic",
		"7.0.0-30-generic")
	r := unit.NewRegistry()
	Add(r, env)

	if u, ok := r.Get("system-kernel-residue"); ok {
		if strings.Contains(u.Command, "rm ") || len(u.SizePaths) > 0 {
			t.Errorf("removes the modules of an installed kernel: %q", u.Command)
		}
	}
}

// A kernel installed outside dpkg -- a mainline build, a custom one -- has a
// module directory and a /boot image but no package. The running kernel may be
// exactly that, so neither may ever be taken on the strength of a stale rc
// entry that happens to share its version.
func TestKernelResidueSparesAKernelWithABootImage(t *testing.T) {
	env := residueEnv(t, nil,
		[]string{"linux-image-7.0.0-30-generic"},
		"7.0.0-34-generic",
		"7.0.0-30-generic")
	if err := os.WriteFile(filepath.Join(env.BootDir, "vmlinuz-7.0.0-30-generic"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r := unit.NewRegistry()
	Add(r, env)

	if u, ok := r.Get("system-kernel-residue"); ok && strings.Contains(u.Command, "rm ") {
		t.Errorf("removes modules of a kernel that still boots: %q", u.Command)
	}
}

func TestKernelResidueNeverTakesTheRunningKernel(t *testing.T) {
	env := residueEnv(t, nil,
		[]string{"linux-modules-7.0.0-34-generic"},
		"7.0.0-34-generic",
		"7.0.0-34-generic")
	r := unit.NewRegistry()
	Add(r, env)

	if u, ok := r.Get("system-kernel-residue"); ok && strings.Contains(u.Command, "7.0.0-34") {
		t.Errorf("touches the running kernel: %q", u.Command)
	}
}

func TestKernelResidueIsAbsentWithNothingLeftBehind(t *testing.T) {
	env := residueEnv(t, []string{"linux-image-7.0.0-34-generic"}, nil, "7.0.0-34-generic")
	r := unit.NewRegistry()
	Add(r, env)
	if _, ok := r.Get("system-kernel-residue"); ok {
		t.Error("registered with no residue")
	}
}

// --- residual configuration ---

// A removed-but-not-purged package leaves its configuration in /etc. Those
// files may carry edits nobody else has a copy of, so purging them destroys
// information: the unit is lossy and needs both its flag and --allow-lossy.
func TestResidualConfigIsLossyAndOptIn(t *testing.T) {
	etc := t.TempDir()
	conf := filepath.Join(etc, "php.ini")
	if err := os.WriteFile(conf, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	env := Env{
		Has: onlyHas("dpkg"),
		Residual: func() []Residue {
			return []Residue{{Package: "php-zts-cli", Files: []string{conf}}}
		},
	}
	r := unit.NewRegistry()
	Add(r, env)

	u, ok := r.Get("system-residual-config")
	if !ok {
		t.Fatal("residual config not registered")
	}
	if u.Reversible {
		t.Error("purging config files destroys local edits; must not be reversible")
	}
	if u.Flag != "--obsolete" {
		t.Errorf("flag %q, want --obsolete", u.Flag)
	}
	if u.Command != "sudo dpkg --purge php-zts-cli" {
		t.Errorf("command %q", u.Command)
	}
	if strings.Join(u.Detail, " ") != "php-zts-cli" {
		t.Errorf("detail %v does not name the package", u.Detail)
	}
	if strings.Join(u.SizePaths, " ") != conf {
		t.Errorf("measures %v, want the leftover conffile", u.SizePaths)
	}
}

// Kernel packages leave nothing anybody edited, so they belong to the
// reversible kernel-residue unit and must not drag the lossy gate in with them.
func TestResidualConfigLeavesKernelsToTheKernelUnit(t *testing.T) {
	env := Env{
		Has: onlyHas("dpkg"),
		Residual: func() []Residue {
			return residues("linux-image-7.0.0-30-generic", "php-zts-cli")
		},
	}
	r := unit.NewRegistry()
	Add(r, env)
	u, ok := r.Get("system-residual-config")
	if !ok {
		t.Fatal("residual config not registered")
	}
	if strings.Contains(u.Command, "linux-") {
		t.Errorf("residual config purges a kernel package: %q", u.Command)
	}
}

// Package names come from dpkg, not from the user, but they still reach a
// shell. Anything outside dpkg's own name grammar is refused, not quoted.
func TestResidualConfigRefusesNamesOutsideDpkgGrammar(t *testing.T) {
	env := Env{
		Has:      onlyHas("dpkg"),
		Residual: func() []Residue { return residues("ok-pkg", "bad;rm -rf /") },
	}
	r := unit.NewRegistry()
	Add(r, env)
	u, _ := r.Get("system-residual-config")
	if strings.Contains(u.Command, ";") {
		t.Errorf("command carries an unvalidated name: %q", u.Command)
	}
}

// --- rotated logs ---

func logEnv(dir string) Env {
	return Env{Has: onlyHas(), LogDir: dir}
}

func writeAged(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestOldLogsTakesOnlyRotatedAndOldFiles(t *testing.T) {
	dir := t.TempDir()
	old := 60 * 24 * time.Hour
	take := []string{
		filepath.Join(dir, "syslog.2.gz"),
		filepath.Join(dir, "apt", "history.log.1.gz"),
		filepath.Join(dir, "kern.log.1"),
		filepath.Join(dir, "dpkg.log-20250101.xz"),
		filepath.Join(dir, "Xorg.0.log.old"),
	}
	for _, p := range take {
		writeAged(t, p, old)
	}
	keep := map[string]time.Duration{
		filepath.Join(dir, "syslog"):                           old, // live log
		filepath.Join(dir, "syslog.1"):                         time.Hour,
		filepath.Join(dir, "journal", "x", "system@a.journal"): old,
		filepath.Join(dir, "Xorg.0.log"):                       old,
	}
	for p, age := range keep {
		writeAged(t, p, age)
	}

	r := unit.NewRegistry()
	Add(r, logEnv(dir))
	u, ok := r.Get("system-old-logs")
	if !ok {
		t.Fatal("old logs not registered")
	}
	got := map[string]bool{}
	for _, p := range u.SizePaths {
		got[p] = true
	}
	for _, p := range take {
		if !got[p] {
			t.Errorf("does not take %s", p)
		}
		if !strings.Contains(u.Command, p) {
			t.Errorf("command omits %s", p)
		}
	}
	for p := range keep {
		if got[p] || strings.Contains(u.Command, "'"+p+"'") {
			t.Errorf("takes %s", p)
		}
	}
	if u.Reversible || u.Flag != "--obsolete" {
		t.Errorf("reversible %v flag %q: a log is a record, not a cache", u.Reversible, u.Flag)
	}
}

// The command deletes exactly the files the dry run listed, each quoted, so a
// name with a space or a quote cannot be split into something else.
func TestOldLogsCommandQuotesEachPath(t *testing.T) {
	dir := t.TempDir()
	odd := filepath.Join(dir, "it's a log.1.gz")
	writeAged(t, odd, 60*24*time.Hour)

	r := unit.NewRegistry()
	Add(r, logEnv(dir))
	u, ok := r.Get("system-old-logs")
	if !ok {
		t.Fatal("old logs not registered")
	}
	want := `'` + strings.ReplaceAll(odd, `'`, `'\''`) + `'`
	if !strings.Contains(u.Command, want) {
		t.Errorf("command %q does not quote %s", u.Command, odd)
	}
}

func TestOldLogsIsAbsentWithNothingOld(t *testing.T) {
	dir := t.TempDir()
	writeAged(t, filepath.Join(dir, "syslog.1.gz"), time.Hour)
	r := unit.NewRegistry()
	Add(r, logEnv(dir))
	if _, ok := r.Get("system-old-logs"); ok {
		t.Error("registered with nothing old enough")
	}
}

// The shape dpkg-query actually prints: conffiles start on the package's own
// line and continue on indented lines, and installed packages are interleaved.
func TestParseResidueReadsRealDpkgOutput(t *testing.T) {
	out := "ii |bash| /etc/bash.bashrc 89269e1298235f1b12b4c16e4065ad0d\n" +
		" /etc/skel/.bashrc 0f4a5e5f3a2b1c0d9e8f7a6b5c4d3e2f\n" +
		"rc |php-zts-cli| /etc/php-zts/conf.d/calendar.ini 59c1b95abf409049d2ab9bf3644c61db\n" +
		" /etc/php-zts/conf.d/ctype.ini f3f0564a992b3ef2502210461cd11a57\n" +
		" /etc/php-zts/old.ini 00000000000000000000000000000000 obsolete\n" +
		"rc |linux-modules-7.0.0-30-generic|\n" +
		"ii |coreutils|\n"
	got := parseResidue(out)
	if len(got) != 2 {
		t.Fatalf("got %d residues, want 2: %+v", len(got), got)
	}
	if got[0].Package != "php-zts-cli" || len(got[0].Files) != 3 {
		t.Errorf("first residue %+v", got[0])
	}
	if got[1].Package != "linux-modules-7.0.0-30-generic" || len(got[1].Files) != 0 {
		t.Errorf("second residue %+v", got[1])
	}
}

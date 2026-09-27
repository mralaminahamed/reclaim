// Package testharness runs the reclaim binary inside a sealed sandbox. Only
// tests import it.
//
// The binary deletes files, so a test that runs it must not be able to reach
// the machine it runs on. The environment is built from nothing rather than
// filtered from the developer's: a variable nobody thought to filter, such as
// GOMODCACHE pointing at a real cache, is exactly what would leak. PATH holds
// only stubs, so every native command a unit could run is either a stub the
// test installed and can inspect, or absent.
package testharness

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type Sandbox struct {
	t testing.TB
	// Root holds everything. Home is the fake home, Bin the stub PATH,
	// Outside a sibling tree that hostile symlinks point into, Tmp is TMPDIR.
	Root, Home, Bin, Outside, Tmp string

	binary    string
	log       string
	protected []string
}

// New makes a sandbox around binary, the program Run executes.
func New(t testing.TB, binary string) *Sandbox {
	t.Helper()
	root := t.TempDir()
	s := &Sandbox{
		t: t, Root: root, binary: binary,
		Home:    filepath.Join(root, "home"),
		Bin:     filepath.Join(root, "bin"),
		Outside: filepath.Join(root, "outside"),
		Tmp:     filepath.Join(root, "tmp"),
		log:     filepath.Join(root, "calls.log"),
	}
	for _, d := range []string{s.Home, s.Bin, s.Outside, s.Tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	linkShell(t, s.Bin)
	return s
}

// StubDir makes a stub PATH directory holding only sh, for tests that manage
// their own home but still need a sealed PATH.
func StubDir(t testing.TB) string {
	t.Helper()
	d := t.TempDir()
	linkShell(t, d)
	return d
}

// linkShell puts sh on the stub PATH: the runner executes commands through
// "sh -c", and without it no command unit could run at all.
func linkShell(t testing.TB, dir string) {
	t.Helper()
	if err := os.Symlink("/bin/sh", filepath.Join(dir, "sh")); err != nil {
		t.Fatal(err)
	}
}

func (s *Sandbox) Env() []string { return SealedEnv(s.Home, s.Bin, s.Tmp) }

// SealedEnv is the whole environment a sandboxed run sees. Nothing is
// inherited.
func SealedEnv(home, bin, tmp string) []string {
	return []string{
		"PATH=" + bin,
		"HOME=" + home,
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"TMPDIR=" + tmp,
		"LANG=C",
		"TZ=UTC",
		"RECLAIM_NO_OPLOG=1",
	}
}

// Stub installs a fake program on PATH. It logs "name args..." to the call
// log, then runs body, which may simulate the tool's effect or exit non-zero.
func (s *Sandbox) Stub(name, body string) {
	s.t.Helper()
	script := "#!/bin/sh\necho \"" + name + " $*\" >> '" + s.log + "'\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(s.Bin, name), []byte(script), 0o755); err != nil {
		s.t.Fatal(err)
	}
}

// Calls returns every stub invocation so far, in order.
func (s *Sandbox) Calls() []string {
	data, err := os.ReadFile(s.log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

type Result struct {
	Out  string
	Code int
}

// Run executes the binary with the sealed environment, from Home.
func (s *Sandbox) Run(args ...string) Result {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.binary, args...)
	cmd.Env = s.Env()
	cmd.Dir = s.Home
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return Result{string(out), ee.ExitCode()}
	case err != nil:
		s.t.Fatalf("running %v: %v", args, err)
	}
	return Result{string(out), 0}
}

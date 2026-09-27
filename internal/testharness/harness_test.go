package testharness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sandbox is only worth anything if a variable set in the developer's
// shell cannot reach the binary under test.
func TestEnvInheritsNothing(t *testing.T) {
	t.Setenv("GOMODCACHE", "/canary/should-not-leak")
	s := New(t, "/bin/sh")

	// export -p is a shell builtin: env itself is not on the sealed PATH.
	r := s.Run("-c", "export -p")

	if strings.Contains(r.Out, "GOMODCACHE") {
		t.Errorf("GOMODCACHE leaked into the sandbox:\n%s", r.Out)
	}
	// Quoting differs between dash and bash, so check content, not syntax:
	// the stub directory is there and no system directory is.
	if !strings.Contains(r.Out, s.Bin) || strings.Contains(r.Out, "/usr/bin") {
		t.Errorf("PATH is not the stub directory alone:\n%s", r.Out)
	}
}

func TestStubRecordsCalls(t *testing.T) {
	s := New(t, "/bin/sh")
	s.Stub("npm", "")

	s.Run("-c", "npm cache clean --force")

	got := s.Calls()
	if len(got) != 1 || got[0] != "npm cache clean --force" {
		t.Errorf("Calls() = %q, want the one npm invocation", got)
	}
}

func TestUnstubbedProgramIsNotFound(t *testing.T) {
	s := New(t, "/bin/sh")

	r := s.Run("-c", "go version")

	if r.Code == 0 {
		t.Errorf("a program with no stub ran:\n%s", r.Out)
	}
}

func TestBuildMakesEveryKind(t *testing.T) {
	s := New(t, "/bin/sh")
	s.Build(
		Entry{Path: "home/a/file", Kind: File, Size: 10},
		Entry{Path: "home/a/link", Kind: Symlink, Target: "outside"},
		Entry{Path: "home/a/hard", Kind: Hardlink, Target: "home/a/file"},
		Entry{Path: "home/a/sparse", Kind: Sparse, Size: 1 << 20},
		Entry{Path: "home/ro", Kind: Dir, Mode: 0o555},
	)
	if fi, err := os.Lstat(filepath.Join(s.Root, "home/a/link")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink not made: %v", err)
	}
	if fi, _ := os.Stat(filepath.Join(s.Root, "home/ro")); fi.Mode().Perm() != 0o555 {
		t.Errorf("mode not applied: %v", fi.Mode())
	}
}

// recorder stands in for *testing.T so the harness's own failure reporting can
// be observed without failing this test.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func TestApplyFailsWhenAProtectedFileChanges(t *testing.T) {
	rec := &recorder{TB: t}
	s := New(rec, "/bin/sh")
	s.Build(Entry{Path: "home/keep", Kind: File, Size: 5, Protected: true})

	s.Apply("-c", "echo x >> '"+filepath.Join(s.Root, "home/keep")+"'", "--apply")

	if len(rec.errs) == 0 {
		t.Fatal("a changed protected file was not reported")
	}
}

func TestApplyPassesWhenNothingProtectedChanges(t *testing.T) {
	rec := &recorder{TB: t}
	s := New(rec, "/bin/sh")
	s.Build(
		Entry{Path: "home/keep", Kind: File, Size: 5, Protected: true},
		Entry{Path: "home/junk", Kind: File, Size: 5},
	)

	s.Apply("-c", "rm '"+filepath.Join(s.Root, "home/junk")+"'", "--apply")

	if len(rec.errs) != 0 {
		t.Fatalf("false alarm: %v", rec.errs)
	}
}

// reclaim sweeps /tmp by default. A sandboxed run must sweep the sandbox's
// own tmp and never the developer's.
func TestSweptTmpIsTheSandboxes(t *testing.T) {
	s := New(t, "/bin/sh")
	r := s.Run("-c", "export -p")
	if !strings.Contains(r.Out, "RECLAIM_TMP_ROOTS") || !strings.Contains(r.Out, s.Tmp) {
		t.Errorf("RECLAIM_TMP_ROOTS is not the sandbox tmp:\n%s", r.Out)
	}
}

func TestExtraEnvReachesTheRun(t *testing.T) {
	s := New(t, "/bin/sh")
	s.ExtraEnv = []string{"RECLAIM_TMP_IDLE=1s"}
	if r := s.Run("-c", "export -p"); !strings.Contains(r.Out, "RECLAIM_TMP_IDLE") {
		t.Errorf("extra env missing:\n%s", r.Out)
	}
}

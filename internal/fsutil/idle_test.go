package fsutil

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mkfile(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setTimes(t *testing.T, p string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
}

func base(ps []string) map[string]bool {
	out := map[string]bool{}
	for _, p := range ps {
		out[filepath.Base(p)] = true
	}
	return out
}

// ctime cannot be set back, so "idle" is tested with a cutoff in the future:
// everything created now is older than it, and a time set past it is use.
func TestIdleEntriesTakesOnlyEntriesUnusedSinceTheCutoff(t *testing.T) {
	dir := t.TempDir()
	cutoff := time.Now().Add(time.Hour)
	mkfile(t, filepath.Join(dir, "old-file"))
	mkfile(t, filepath.Join(dir, "old-dir/a/b"))
	mkfile(t, filepath.Join(dir, "busy-dir/a/deep"))
	setTimes(t, filepath.Join(dir, "busy-dir/a/deep"), cutoff.Add(time.Hour))
	mkfile(t, filepath.Join(dir, "busy-file"))
	setTimes(t, filepath.Join(dir, "busy-file"), cutoff.Add(time.Hour))

	got := base(IdleEntries(dir, cutoff, uint32(os.Geteuid())))
	if !got["old-file"] || !got["old-dir"] || got["busy-dir"] || got["busy-file"] || len(got) != 2 {
		t.Fatalf("got %v, want old-file and old-dir only", got)
	}
}

// Extracting an archive keeps its old modification times. The change time is
// the extraction, so the freshly unpacked tree is not idle.
func TestIdleEntriesCountsChangeTime(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "unpacked/file")
	mkfile(t, p)
	old := time.Now().Add(-90 * 24 * time.Hour)
	setTimes(t, p, old)
	setTimes(t, filepath.Dir(p), old)

	if got := IdleEntries(dir, time.Now().Add(-7*24*time.Hour), uint32(os.Geteuid())); len(got) != 0 {
		t.Fatalf("got %v: a tree changed just now is not idle", got)
	}
}

// A socket is a rendezvous for something running -- tmux, an ssh agent -- and
// its times say nothing about whether that is still alive.
func TestIdleEntriesSkipsAnythingHoldingASocket(t *testing.T) {
	dir := t.TempDir()
	sockDir := filepath.Join(dir, "tmux-1000")
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(sockDir, "default"))
	if err != nil {
		t.Skip("cannot create a unix socket here:", err)
	}
	defer l.Close()

	if got := IdleEntries(dir, time.Now().Add(time.Hour), uint32(os.Geteuid())); len(got) != 0 {
		t.Fatalf("got %v, want the socket's directory left alone", got)
	}
}

// Someone else's entry is theirs; the sticky bit would refuse it anyway.
func TestIdleEntriesTakesOnlyTheOwnersEntries(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, filepath.Join(dir, "mine"))
	if got := IdleEntries(dir, time.Now().Add(time.Hour), uint32(os.Geteuid())+1); len(got) != 0 {
		t.Fatalf("got %v for another uid", got)
	}
}

func TestIdleSince(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	mkfile(t, p)
	if !IdleSince(p, time.Now().Add(time.Hour)) {
		t.Error("not idle before a future cutoff")
	}
	if IdleSince(p, time.Now().Add(-time.Hour)) {
		t.Error("idle though created after the cutoff")
	}
	if IdleSince(filepath.Join(t.TempDir(), "gone"), time.Now()) {
		t.Error("a missing path counts as idle")
	}
}

// Listing a directory sets its access time -- and reclaim lists it to decide
// and again to measure. That is not use, so a directory's access time is not
// counted; its files' are.
func TestIdleIgnoresADirectorysAccessTime(t *testing.T) {
	dir := t.TempDir()
	mkfile(t, filepath.Join(dir, "listed/file"))
	cutoff := time.Now().Add(time.Hour)
	later := cutoff.Add(time.Hour)
	listed := filepath.Join(dir, "listed")
	if err := os.Chtimes(listed, later, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !IdleSince(listed, cutoff) {
		t.Error("a directory only listed since the cutoff counted as used")
	}
	if err := os.Chtimes(filepath.Join(listed, "file"), later, time.Now()); err != nil {
		t.Fatal(err)
	}
	if IdleSince(listed, cutoff) {
		t.Error("a file read since the cutoff did not count as use")
	}
}

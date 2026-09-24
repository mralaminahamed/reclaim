package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexBuildShowAndRefresh(t *testing.T) {
	home := t.TempDir()
	big := filepath.Join(home, "big")
	if err := os.MkdirAll(big, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(big, "blob"), make([]byte, 3<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := run(t, home, "index", "--root", home)
	if code != 0 || !strings.Contains(out, "full") {
		t.Fatalf("first build exited %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".cache/reclaim/index.gob")); err != nil {
		t.Fatalf("index not written: %v", err)
	}
	out, _ = run(t, home, "index", "--root", home)
	if !strings.Contains(out, "refresh") {
		t.Errorf("second build did not refresh:\n%s", out)
	}
	out, code = run(t, home, "index", "show", home)
	if code != 0 || !strings.Contains(out, big) || !strings.Contains(out, "3.0MiB") {
		t.Errorf("show exited %d:\n%s", code, out)
	}
}

// With a fresh index, analyze answers from it and says so.
func TestAnalyzeUsesAFreshIndexAndSaysSo(t *testing.T) {
	home := t.TempDir()
	big := filepath.Join(home, "big")
	os.MkdirAll(big, 0o755)
	os.WriteFile(filepath.Join(big, "blob"), make([]byte, 2<<20), 0o644)
	if _, code := run(t, home, "index", "--root", home); code != 0 {
		t.Fatal("index failed")
	}
	out, code := run(t, home, "analyze", "--min", "1M")
	if code != 0 || !strings.Contains(out, "from the index") || !strings.Contains(out, big) {
		t.Errorf("analyze exited %d:\n%s", code, out)
	}
}

func TestIndexShowWithoutAnIndexSaysHowToBuildOne(t *testing.T) {
	out, code := run(t, t.TempDir(), "index", "show")
	if code == 0 || !strings.Contains(out, "reclaim index") {
		t.Errorf("exited %d:\n%s", code, out)
	}
}

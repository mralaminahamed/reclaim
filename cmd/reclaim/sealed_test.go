package main

import (
	"os"
	"path/filepath"
	"testing"
)

// run() used to pass os.Environ() through with only HOME replaced, so an
// apply test executed the real go, npm and uv on the developer's machine. A
// developer with GOMODCACHE or npm_config_cache set would have had a real
// cache wiped by "go test".
func TestTestsCannotReachRealCaches(t *testing.T) {
	canary := t.TempDir()
	for _, v := range []string{"GOMODCACHE", "GOCACHE", "npm_config_cache", "UV_CACHE_DIR", "XDG_CACHE_HOME"} {
		dir := filepath.Join(canary, v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "precious"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv(v, dir)
	}

	if out, code := run(t, fixtureHome(t), "clean", "--apply", "--yes"); code != 0 {
		t.Fatalf("clean --apply exited %d:\n%s", code, out)
	}

	for _, v := range []string{"GOMODCACHE", "GOCACHE", "npm_config_cache", "UV_CACHE_DIR", "XDG_CACHE_HOME"} {
		if _, err := os.Stat(filepath.Join(canary, v, "precious")); err != nil {
			t.Errorf("%s was reached by the test: %v", v, err)
		}
	}
}

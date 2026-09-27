package discover

import (
	"path/filepath"
	"runtime"
	"testing"
)

// Discovery hardcoded the XDG tree. On macOS almost none of that exists, so the
// sweep would walk an empty directory and find nothing, on the platform where
// ~/Library/Caches is the single biggest thing worth sweeping.
func TestCacheRootIsThePlatformsOwn(t *testing.T) {
	got := CacheRoot("/home/u")

	want := "/home/u/.cache"
	if runtime.GOOS == "darwin" {
		want = "/home/u/Library/Caches"
	}
	if got != want {
		t.Fatalf("cache root %q, want %q", got, want)
	}
}

// The nested sweep matches by name inside an application's own directory,
// which is what makes it safe to point at a directory holding real state:
// ~/Library/Application Support/Slack/Cache is a cache, and its siblings are
// not touched.
func TestNestedRootsCoverThePlatformsApplicationState(t *testing.T) {
	got := NestedRoots("/home/u")

	if len(got) == 0 {
		t.Fatal("no roots")
	}
	want := filepath.Join("/home/u", ".config")
	if runtime.GOOS == "darwin" {
		want = filepath.Join("/home/u", "Library", "Application Support")
	}
	for _, r := range got {
		if r == want {
			return
		}
	}
	t.Fatalf("roots %v do not include %q", got, want)
}

// A relocated cache home is where the tools write, so it is what the sweep
// walks. On macOS the cache root is ~/Library/Caches, which the variable does
// not move.
func TestCacheRootFollowsXDGCacheHome(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	got := CacheRootFrom(home, func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return xdg
		}
		return ""
	})
	want := xdg
	if runtime.GOOS == "darwin" {
		want = CacheRoot(home)
	}
	if got != want {
		t.Fatalf("cache root %q, want %q", got, want)
	}
}

// Unset, relative, or aimed at something the sweep must never treat as a
// cache root: the default stands.
func TestCacheRootIgnoresUnusableXDGCacheHome(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"", "rel/cache", "/", home, "/usr"} {
		got := CacheRootFrom(home, func(string) string { return v })
		if got != CacheRoot(home) {
			t.Errorf("XDG_CACHE_HOME=%q: root %q, want the default", v, got)
		}
	}
}

func TestNestedRootsFollowXDGConfigAndDataHome(t *testing.T) {
	home, cfg, data := t.TempDir(), t.TempDir(), t.TempDir()
	env := map[string]string{"XDG_CONFIG_HOME": cfg, "XDG_DATA_HOME": data}
	got := NestedRootsFrom(home, func(k string) string { return env[k] })
	want := []string{cfg, data}
	if runtime.GOOS == "darwin" {
		want = NestedRoots(home)
	}
	if len(got) != len(want) || got[0] != want[0] || got[len(got)-1] != want[len(want)-1] {
		t.Fatalf("roots %v, want %v", got, want)
	}
}

func TestNestedRootsIgnoreUnusableXDGValues(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"", "rel/dir", "/", home, "/usr"} {
		got := NestedRootsFrom(home, func(string) string { return v })
		want := NestedRoots(home)
		if len(got) != len(want) || got[0] != want[0] || got[len(got)-1] != want[len(want)-1] {
			t.Errorf("value %q: roots %v, want the defaults %v", v, got, want)
		}
	}
}

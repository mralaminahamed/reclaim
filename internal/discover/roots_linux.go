//go:build linux

package discover

import (
	"path/filepath"

	"github.com/mralaminahamed/reclaim/internal/runner"
)

// CacheRoot is the directory whose children are regenerable by location. On
// Linux that is the XDG cache home.
func CacheRoot(home string) string { return filepath.Join(home, ".cache") }

// CacheRootFrom is CacheRoot moved by $XDG_CACHE_HOME, where it is set to an
// absolute path the runner would accept. A relative value is invalid by the
// XDG spec; one aimed at home or / would make the whole of it a cache.
func CacheRootFrom(home string, getenv func(string) string) string {
	v := getenv("XDG_CACHE_HOME")
	if !filepath.IsAbs(v) || runner.CheckSafe(v, home) != nil {
		return CacheRoot(home)
	}
	return filepath.Clean(v)
}

// NestedRoots are directories holding per-application state, swept one level
// deeper and matched by name rather than by location -- a cache in here sits
// beside data that is not one.
func NestedRoots(home string) []string {
	return []string{
		filepath.Join(home, ".config"),
		filepath.Join(home, ".local", "share"),
	}
}

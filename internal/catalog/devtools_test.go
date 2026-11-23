package catalog

import (
	"path/filepath"
	"testing"
)

// Each unit registers only the directories that exist, so a clean home holds
// none of them and a populated one holds exactly what was created.
func TestDevToolCachesRegisterWhenPresent(t *testing.T) {
	home := t.TempDir()
	want := map[string]string{
		"devtool-caches":         ".cache/eslint",
		"expo-caches":            ".expo/schema-cache",
		"cli-logs":               ".kube/cache",
		"version-manager-caches": ".pyenv/cache",
		"xcode-doc-cache":        "Library/Developer/Xcode/DocumentationCache",
	}
	for _, rel := range want {
		mkdir(t, filepath.Join(home, rel))
	}
	r := Build(Env{Home: home, Has: func(string) bool { return false }})
	for id := range want {
		if _, ok := r.Get(id); !ok {
			t.Errorf("%s not registered though its directory exists", id)
		}
	}
	empty := Build(Env{Home: t.TempDir(), Has: func(string) bool { return false }})
	for id := range want {
		if _, ok := empty.Get(id); ok {
			t.Errorf("%s registered on a home without it", id)
		}
	}
}

// ~/.expo holds the login session next to its caches; only the named cache
// directories may be claimed, never the parent.
func TestExpoCachesNeverClaimTheSession(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".expo/schema-cache"))
	mkdir(t, filepath.Join(home, ".expo/state"))
	r := Build(Env{Home: home, Has: func(string) bool { return false }})
	u, ok := r.Get("expo-caches")
	if !ok {
		t.Fatal("expo-caches not registered")
	}
	for _, p := range u.Paths {
		if p == filepath.Join(home, ".expo") || p == filepath.Join(home, ".expo/state") {
			t.Errorf("expo-caches claims %s", p)
		}
	}
}

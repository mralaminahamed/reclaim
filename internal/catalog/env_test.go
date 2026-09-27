package catalog

import (
	"path/filepath"
	"testing"
)

func envOf(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func pathsOf(t *testing.T, env Env, id string) []string {
	t.Helper()
	u, ok := Build(env).Get(id)
	if !ok {
		return nil
	}
	return u.Paths
}

func has(ps []string, want string) bool {
	for _, p := range ps {
		if p == want {
			return true
		}
	}
	return false
}

// A relocated cache home moves every ~/.cache entry: that is where the tools
// write once it is set, often on a second disk.
func TestXDGCacheHomeRelocatesCacheEntries(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	mkdir(t, filepath.Join(xdg, "go-build"))
	mkdir(t, filepath.Join(xdg, "pip"))

	env := Env{Home: home, Has: func(string) bool { return false },
		Getenv: envOf(map[string]string{"XDG_CACHE_HOME": xdg})}
	for id, dir := range map[string]string{"go-build": "go-build", "pip-cache": "pip"} {
		if got := pathsOf(t, env, id); !has(got, filepath.Join(xdg, dir)) {
			t.Errorf("%s paths %v, want the relocated %s", id, got, filepath.Join(xdg, dir))
		}
	}
}

func TestUnsetXDGCacheHomeKeepsTheDefault(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".cache/go-build"))
	env := Env{Home: home, Has: func(string) bool { return false }, Getenv: envOf(nil)}
	if got := pathsOf(t, env, "go-build"); !has(got, filepath.Join(home, ".cache/go-build")) {
		t.Errorf("paths %v, want the default under ~/.cache", got)
	}
}

// The XDG spec says a relative value is invalid and must be ignored. Read
// against the working directory, it would name whatever sits there.
func TestRelativeXDGCacheHomeIsIgnored(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	mkdir(t, filepath.Join(home, ".cache/go-build"))
	mkdir(t, filepath.Join(cwd, "relative/cache/go-build"))
	t.Chdir(cwd)
	env := Env{Home: home, Has: func(string) bool { return false },
		Getenv: envOf(map[string]string{"XDG_CACHE_HOME": "relative/cache"})}
	got := pathsOf(t, env, "go-build")
	if len(got) != 1 || got[0] != filepath.Join(home, ".cache/go-build") {
		t.Errorf("paths %v, want only the default", got)
	}
}

func TestModelStoresFollowTheirVariables(t *testing.T) {
	home, disk := t.TempDir(), t.TempDir()
	hf, hub, torch := filepath.Join(disk, "hf"), filepath.Join(disk, "hub"), filepath.Join(disk, "torch")
	for _, d := range []string{hf, hub, torch} {
		mkdir(t, d)
	}
	env := Env{Home: home, Has: func(string) bool { return false },
		Getenv: envOf(map[string]string{"HF_HOME": hf, "HF_HUB_CACHE": hub, "TORCH_HOME": torch})}

	if got := pathsOf(t, env, "hf-cache"); !has(got, hf) || !has(got, hub) {
		t.Errorf("hf-cache paths %v, want HF_HOME and HF_HUB_CACHE", got)
	}
	if got := pathsOf(t, env, "torch-hub"); !has(got, torch) {
		t.Errorf("torch-hub paths %v, want TORCH_HOME", got)
	}
}

func TestLegacyHubCacheVariableStillCounts(t *testing.T) {
	home, hub := t.TempDir(), t.TempDir()
	env := Env{Home: home, Has: func(string) bool { return false },
		Getenv: envOf(map[string]string{"HUGGINGFACE_HUB_CACHE": hub})}
	if got := pathsOf(t, env, "hf-cache"); !has(got, hub) {
		t.Errorf("hf-cache paths %v, want HUGGINGFACE_HUB_CACHE", got)
	}
}

// A variable is the user's word for where a cache lives, not a licence to
// delete whatever it names. One aimed at home, or at /, registers nothing.
func TestVariableAimedAtAProtectedPathIsRefused(t *testing.T) {
	home := t.TempDir()
	for _, bad := range []string{"/", home, "/usr"} {
		env := Env{Home: home, Has: func(string) bool { return false },
			Getenv: envOf(map[string]string{"HF_HOME": bad, "TORCH_HOME": bad})}
		if got := pathsOf(t, env, "hf-cache"); has(got, bad) {
			t.Errorf("HF_HOME=%s registered %v", bad, got)
		}
		if got := pathsOf(t, env, "torch-hub"); has(got, bad) {
			t.Errorf("TORCH_HOME=%s registered %v", bad, got)
		}
	}
}

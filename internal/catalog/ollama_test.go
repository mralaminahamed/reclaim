package catalog

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

func ollamaEnv(home string, list func() ([]OllamaModel, error), env map[string]string) Env {
	return Env{Home: home, Has: func(b string) bool { return b == "ollama" },
		Ollama: list, Getenv: envOf(env)}
}

func twoModels() ([]OllamaModel, error) {
	return []OllamaModel{{Name: "llama3.2:latest", Size: "2.0 GB"}, {Name: "uposham-lfm2.5:latest", Size: "1.6 GB"}}, nil
}

// The server keeps its own index, so models go through "ollama rm" and never
// by deleting blobs under it. The unit removes exactly the models it listed.
func TestOllamaUnitRemovesExactlyTheListedModels(t *testing.T) {
	u, ok := Build(ollamaEnv(t.TempDir(), twoModels, nil)).Get("ollama-models")
	if !ok {
		t.Fatal("ollama-models not registered")
	}
	if u.Kind != unit.KindCmd || u.Command != "ollama rm llama3.2:latest uposham-lfm2.5:latest" {
		t.Errorf("kind %v command %q", u.Kind, u.Command)
	}
	if len(u.Detail) != 2 || !strings.Contains(u.Detail[1], "uposham-lfm2.5:latest") || !strings.Contains(u.Detail[1], "1.6 GB") {
		t.Errorf("detail %v, want each model named with its size", u.Detail)
	}
}

// A model built locally with "ollama create" does not come back from a pull,
// and nothing on disk tells it apart. So the unit is lossy: --models and
// --allow-lossy both.
func TestOllamaUnitIsLossyAndBehindModels(t *testing.T) {
	u, _ := Build(ollamaEnv(t.TempDir(), twoModels, nil)).Get("ollama-models")
	if u == nil || u.Reversible || u.Flag != "--models" || u.Tier != unit.TierColdReload {
		t.Fatalf("unit %+v: want lossy, --models, cold reload", u)
	}
}

// What cannot be listed cannot be previewed, so it is not offered.
func TestOllamaUnitNeedsAListing(t *testing.T) {
	for name, list := range map[string]func() ([]OllamaModel, error){
		"error": func() ([]OllamaModel, error) { return nil, errors.New("could not connect") },
		"empty": func() ([]OllamaModel, error) { return nil, nil },
		"nil":   nil,
	} {
		if _, ok := Build(ollamaEnv(t.TempDir(), list, nil)).Get("ollama-models"); ok {
			t.Errorf("%s: registered", name)
		}
	}
	noTool := Env{Home: t.TempDir(), Has: func(string) bool { return false }, Ollama: twoModels}
	if _, ok := Build(noTool).Get("ollama-models"); ok {
		t.Error("registered without ollama installed")
	}
}

// Names reach a shell. Anything outside ollama's name alphabet is dropped
// rather than quoted.
func TestOllamaUnitDropsNamesThatAreNotNames(t *testing.T) {
	list := func() ([]OllamaModel, error) {
		return []OllamaModel{{Name: "ok:latest"}, {Name: "x; rm -rf ~"}, {Name: "$(id)"}}, nil
	}
	u, _ := Build(ollamaEnv(t.TempDir(), list, nil)).Get("ollama-models")
	if u == nil || u.Command != "ollama rm ok:latest" {
		t.Fatalf("unit %+v, want only the valid name", u)
	}
}

// The store is found, not assumed: OLLAMA_MODELS, a user-run server's
// ~/.ollama/models, or the packaged service's home. It is measured, never
// deleted -- the command does the deleting.
func TestOllamaStoreIsFoundWhereverItIs(t *testing.T) {
	home, moved, sys := t.TempDir(), t.TempDir(), t.TempDir()
	mkdir(t, filepath.Join(home, ".ollama/models"))
	saved := ollamaSystemStores
	ollamaSystemStores = []string{sys, filepath.Join(t.TempDir(), "missing")}
	defer func() { ollamaSystemStores = saved }()

	u, _ := Build(ollamaEnv(home, twoModels, map[string]string{"OLLAMA_MODELS": moved})).Get("ollama-models")
	if u == nil {
		t.Fatal("not registered")
	}
	for _, want := range []string{moved, filepath.Join(home, ".ollama/models"), sys} {
		if !has(u.SizePaths, want) {
			t.Errorf("size paths %v lack %s", u.SizePaths, want)
		}
	}
	if len(u.SizePaths) != 3 || len(u.Paths) != 0 {
		t.Errorf("size paths %v paths %v: want three stores measured, nothing deleted by path", u.SizePaths, u.Paths)
	}
}

func TestParseOllamaList(t *testing.T) {
	out := "NAME                     ID              SIZE      MODIFIED\n" +
		"uposham-lfm2.5:latest    4f2a9c1b7d4e    1.6 GB    3 weeks ago\n" +
		"llama3.2:latest          a80c4f17acd5    2.0 GB    2 months ago\n\n"
	got := parseOllamaList(out)
	if len(got) != 2 || got[0] != (OllamaModel{Name: "uposham-lfm2.5:latest", Size: "1.6 GB"}) ||
		got[1].Name != "llama3.2:latest" {
		t.Fatalf("got %+v", got)
	}
}

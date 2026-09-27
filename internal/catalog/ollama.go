package catalog

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mralaminahamed/reclaim/internal/runner"
	"github.com/mralaminahamed/reclaim/internal/unit"
)

// OllamaModel is one model "ollama list" reports.
type OllamaModel struct {
	Name, Size string
}

// ollamaSystemStores are where packaged services keep models: the install
// script's ollama user, and distribution packages. Variable so tests can aim
// them at a fixture.
var ollamaSystemStores = []string{
	"/usr/share/ollama/.ollama/models",
	"/var/lib/ollama/.ollama/models",
	"/var/lib/ollama/models",
}

// ollamaName is the alphabet of model names. Names reach a shell; anything
// else is dropped rather than quoted.
var ollamaName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)

// ollama offers the models the server lists, removed with "ollama rm" so the
// server keeps its own index -- never by deleting blobs under a live store.
//
// Lossy: a model pulled from a registry comes back, but one built locally
// with "ollama create" does not, and nothing on disk tells them apart. What
// cannot be listed -- no server, no models -- is not offered, since it cannot
// be previewed. The store is found rather than assumed, and measured only.
func (b *builder) ollama() {
	if b.env.Ollama == nil {
		return
	}
	models, err := b.env.Ollama()
	if err != nil {
		return
	}
	var names, detail []string
	for _, m := range models {
		if !ollamaName.MatchString(m.Name) {
			continue
		}
		names = append(names, m.Name)
		detail = append(detail, strings.TrimSpace(m.Name+"  "+m.Size))
	}
	if len(names) == 0 {
		return
	}
	stores := b.ollamaStores()
	hint := b.env.Home
	if len(stores) > 0 {
		hint = stores[0]
	}
	b.r.Add(&unit.Unit{ID: "ollama-models", Tier: unit.TierColdReload, Reversible: false,
		Label: "ollama models", Kind: unit.KindCmd, Flag: "--models",
		Command: "ollama rm " + strings.Join(names, " "), Detail: detail,
		SizePaths: stores, MountHint: hint})
}

func (b *builder) ollamaStores() []string {
	cands := append([]string{b.dir("OLLAMA_MODELS"), filepath.Join(b.env.Home, ".ollama/models")},
		ollamaSystemStores...)
	var out []string
	for _, p := range cands {
		if p == "" || !exists(p) || runner.CheckSafe(p, b.env.Home) != nil {
			continue
		}
		out = append(out, filepath.Clean(p))
	}
	return outermost(out)
}

// ollamaList asks the server what it holds. Bounded: a wedged server must
// not hang every run on a machine that has ollama installed.
func ollamaList() ([]OllamaModel, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ollama", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("ollama list: %w", err)
	}
	return parseOllamaList(string(out)), nil
}

// parseOllamaList reads "NAME ID SIZE MODIFIED" rows. SIZE is two fields,
// "1.6 GB".
func parseOllamaList(out string) []OllamaModel {
	var ms []OllamaModel
	for i, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 2 {
			continue
		}
		m := OllamaModel{Name: f[0]}
		if len(f) >= 4 {
			m.Size = f[2] + " " + f[3]
		}
		ms = append(ms, m)
	}
	return ms
}

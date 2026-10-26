package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

func project(t *testing.T, root, name string, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "package.json")
	if err := os.WriteFile(manifest, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "lib.js"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(manifest, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIdleProjectsRegisterDormantDepsOnly(t *testing.T) {
	root := t.TempDir()
	project(t, root, "dormant", 400*24*time.Hour)
	project(t, root, "active", time.Hour)

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)

	var dormant, active bool
	for _, u := range r.All() {
		for _, p := range u.Paths {
			if filepath.Base(filepath.Dir(p)) == "dormant" {
				dormant = true
			}
			if filepath.Base(filepath.Dir(p)) == "active" {
				active = true
			}
		}
	}
	if !dormant {
		t.Error("dormant project's node_modules was not registered")
	}
	if active {
		t.Error("ACTIVE project's node_modules was registered")
	}
}

func TestIdleProjectDepsAreLossy(t *testing.T) {
	// Reinstalling deps needs the network and a matching lockfile, which may no
	// longer resolve. That is information loss, not a cache miss.
	root := t.TempDir()
	project(t, root, "dormant", 400*24*time.Hour)

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)
	if len(r.All()) == 0 {
		t.Fatal("nothing registered")
	}
	for _, u := range r.All() {
		if u.Reversible {
			t.Errorf("unit %q marked reversible; dep trees must be lossy", u.ID)
		}
		if u.Tier != unit.TierLossy {
			t.Errorf("unit %q tier = %v, want TierLossy", u.ID, u.Tier)
		}
	}
}

func TestIdleProjectsIgnoresDepsWithoutAManifest(t *testing.T) {
	// A bare node_modules with no package.json beside it is not a project; it
	// may be something else entirely, so it is left alone.
	root := t.TempDir()
	orphan := filepath.Join(root, "orphan", "node_modules")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-400 * 24 * time.Hour)
	os.Chtimes(filepath.Join(root, "orphan"), old, old)

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)
	if len(r.All()) != 0 {
		t.Errorf("registered %d units for a manifest-less directory", len(r.All()))
	}
}

func TestIdleProjectsWithZeroDaysDoesNothing(t *testing.T) {
	// The feature is opt-in; without a day threshold it must stay inert.
	root := t.TempDir()
	project(t, root, "dormant", 400*24*time.Hour)
	r := unit.NewRegistry()
	IdleProjects(r, root, 0)
	if len(r.All()) != 0 {
		t.Error("registered units with no idle threshold set")
	}
}

func TestIdleProjectsMissingRootIsNotAnError(t *testing.T) {
	r := unit.NewRegistry()
	IdleProjects(r, filepath.Join(t.TempDir(), "absent"), 30)
	if len(r.All()) != 0 {
		t.Error("registered units for a nonexistent root")
	}
}

// depProject builds a dormant project of its own, outside any repository,
// proving the given dep directory with manifest beside it.
func depProject(t *testing.T, root, name, dep, manifest string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, dep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-400 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, manifest), old, old); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Each of these pairs a dependency/build directory with the manifest that
// proves what produced it, the same claim every existing depDirs row makes.
// A regression here is a real false negative: the project shape goes
// unclaimed even though it is exactly what --sites-idle exists to find.
func TestIdleProjectsCoverModernEcosystems(t *testing.T) {
	cases := []struct {
		name, dep, manifest string
	}{
		{"go-vendor", "vendor", "go.mod"},
		{"deno-vendor", "vendor", "deno.json"},
		{"swiftpm", ".build", "Package.swift"},
		{"bundler", "vendor/bundle", "Gemfile"},
		{"stack", ".stack-work", "stack.yaml"},
		{"cabal", "dist-newstyle", "project.cabal"},
		{"elm", "elm-stuff", "elm.json"},
		{"renv", "renv/library", "renv.lock"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			proj := depProject(t, root, c.name, c.dep, c.manifest)
			r := unit.NewRegistry()
			IdleProjects(r, root, 30)
			var found bool
			for _, u := range r.All() {
				for _, p := range u.Paths {
					if p == filepath.Join(proj, c.dep) {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("%s/%s beside %s was not claimed", c.name, c.dep, c.manifest)
			}
		})
	}
}

// A unit id must never carry a "/" from a multi-component dep dir such as
// "vendor/bundle": --only and --exclude match ids with filepath.Match, whose
// "*" does not cross a path separator, so a slash would make the unit
// unreachable by the normal "idle-*" style globs.
func TestIdleProjectUnitIDsHaveNoSlash(t *testing.T) {
	root := t.TempDir()
	depProject(t, root, "bundler", "vendor/bundle", "Gemfile")
	r := unit.NewRegistry()
	IdleProjects(r, root, 30)
	var found bool
	for _, u := range r.All() {
		if strings.Contains(u.ID, "/") {
			t.Errorf("unit id %q contains a slash", u.ID)
		}
		if ok, _ := filepath.Match("idle-*", u.ID); ok {
			found = true
		}
	}
	if !found {
		t.Error("no unit id matched the idle-* glob --only/--exclude rely on")
	}
}

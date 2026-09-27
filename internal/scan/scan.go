// Package scan finds reclaimable space inside project directories.
//
// Unlike a cache, a dependency tree is not freely regenerable: reinstalling
// needs the network and a lockfile that may no longer resolve to the same
// versions. Everything here is therefore lossy and opt-in.
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// depDirs are dependency and build trees, each paired with the manifests that
// prove the directory really is a project of that kind.
//
// The pairing carries far more weight here than it did for node_modules.
// "build", "target", "obj" and "bin" are ordinary English words, and a
// directory of that name with nothing beside it to explain what produced it is
// somebody's source. No entry may be added without a manifest.
//
// A manifest may be a filepath.Match pattern: there is no fixed name for a
// terraform config or a .NET project file.
//
// "dist" carries no framework's meaning, and plenty of published packages
// commit one, so it is claimed only with proof: inside a git repository that
// ignores it and tracks nothing under it (needsProof). Outside a repository
// there is no way to tell, and it is left alone.
var depDirs = []struct {
	dir        string
	manifests  []string
	needsProof bool
}{
	{dir: "node_modules", manifests: []string{"package.json"}},
	{dir: "vendor", manifests: []string{"composer.json"}},
	{dir: "target", manifests: []string{"Cargo.toml", "pom.xml"}},
	{dir: "build", manifests: []string{"build.gradle", "build.gradle.kts", "pubspec.yaml"}},
	{dir: ".venv", manifests: []string{"pyproject.toml", "requirements.txt", "setup.py"}},
	{dir: "venv", manifests: []string{"pyproject.toml", "requirements.txt", "setup.py"}},
	{dir: ".next", manifests: []string{"package.json"}},
	{dir: ".nuxt", manifests: []string{"package.json"}},
	{dir: ".turbo", manifests: []string{"package.json"}},
	{dir: "_build", manifests: []string{"mix.exs"}},
	{dir: "deps", manifests: []string{"mix.exs"}},
	{dir: ".terraform", manifests: []string{"*.tf"}},
	{dir: "Pods", manifests: []string{"Podfile"}},
	{dir: "obj", manifests: []string{"*.csproj", "*.fsproj", "*.sln"}},
	{dir: "bin", manifests: []string{"*.csproj", "*.fsproj", "*.sln"}},
	{dir: "zig-cache", manifests: []string{"build.zig"}},
	{dir: ".zig-cache", manifests: []string{"build.zig"}},
	{dir: "zig-out", manifests: []string{"build.zig"}},
	{dir: ".dart_tool", manifests: []string{"pubspec.yaml"}},
	{dir: "dist", manifests: []string{"package.json"}, needsProof: true},
}

// artifactDirs is every directory name in depDirs, for the idleness walk to
// skip. Built from the one table so the two cannot drift: a build output
// counted as a source makes a dormant project look busy, and it is then never
// offered.
var artifactDirs = func() map[string]bool {
	m := map[string]bool{".git": true}
	for _, d := range depDirs {
		m[d.dir] = true
	}
	return m
}()

// hasManifest reports whether any of the manifests is present in proj. A name
// containing a glob metacharacter is matched against the directory listing.
func hasManifest(proj string, manifests []string) bool {
	for _, m := range manifests {
		if !strings.ContainsAny(m, "*?[") {
			if _, err := os.Stat(filepath.Join(proj, m)); err == nil {
				return true
			}
			continue
		}
		entries, err := os.ReadDir(proj)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if ok, _ := filepath.Match(m, e.Name()); ok {
				return true
			}
		}
	}
	return false
}

// maxDepth bounds how far below the root projects are looked for. A plugin
// in Sites/<site>/wp-content/plugins/<plugin> is four levels down.
const maxDepth = 6

// IdleProjects registers dependency trees belonging to projects whose sources
// have not been touched for idleDays.
//
// Judging the project's own activity, rather than the timestamp on the
// dependency directory, is what distinguishes a dormant site from one that was
// merely installed a while ago and is still in daily use.
//
// Projects are found at any depth up to maxDepth, so a plugin inside a site is
// its own project. Hidden directories, symlinks and the artifact trees
// themselves are never entered.
func IdleProjects(r *unit.Registry, root string, idleDays int) {
	if idleDays <= 0 || root == "" {
		return
	}
	cutoff := time.Now().Add(-time.Duration(idleDays) * 24 * time.Hour)
	walkProjects(root, "", 0, func(proj, rel string) {
		claimIdle(r, proj, rel, cutoff)
	})
}

// walkProjects calls visit for every directory below dir, to maxDepth, that
// could be a project: not hidden, not a symlink, not an artifact tree.
func walkProjects(dir, rel string, depth int, visit func(proj, rel string)) {
	if depth >= maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || artifactDirs[name] {
			continue
		}
		child, childRel := filepath.Join(dir, name), filepath.Join(rel, name)
		visit(child, childRel)
		walkProjects(child, childRel, depth+1, visit)
	}
}

// claimIdle registers proj's dependency trees if proj is idle.
func claimIdle(r *unit.Registry, proj, rel string, cutoff time.Time) {
	var idle int
	idleKnown, isIdle := false, false
	// Idleness is judged only once a candidate exists: it walks the whole
	// project, and most directories are not projects.
	dormant := func() bool {
		if !idleKnown {
			newest := newestSource(proj)
			isIdle = !newest.IsZero() && !newest.After(cutoff)
			idle, idleKnown = int(time.Since(newest).Hours()/24), true
		}
		return isIdle
	}
	id := strings.ReplaceAll(strings.ReplaceAll(rel, string(filepath.Separator), "-"), " ", "-")
	repo := inRepo(proj)
	claimTagged(r, proj, rel, id, repo, dormant, &idle)
	for _, d := range depDirs {
		dep := filepath.Join(proj, d.dir)
		if !isDir(dep) {
			continue
		}
		// The manifest is what proves this is a project. A bare
		// node_modules with nothing beside it may be something else.
		if !hasManifest(proj, d.manifests) {
			continue
		}
		if holdsIrreplaceable(dep) {
			continue
		}
		// Inside a repository git can say whether the directory is
		// generated: nothing under it tracked, and the directory ignored.
		// A committed vendor/ or build/ is source, whatever its name. If git
		// cannot answer, the directory is left alone.
		if repo && !generated(proj, d.dir) {
			continue
		}
		if !repo && d.needsProof {
			continue
		}
		if !dormant() {
			return
		}
		r.Add(&unit.Unit{
			ID:         "idle-" + d.dir + "-" + id,
			Tier:       unit.TierLossy,
			Reversible: false,
			Label:      rel + "/" + d.dir + " (" + itoa(idle) + "d idle)",
			Kind:       unit.KindPaths,
			Paths:      []string{dep},
			Flag:       "--sites-idle",
		})
	}
}

// cacheDirTag is the signature line a CACHEDIR.TAG must start with
// (https://bford.info/cachedir/).
const cacheDirTag = "Signature: 8a477f597d28d172789f06886806bc55"

// claimTagged registers directories directly inside proj that carry a valid
// CACHEDIR.TAG. The tag is the directory's own statement that it is a
// regenerable cache, so unlike a dependency tree it is a reversible artifact.
// Inside a repository a tracked directory contradicts the tag and is refused.
func claimTagged(r *unit.Registry, proj, rel, id string, repo bool, dormant func() bool, idle *int) {
	entries, err := os.ReadDir(proj)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name == ".git" || artifactDirs[name] {
			continue
		}
		dir := filepath.Join(proj, name)
		data, err := os.ReadFile(filepath.Join(dir, "CACHEDIR.TAG"))
		if err != nil || !strings.HasPrefix(string(data), cacheDirTag) {
			continue
		}
		if repo {
			out, err := runGit(proj, "ls-files", "-z", "--", name)
			if err != nil || len(out) > 0 {
				continue
			}
		}
		if !dormant() {
			return
		}
		r.Add(&unit.Unit{
			ID:         "idle-cachedir-" + id + "-" + strings.ReplaceAll(name, " ", "-"),
			Tier:       unit.TierArtifact,
			Reversible: true,
			Label:      rel + "/" + name + " (cache, " + itoa(*idle) + "d idle)",
			Kind:       unit.KindPaths,
			Paths:      []string{dir},
			Flag:       "--sites-idle",
		})
	}
}

// holdsIrreplaceable reports whether an artifact directory contains something
// that is not an artifact: a repository of its own (a dependency checked out
// for development), or Anchor's program keypairs in target/deploy, which are
// the only copy.
func holdsIrreplaceable(dep string) bool {
	if _, err := os.Lstat(filepath.Join(dep, ".git")); err == nil {
		return true
	}
	keys, _ := filepath.Glob(filepath.Join(dep, "deploy", "*-keypair.json"))
	return len(keys) > 0
}

// inRepo reports whether dir is inside a git working tree, by looking for a
// .git entry (a directory, or a file for worktrees and submodules) in it or
// any parent.
func inRepo(dir string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// generated reports whether git vouches for name, a directory in proj, being
// generated: it tracks nothing under it and ignores it. Any failure to get an
// answer is a no.
func generated(proj, name string) bool {
	out, err := runGit(proj, "ls-files", "-z", "--", name)
	if err != nil || len(out) > 0 {
		return false
	}
	// check-ignore exits 0 when ignored, 1 when not, 128 on error. A tracked
	// file is never reported ignored, which is why ls-files comes first.
	_, err = runGit(proj, "check-ignore", "-q", name+"/")
	return err == nil
}

// newestSource returns the newest modification time among a project's own
// sources, ignoring VCS metadata and the dependency trees themselves.
func newestSource(proj string) time.Time {
	var newest time.Time
	filepath.WalkDir(proj, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr
		}
		if d.IsDir() {
			if artifactDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr
		}
		if fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
		return nil
	})
	return newest
}

func isDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

package scan

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// git runs git in dir with a config that cannot be influenced by the
// developer's: no hooks, no signing, a fixed identity.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
}

// age backdates every file under dir so the project reads as idle. git
// writes files as it goes, so this runs last.
func age(t *testing.T, dir string, d time.Duration) {
	t.Helper()
	when := time.Now().Add(-d)
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			os.Chtimes(p, when, when)
		}
		return nil
	})
}

const old = 400 * 24 * time.Hour

// Plugins sit deep under a site: Sites/<site>/wp-content/plugins/<plugin>.
// Only looking one level below the root found none of them.
func TestScanFindsNestedProjects(t *testing.T) {
	root := t.TempDir()
	plug := filepath.Join(root, "site", "wp-content", "plugins", "plug")
	write(t, filepath.Join(plug, "composer.json"))
	write(t, filepath.Join(plug, "vendor", "autoload.php"))
	age(t, root, old)

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)

	if !registered(r)["idle-vendor-site-wp-content-plugins-plug"] {
		t.Fatalf("nested plugin not found; got %v", registered(r))
	}
}

func TestScanDoesNotEnterHiddenDirectories(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, ".config", "app")
	write(t, filepath.Join(p, "package.json"))
	write(t, filepath.Join(p, "node_modules", "x.js"))
	age(t, root, old)

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)

	if len(r.All()) != 0 {
		t.Fatalf("claimed something under a hidden directory: %v", registered(r))
	}
}

// A WordPress plugin commits vendor/ so it ships working. composer.json beside
// it proves the shape, not that the directory can be regenerated.
func TestScanRefusesCommittedDependencies(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	plug := filepath.Join(root, "plug")
	write(t, filepath.Join(plug, "composer.json"))
	write(t, filepath.Join(plug, "vendor", "autoload.php"))
	git(t, plug, "init", "-q")
	git(t, plug, "add", ".")
	git(t, plug, "commit", "-qm", "ship vendor")
	age(t, root, old)

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)

	if registered(r)["idle-vendor-plug"] {
		t.Fatal("claimed a vendor/ that is committed to the repository")
	}
}

func TestScanClaimsIgnoredDependencies(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	plug := filepath.Join(root, "plug")
	write(t, filepath.Join(plug, "composer.json"))
	if err := os.WriteFile(filepath.Join(plug, ".gitignore"), []byte("/vendor/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, plug, "init", "-q")
	git(t, plug, "add", ".")
	git(t, plug, "commit", "-qm", "init")
	write(t, filepath.Join(plug, "vendor", "autoload.php"))
	age(t, root, old)

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)

	if !registered(r)["idle-vendor-plug"] {
		t.Fatalf("an ignored, untracked vendor/ was not claimed: %v", registered(r))
	}
}

// Untracked and not ignored means nobody told git it is generated. It may be
// work that was never committed.
func TestScanRefusesUntrackedDirectoriesThatAreNotIgnored(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	plug := filepath.Join(root, "plug")
	write(t, filepath.Join(plug, "composer.json"))
	git(t, plug, "init", "-q")
	write(t, filepath.Join(plug, "vendor", "autoload.php"))
	age(t, root, old)

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)

	if registered(r)["idle-vendor-plug"] {
		t.Fatal("claimed an untracked vendor/ that git was never told to ignore")
	}
}

func TestScanFailsClosedWhenGitCannotAnswer(t *testing.T) {
	root := t.TempDir()
	plug := filepath.Join(root, "plug")
	write(t, filepath.Join(plug, "composer.json"))
	write(t, filepath.Join(plug, "vendor", "autoload.php"))
	if err := os.MkdirAll(filepath.Join(plug, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	age(t, root, old)

	real := runGit
	t.Cleanup(func() { runGit = real })
	runGit = func(string, ...string) ([]byte, error) { return nil, errors.New("timed out") }

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)

	if registered(r)["idle-vendor-plug"] {
		t.Fatal("claimed a directory inside a repository git could not vouch for")
	}
}

// dist is claimed only with proof: a published package often commits it.
func TestScanClaimsIgnoredDist(t *testing.T) {
	needGit(t)
	root := t.TempDir()
	lib := filepath.Join(root, "lib")
	write(t, filepath.Join(lib, "package.json"))
	if err := os.WriteFile(filepath.Join(lib, ".gitignore"), []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, lib, "init", "-q")
	git(t, lib, "add", ".")
	git(t, lib, "commit", "-qm", "init")
	write(t, filepath.Join(lib, "dist", "index.js"))
	age(t, root, old)

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)

	if !registered(r)["idle-dist-lib"] {
		t.Fatalf("an ignored dist/ was not claimed: %v", registered(r))
	}
}

func TestScanNeverClaimsADirectoryHoldingARepository(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "app")
	write(t, filepath.Join(p, "package.json"))
	write(t, filepath.Join(p, "node_modules", "forked", ".git", "HEAD"))
	write(t, filepath.Join(p, "node_modules", ".git", "HEAD"))
	age(t, root, old)

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)

	if registered(r)["idle-node_modules-app"] {
		t.Fatal("claimed a directory that is itself a repository")
	}
}

// Anchor keeps program keypairs in target/deploy. They are the only copy.
func TestScanNeverClaimsATargetHoldingKeypairs(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "prog")
	write(t, filepath.Join(p, "Cargo.toml"))
	write(t, filepath.Join(p, "target", "deploy", "prog-keypair.json"))
	age(t, root, old)

	r := unit.NewRegistry()
	IdleProjects(r, root, 30)

	if registered(r)["idle-target-prog"] {
		t.Fatal("claimed a target/ holding deploy keypairs")
	}
}

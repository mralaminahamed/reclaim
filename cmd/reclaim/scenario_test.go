package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mralaminahamed/reclaim/internal/discover"
	th "github.com/mralaminahamed/reclaim/internal/testharness"
)

var update = flag.Bool("update", false, "rewrite golden files")

// realistic is a home shaped like the author's machine: package caches, an
// Electron app's cache next to its settings, dotfiles and a project that must
// survive.
func realistic(s *th.Sandbox) {
	cache, _ := filepath.Rel(s.Root, discover.CacheRoot(s.Home))
	s.Build(
		th.Entry{Path: "home/.npm/_cacache/index", Kind: th.File, Size: 8192},
		th.Entry{Path: filepath.Join(cache, "pip/http/x"), Kind: th.File, Size: 8192},
		th.Entry{Path: filepath.Join(cache, "go-build/aa/b"), Kind: th.File, Size: 8192},
		th.Entry{Path: "home/.bashrc", Kind: th.File, Size: 100, Protected: true},
		th.Entry{Path: "home/.ssh/id_ed25519", Kind: th.File, Size: 400, Mode: 0o600, Protected: true},
		th.Entry{Path: "home/.config/Slack/Local Storage/leveldb/000003.log", Kind: th.File, Size: 4096, Protected: true},
		th.Entry{Path: "home/Projects/app/main.go", Kind: th.File, Size: 300, Protected: true},
		th.Entry{Path: "outside/precious", Kind: th.File, Size: 4096, Protected: true},
	)
}

func TestScenarioDefaultApplyKeepsEverythingProtected(t *testing.T) {
	s := th.New(t, bin)
	realistic(s)

	r := s.Apply("clean", "--apply", "--yes")

	if r.Code != 0 {
		t.Fatalf("exit %d:\n%s", r.Code, r.Out)
	}
	if _, err := os.Stat(filepath.Join(s.Home, ".npm/_cacache")); !os.IsNotExist(err) {
		t.Errorf("npm cache survived:\n%s", r.Out)
	}
}

func TestScenarioReadOnlyModuleCacheIsRemoved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := th.New(t, bin)
	s.Build(
		th.Entry{Path: "home/go/pkg/mod/golang.org/x/net@v0.58.0/quic/stream.go", Kind: th.File, Size: 4096},
		th.Entry{Path: "home/go/pkg/mod/golang.org/x/net@v0.58.0/quic", Kind: th.Dir, Mode: 0o555},
		th.Entry{Path: "home/go/pkg/mod/golang.org/x/net@v0.58.0", Kind: th.Dir, Mode: 0o555},
		th.Entry{Path: "home/.bashrc", Kind: th.File, Size: 10, Protected: true},
	)

	r := s.Apply("clean", "--apply", "--yes", "--only", "go-modcache")

	if r.Code != 0 || strings.Contains(r.Out, "== Failed ==") {
		t.Fatalf("exit %d:\n%s", r.Code, r.Out)
	}
	if _, err := os.Stat(filepath.Join(s.Home, "go/pkg/mod")); !os.IsNotExist(err) {
		t.Errorf("module cache survived:\n%s", r.Out)
	}
}

func TestScenarioLinksOutOfACacheAreNotFollowed(t *testing.T) {
	s := th.New(t, bin)
	cache, _ := filepath.Rel(s.Root, discover.CacheRoot(s.Home))
	s.Build(
		th.Entry{Path: "outside/precious", Kind: th.File, Size: 4096, Protected: true},
		th.Entry{Path: filepath.Join(cache, "pip/http/x"), Kind: th.File, Size: 4096},
		th.Entry{Path: filepath.Join(cache, "pip/escape"), Kind: th.Symlink, Target: "outside"},
		// The npm cache directory itself is a link to data elsewhere: only the
		// link may go. outside/_cacache is there so the test has something
		// to lose -- without it this passed whatever the deleter did.
		th.Entry{Path: "outside/_cacache/precious", Kind: th.File, Size: 4096, Protected: true},
		th.Entry{Path: "home/.npm/_cacache", Kind: th.Symlink, Target: "outside/_cacache"},
	)

	if r := s.Apply("clean", "--apply", "--yes"); r.Code != 0 {
		t.Fatalf("exit %d:\n%s", r.Code, r.Out)
	}
}

func TestScenarioSharedStoreIsNotOverReported(t *testing.T) {
	s := th.New(t, bin)
	s.Build(
		th.Entry{Path: "home/.local/share/pnpm/store/v3/files/ab/pkg", Kind: th.File, Size: 1 << 20},
		th.Entry{Path: "home/Projects/app/package.json", Kind: th.File, Size: 10, Protected: true},
		th.Entry{Path: "home/Projects/app/node_modules/pkg", Kind: th.Hardlink, Target: "home/.local/share/pnpm/store/v3/files/ab/pkg"},
	)

	r := s.Run("clean", "--json", "--only", "pnpm-store")

	var out struct {
		Units []struct {
			ID     string `json:"id"`
			Bytes  int64  `json:"bytes"`
			Shared int64  `json:"shared_bytes"`
		} `json:"units"`
	}
	if err := json.Unmarshal([]byte(r.Out), &out); err != nil {
		t.Fatalf("bad json: %v\n%s", err, r.Out)
	}
	found := false
	for _, u := range out.Units {
		if u.ID != "pnpm-store" {
			continue
		}
		found = true
		if u.Bytes >= 1<<20 {
			t.Errorf("pnpm-store claims %d bytes; its only file is linked into a project", u.Bytes)
		}
		if u.Shared < 1<<20 {
			t.Errorf("pnpm-store shared = %d; the linked file should be reported as shared", u.Shared)
		}
	}
	// Without this the test passes vacuously if the unit stops registering.
	if !found {
		t.Fatalf("pnpm-store was not planned at all:\n%s", r.Out)
	}
}

func TestScenarioNativeCommandsAreStubbedAndDryRunCallsNothing(t *testing.T) {
	s := th.New(t, bin)
	realistic(s)
	for _, tool := range []string{"npm", "go", "pip", "uv"} {
		s.Stub(tool, "")
	}

	s.Run("clean")
	if calls := s.Calls(); calls != nil {
		t.Fatalf("a dry run executed commands: %q", calls)
	}

	s.Apply("clean", "--apply", "--yes")
	calls := strings.Join(s.Calls(), "\n")
	for _, want := range []string{"npm cache clean --force", "go clean"} {
		if !strings.Contains(calls, want) {
			t.Errorf("expected %q among calls:\n%s", want, calls)
		}
	}
}

// The plan for the realistic home, reduced to what should only change on
// purpose: which units land in which section, at which tier. Sizes and mount
// points depend on the filesystem the test runs on and are left out.
func TestScenarioRealisticPlanMatchesGolden(t *testing.T) {
	s := th.New(t, bin)
	realistic(s)

	r := s.Run("clean", "--json")

	type planned struct {
		ID   string `json:"id"`
		Tier int    `json:"tier"`
	}
	var out struct {
		Units    []planned `json:"units"`
		Locked   []planned `json:"locked"`
		Withheld []planned `json:"withheld"`
		OptIn    []planned `json:"opt_in"`
	}
	if err := json.Unmarshal([]byte(r.Out), &out); err != nil {
		t.Fatalf("bad json: %v\n%s", err, r.Out)
	}
	var lines []string
	for _, sec := range []struct {
		name  string
		units []planned
	}{{"units", out.Units}, {"locked", out.Locked}, {"withheld", out.Withheld}, {"opt_in", out.OptIn}} {
		for _, u := range sec.units {
			lines = append(lines, fmt.Sprintf("%s %s tier=%d", sec.name, u.ID, u.Tier))
		}
	}
	sort.Strings(lines)
	got := strings.Join(lines, "\n") + "\n"

	golden := filepath.Join("testdata", "realistic.golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("plan changed; if intended, re-run with -update.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// A sparse file claims its length and occupies almost nothing; the plan must
// be sized by what deleting would give back.
func TestScenarioSparseFileIsSizedByWhatItOccupies(t *testing.T) {
	s := th.New(t, bin)
	cache, _ := filepath.Rel(s.Root, discover.CacheRoot(s.Home))
	s.Build(th.Entry{Path: filepath.Join(cache, "pip/sparse"), Kind: th.Sparse, Size: 256 << 20})

	r := s.Run("clean", "--json", "--only", "pip-cache")

	var out struct {
		Units []struct {
			ID    string `json:"id"`
			Bytes int64  `json:"bytes"`
		} `json:"units"`
	}
	if err := json.Unmarshal([]byte(r.Out), &out); err != nil {
		t.Fatalf("bad json: %v\n%s", err, r.Out)
	}
	if len(out.Units) != 1 || out.Units[0].Bytes >= 1<<20 {
		t.Errorf("units = %+v, want pip-cache sized near zero for a sparse file", out.Units)
	}
}

// A WordPress plugin that commits vendor/ must survive even a run that has
// opted in to lossy idle-project cleanup; one that ignores vendor/ gives it up.
func TestScenarioCommittedVendorSurvivesIdleCleanup(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	s := th.New(t, bin)
	// Real git behind a stub, so the sandbox still logs the call.
	s.Stub("git", `exec `+gitBin+` -c user.email=t@t -c user.name=t -c commit.gpgsign=false -c core.hooksPath=/dev/null "$@"`)
	shipped := filepath.Join("home/Sites/site/wp-content/plugins/shipped")
	ignored := filepath.Join("home/Sites/site/wp-content/plugins/ignored")
	s.Build(
		th.Entry{Path: shipped + "/composer.json", Kind: th.File, Size: 10},
		th.Entry{Path: shipped + "/vendor/autoload.php", Kind: th.File, Size: 4096, Protected: true},
		th.Entry{Path: ignored + "/composer.json", Kind: th.File, Size: 10},
		th.Entry{Path: ignored + "/.gitignore", Kind: th.File, Size: 0},
	)
	os.WriteFile(filepath.Join(s.Root, ignored, ".gitignore"), []byte("/vendor/\n"), 0o644)
	sh := func(dir, script string) {
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Dir, cmd.Env = filepath.Join(s.Root, dir), s.Env()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
	}
	sh(shipped, "git init -q && git add . && git commit -qm ship")
	sh(ignored, "git init -q && git add . && git commit -qm init")
	s.Build(th.Entry{Path: ignored + "/vendor/autoload.php", Kind: th.File, Size: 4096})
	when := time.Now().Add(-400 * 24 * time.Hour)
	filepath.WalkDir(filepath.Join(s.Home, "Sites"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			os.Chtimes(p, when, when)
		}
		return nil
	})

	r := s.Apply("clean", "--apply", "--yes", "--sites-idle", "30", "--allow-lossy", "--only", "idle-*")

	if r.Code != 0 {
		t.Fatalf("exit %d:\n%s", r.Code, r.Out)
	}
	if _, err := os.Stat(filepath.Join(s.Root, ignored, "vendor")); !os.IsNotExist(err) {
		t.Errorf("the ignored vendor/ survived:\n%s", r.Out)
	}
}

// A running Electron app the rule table has never heard of announces itself
// with SingletonLock. Its caches must survive a discovering apply; a stopped
// app's caches go.
func TestScenarioRunningElectronAppKeepsItsCache(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Skip("no hostname")
	}
	s := th.New(t, bin)
	// Where apps keep their data differs by platform: ~/.config on Linux,
	// ~/Library/Application Support on macOS.
	apps, _ := filepath.Rel(s.Root, discover.NestedRoots(s.Home)[0])
	s.Build(
		th.Entry{Path: filepath.Join(apps, "Running/Local State"), Kind: th.File, Size: 10},
		th.Entry{Path: filepath.Join(apps, "Running/Cache/blob"), Kind: th.File, Size: 4096, Protected: true},
		th.Entry{Path: filepath.Join(apps, "Stopped/Local State"), Kind: th.File, Size: 10},
		th.Entry{Path: filepath.Join(apps, "Stopped/Partitions/p/Cache/blob"), Kind: th.File, Size: 4096},
	)
	// Chromium's lock is a symlink whose target is "host-pid", not a path,
	// so it is made directly rather than through Build, which resolves
	// relative targets against the sandbox.
	if err := os.Symlink(fmt.Sprintf("%s-%d", host, os.Getpid()), filepath.Join(s.Root, apps, "Running/SingletonLock")); err != nil {
		t.Fatal(err)
	}

	r := s.Apply("clean", "--apply", "--yes", "--discover")

	if r.Code != 0 {
		t.Fatalf("exit %d:\n%s", r.Code, r.Out)
	}
	if !strings.Contains(r.Out, "Running") || !strings.Contains(r.Out, "Locked") {
		t.Errorf("the running app was not reported as locked:\n%s", r.Out)
	}
	if _, err := os.Stat(filepath.Join(s.Root, apps, "Stopped/Partitions/p/Cache")); !os.IsNotExist(err) {
		t.Errorf("the stopped app's partition cache survived:\n%s", r.Out)
	}
}

// The Hugging Face home holds the login beside the downloads. --models takes
// the downloads, and --discover must not take the directory whole instead.
func TestScenarioModelsKeepsTheHuggingFaceLogin(t *testing.T) {
	s := th.New(t, bin)
	s.Build(
		th.Entry{Path: "home/.cache/huggingface/hub/models--x/blobs/abc", Kind: th.File, Size: 8192},
		th.Entry{Path: "home/.cache/huggingface/xet/chunk", Kind: th.File, Size: 8192},
		th.Entry{Path: "home/.cache/huggingface/token", Kind: th.File, Size: 40, Mode: 0o600, Protected: true},
		th.Entry{Path: "home/.cache/huggingface/stored_tokens", Kind: th.File, Size: 80, Mode: 0o600, Protected: true},
	)

	r := s.Apply("clean", "--models", "--discover", "--apply", "--yes")

	if r.Code != 0 {
		t.Fatalf("exit %d:\n%s", r.Code, r.Out)
	}
	for _, gone := range []string{"hub", "xet"} {
		if _, err := os.Stat(filepath.Join(s.Home, ".cache/huggingface", gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived --models:\n%s", gone, r.Out)
		}
	}
}

// Models go through "ollama rm", naming what the listing showed. Lossy, so
// --models alone lists them and runs nothing; with --allow-lossy they go.
func TestScenarioOllamaModelsGoThroughTheServer(t *testing.T) {
	s := th.New(t, bin)
	s.Stub("ollama", `[ "$1" = list ] && printf 'NAME ID SIZE MODIFIED\nllama3.2:latest a80c4f17acd5 2.0 GB 2 months ago\n'; exit 0`)

	if r := s.Apply("clean", "--models", "--apply", "--yes"); r.Code != 0 {
		t.Fatalf("exit %d:\n%s", r.Code, r.Out)
	}
	for _, c := range s.Calls() {
		if strings.HasPrefix(c, "ollama rm") {
			t.Fatalf("ran %q without --allow-lossy", c)
		}
	}

	r := s.Apply("clean", "--models", "--allow-lossy", "--apply", "--yes")
	if r.Code != 0 {
		t.Fatalf("exit %d:\n%s", r.Code, r.Out)
	}
	calls := strings.Join(s.Calls(), "\n")
	if !strings.Contains(calls, "ollama rm llama3.2:latest") {
		t.Errorf("calls:\n%s\nwant ollama rm of the listed model\n%s", calls, r.Out)
	}
}

// A running flatpak app keeps its cache; the others go by default.
func TestScenarioRunningFlatpakAppKeepsItsCache(t *testing.T) {
	s := th.New(t, bin)
	s.Stub("flatpak", "exit 0")
	s.Build(
		th.Entry{Path: "home/.var/app/org.mozilla.firefox/cache/blob", Kind: th.File, Size: 8192, Protected: true},
		th.Entry{Path: "home/.var/app/org.gimp.GIMP/cache/blob", Kind: th.File, Size: 8192},
		th.Entry{Path: "home/.var/app/org.gimp.GIMP/data/work.xcf", Kind: th.File, Size: 100, Protected: true},
	)
	inst := filepath.Join(s.Tmp, "run", ".flatpak", "4242")
	if err := os.MkdirAll(inst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst, "info"), []byte("[Application]\nname=org.mozilla.firefox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pid := fmt.Sprintf(`{"child-pid": %d}`, os.Getpid())
	if err := os.WriteFile(filepath.Join(inst, "bwrapinfo.json"), []byte(pid), 0o644); err != nil {
		t.Fatal(err)
	}

	r := s.Apply("clean", "--apply", "--yes")

	if r.Code != 0 {
		t.Fatalf("exit %d:\n%s", r.Code, r.Out)
	}
	if _, err := os.Stat(filepath.Join(s.Home, ".var/app/org.gimp.GIMP/cache")); !os.IsNotExist(err) {
		t.Errorf("GIMP's cache survived though GIMP is not running:\n%s", r.Out)
	}
	if !strings.Contains(r.Out, "org.mozilla.firefox") {
		t.Errorf("report does not say firefox's cache was parked:\n%s", r.Out)
	}
}

// Scratch the user left behind goes by default: known compile caches at any
// age, other entries once idle. Something fresh, or holding a live socket,
// stays.
func TestScenarioIdleTmpEntriesGoAndLiveOnesStay(t *testing.T) {
	s := th.New(t, bin)
	tmp, _ := filepath.Rel(s.Root, s.Tmp)
	s.Build(
		th.Entry{Path: filepath.Join(tmp, "old-build/out.o"), Kind: th.File, Size: 8192},
		th.Entry{Path: filepath.Join(tmp, "node-compile-cache/blob"), Kind: th.File, Size: 8192},
	)
	time.Sleep(1500 * time.Millisecond)
	s.Build(th.Entry{Path: filepath.Join(tmp, "in-progress/part"), Kind: th.File, Size: 100, Protected: true})
	sock := filepath.Join(s.Tmp, "tmux-test")
	if err := os.MkdirAll(sock, 0o700); err != nil {
		t.Fatal(err)
	}
	if l, err := net.Listen("unix", filepath.Join(sock, "default")); err == nil {
		defer l.Close()
	}
	s.ExtraEnv = []string{"RECLAIM_TMP_IDLE=1s"}

	r := s.Apply("clean", "--apply", "--yes")

	if r.Code != 0 {
		t.Fatalf("exit %d:\n%s", r.Code, r.Out)
	}
	for _, gone := range []string{"old-build", "node-compile-cache"} {
		if _, err := os.Stat(filepath.Join(s.Tmp, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived:\n%s", gone, r.Out)
		}
	}
	if _, err := os.Stat(sock); err != nil {
		t.Errorf("the socket's directory was taken: %v\n%s", err, r.Out)
	}
}

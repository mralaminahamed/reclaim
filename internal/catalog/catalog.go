// Package catalog is the table of things worth reclaiming.
//
// Everything here is data. A unit is registered only when its path actually
// exists or its tool is actually installed, so the report never lists things
// this machine does not have.
package catalog

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// Env is the machine the catalog is built against. Injecting it keeps the
// catalog testable against a fixture home rather than the real one.
type Env struct {
	Home string
	// Has reports whether a binary is on PATH.
	Has func(bin string) bool
}

// DefaultEnv returns an Env describing this machine.
func DefaultEnv(home string) Env {
	return Env{Home: home, Has: func(bin string) bool {
		_, err := exec.LookPath(bin)
		return err == nil
	}}
}

type builder struct {
	env Env
	r   *unit.Registry
}

// Build assembles the unit registry for this machine.
func Build(env Env) *unit.Registry {
	b := &builder{env: env, r: unit.NewRegistry()}

	// Tier 0: a package manager's own cache-clean command. Safer than any rm we
	// could write, because the tool knows its own layout.
	b.cmd("npm-native", "npm cache clean", "npm", "npm cache clean --force")
	b.cmd("pnpm-native", "pnpm store prune", "pnpm", "pnpm store prune")
	b.cmd("yarn-native", "yarn cache clean", "yarn", "yarn cache clean")
	b.cmd("bun-native", "bun cache rm", "bun", "bun pm cache rm")
	b.cmd("go-native", "go clean -cache", "go", "go clean -cache -modcache")
	b.cmd("composer-native", "composer clear-cache", "composer", "composer clear-cache")
	b.cmd("pip-native", "pip cache purge", "pip", "pip cache purge")
	b.cmd("uv-native", "uv cache clean", "uv", "uv cache clean")
	// --prune=all discards every cached download rather than only those older
	// than the default 120 days. All of them are re-downloadable, which is the
	// whole distinction this tool draws.
	b.cmdAt("brew-native", "brew cleanup", "brew", "brew cleanup --prune=all", unit.TierPkgCache)

	b.paths("thumbnails", "thumbnails", unit.TierNative, true, "", ".cache/thumbnails")
	// The trash is what the user already chose to delete but kept the option
	// of taking back. Emptying it removes that option, so it is lossy and
	// opt-in -- it used to run by default as though it were a cache. File
	// managers recreate the directories on demand.
	b.paths("trash", "trash", unit.TierLossy, false, "--trash",
		".local/share/Trash/files", ".local/share/Trash/info",
		".local/share/Trash/expunged")

	// Tier 1: package-manager cache leftovers, re-downloaded on demand.
	//
	// Several are named twice, once per platform: macOS tools cache under
	// ~/Library/Caches rather than ~/.cache. One unit rather than two, because
	// only the location differs -- the tier and the reversibility are the same
	// claim about the same thing, and b.paths keeps only the paths that exist,
	// so the wrong one never registers.
	b.paths("npm-cacache", "npm _cacache", unit.TierPkgCache, true, "", ".npm/_cacache")
	b.paths("npm-npx", "npm _npx", unit.TierPkgCache, true, "", ".npm/_npx")
	b.paths("npm-logs", "npm _logs", unit.TierPkgCache, true, "", ".npm/_logs")
	b.paths("yarn-classic", "yarn (classic)", unit.TierPkgCache, true, "",
		".cache/yarn", "Library/Caches/Yarn")
	// metadata is the registry metadata berry caches per package; it refetches.
	// index is left alone: it maps the global cache and is small.
	b.paths("yarn-berry", "yarn berry cache", unit.TierPkgCache, true, "",
		".yarn/berry/cache", ".yarn/berry/metadata")
	// Downloaded core, plugin and theme zips. packages/ is left alone: those
	// are installed wp-cli commands, not downloads.
	b.paths("wp-cli-cache", "wp-cli cache", unit.TierPkgCache, true, "", ".wp-cli/cache")
	b.paths("pnpm-cache", "pnpm cache", unit.TierPkgCache, true, "",
		".cache/pnpm", "Library/Caches/pnpm")
	b.paths("pnpm-store", "pnpm store", unit.TierPkgCache, true, "", ".local/share/pnpm/store")
	b.paths("bun-cache", "bun cache", unit.TierPkgCache, true, "", ".bun/install/cache")
	b.paths("node-gyp", "node-gyp headers", unit.TierPkgCache, true, "",
		".cache/node-gyp", "Library/Caches/node-gyp")
	b.paths("go-build", "go build cache", unit.TierPkgCache, true, "",
		".cache/go-build", "Library/Caches/go-build")
	b.paths("go-modcache", "go module cache", unit.TierPkgCache, true, "", "go/pkg/mod")
	b.paths("composer-cache", "composer cache", unit.TierPkgCache, true, "",
		".cache/composer", "Library/Caches/composer")
	b.paths("uv-cache", "uv cache", unit.TierPkgCache, true, "",
		".cache/uv", "Library/Caches/uv")
	b.paths("pip-cache", "pip cache", unit.TierPkgCache, true, "",
		".cache/pip", "Library/Caches/pip")
	b.paths("cargo-cache", "cargo registry", unit.TierPkgCache, true, "",
		".cargo/registry/cache", ".cargo/registry/src")
	b.paths("deno-cache", "deno cache", unit.TierPkgCache, true, "",
		".deno", "Library/Caches/deno")
	b.paths("nuget-cache", "nuget packages", unit.TierPkgCache, true, "", ".nuget/packages")
	b.paths("gem-cache", "gem cache", unit.TierPkgCache, true, "", ".gem")
	b.paths("gradle-daemon", "gradle daemon logs", unit.TierPkgCache, true, "", ".gradle/daemon")
	b.paths("phpactor", "phpactor index", unit.TierPkgCache, true, "", ".cache/phpactor")
	b.paths("devtools-mcp", "chrome-devtools-mcp", unit.TierPkgCache, true, "", ".cache/chrome-devtools-mcp")
	b.paths("act-cache", "act (gh actions)", unit.TierPkgCache, true, "", ".cache/act")
	b.paths("giget", "giget templates", unit.TierPkgCache, true, "", ".cache/giget")

	// Local model storage, which is what changed most about what fills a disk
	// since the rest of this catalog was written. These reach tens to hundreds
	// of gigabytes.
	//
	// Reversible: every file re-downloads. Tier 3 because that download is
	// hours long and often metered, which is the whole distinction the ladder
	// exists to draw -- "comes back" and "comes back for free" are different
	// claims.
	//
	// Pruning is the exception and is deliberately separate. It discards only
	// revisions nothing references and downloads that never finished, so it is
	// cheap, needs no permission, and leaves working models alone. Same reason
	// the catalog prefers "npm cache clean" to deleting the directory: the tool
	// knows which parts are dead and we do not.
	b.cmdAt("hf-prune", "huggingface prune", "hf", "hf cache prune", unit.TierPkgCache)
	b.paths("hf-cache", "huggingface models", unit.TierColdReload, true, "--models",
		".cache/huggingface")
	b.paths("torch-hub", "torch checkpoints", unit.TierColdReload, true, "--models",
		".cache/torch")
	b.paths("whisper-models", "whisper weights", unit.TierColdReload, true, "--models",
		".cache/whisper")
	b.paths("lmstudio-models", "LM Studio models", unit.TierColdReload, true, "--models",
		".lmstudio/models")
	// Chrome's on-device model lives in the profile, not the cache, and runs
	// to several gigabytes. Chrome fetches it again when a feature needs it.
	b.paths("chrome-ai-model", "Chrome on-device AI model", unit.TierColdReload, true, "--models",
		".config/google-chrome/OptGuideOnDeviceModel",
		".config/google-chrome/optimization_guide_model_store")

	// macOS. Registered from this same table rather than a platform file: the
	// paths simply do not exist on Linux, so nothing registers there, and the
	// tier and reversibility decisions -- the part that matters -- are written
	// down once instead of once per platform.
	//
	// ~/Library/Caches is the ~/.cache analogue and the same "regenerable by
	// location" argument applies. ~/Library/Application Support is not: it
	// holds application state, and nothing here may claim it.
	b.paths("homebrew-cache", "homebrew downloads", unit.TierPkgCache, true, "",
		"Library/Caches/Homebrew")
	b.paths("cocoapods-cache", "cocoapods cache", unit.TierPkgCache, true, "",
		"Library/Caches/CocoaPods")
	b.paths("swiftpm-cache", "swift package cache", unit.TierPkgCache, true, "",
		"Library/Caches/org.swift.swiftpm")
	b.paths("xcode-cache", "Xcode cache", unit.TierArtifact, true, "",
		"Library/Caches/com.apple.dt.Xcode")
	b.paths("coresimulator-caches", "simulator caches", unit.TierArtifact, true, "",
		"Library/Developer/CoreSimulator/Caches")

	// Xcode's own state is opt-in for the reason JetBrains is: deleting it
	// under a running IDE breaks the session it is in the middle of, and lock
	// detection has no rule for Xcode yet.
	b.paths("xcode-derived-data", "Xcode derived data", unit.TierColdReload, true, "--xcode",
		"Library/Developer/Xcode/DerivedData")
	b.paths("ios-device-support", "iOS device support", unit.TierColdReload, true, "--xcode",
		"Library/Developer/Xcode/iOS DeviceSupport")

	// An archive is the built, signed artifact of a shipped release. Xcode
	// cannot recreate one from anything left on disk, which is exactly why
	// people keep them.
	b.paths("xcode-archives", "Xcode archives", unit.TierIrreplaceable, false, "--xcode",
		"Library/Developer/Xcode/Archives")

	// A simulator device holds installed apps and their data, not a cache.
	// Wiping it is closer to erasing a phone than to clearing a directory.
	b.paths("coresimulator-devices", "simulator devices", unit.TierLossy, false, "--simulators",
		"Library/Developer/CoreSimulator/Devices")

	// Tier 2: bigger regenerable artefacts.
	b.paths("playwright", "playwright browsers", unit.TierArtifact, true, "--playwright",
		".cache/ms-playwright", "Library/Caches/ms-playwright")

	// Tier 3: costs a reindex or a full cold re-download on next use.
	b.jetbrainsOld()
	b.paths("jetbrains-cache", "JetBrains caches", unit.TierColdReload, true, "--jetbrains",
		".cache/JetBrains", "Library/Caches/JetBrains")
	b.paths("gradle-caches", "gradle caches", unit.TierColdReload, true, "--gradle", ".gradle/caches")
	b.paths("gradle-wrapper", "gradle wrapper", unit.TierColdReload, true, "--gradle", ".gradle/wrapper")
	b.paths("maven-repo", "maven repository", unit.TierColdReload, true, "--maven", ".m2/repository")
	b.paths("chrome-cache", "chrome cache", unit.TierColdReload, true, "--browsers",
		".cache/google-chrome", ".cache/Google")
	b.paths("brave-cache", "brave cache", unit.TierColdReload, true, "--browsers", ".cache/BraveSoftware")
	b.paths("firefox-cache", "firefox cache", unit.TierColdReload, true, "--browsers", ".cache/mozilla")

	// Claude Code scratch state. Jobs and plugin versions are re-fetched or
	// regenerated; transcripts are not, so history is lossy and separate.
	b.paths("claude-jobs", "Claude job scratch", unit.TierArtifact, true, "--claude-jobs",
		".claude/jobs")
	b.paths("claude-plugins", "Claude plugin cache", unit.TierArtifact, true, "--claude-plugins",
		".claude/plugins/cache")
	b.paths("claude-history", "Claude session transcripts", unit.TierLossy, false,
		"--claude-history", ".claude/projects")

	// Tier 5: may destroy the only copy. Never reached by escalation alone.
	b.paths("claude-vm", "Claude VM bundles", unit.TierIrreplaceable, false, "--claude-vm",
		".config/Claude/vm_bundles")

	// Flatpak. Each app pins the runtime version it was built against, so as
	// apps update the old runtimes are left behind unreferenced at 1-2GiB
	// apiece. Nothing removes them by default.
	//
	// Both units are opt-in. The uninstall changes what is installed rather
	// than only what is cached, and the app caches are opt-in for a duller
	// reason: lock detection cannot yet tell which flatpak apps are running, so
	// the flag is standing in for the check that would otherwise park a live
	// app's cache.
	if env.Has != nil && env.Has("flatpak") {
		b.r.Add(&unit.Unit{ID: "flatpak-unused", Tier: unit.TierPkgCache, Reversible: true,
			Label: "unused flatpak runtimes", Kind: unit.KindCmd, Flag: "--flatpak",
			Command: "flatpak uninstall --unused -y", MountHint: "/var/lib/flatpak"})

		// ~/.var/app/<id>/cache is the sandbox's own ~/.cache. Its siblings are
		// not: "data" holds the application's real state and "config" its
		// settings, so the app directory is named a level at a time rather than
		// globbed.
		b.appCaches("flatpak-app-caches", "flatpak app caches", "--flatpak",
			filepath.Join(b.env.Home, ".var", "app"))
	}

	// Docker is usually the single biggest reclaim on a developer machine, but
	// pruning volumes can drop database data, so it stays irreversible.
	if env.Has != nil && env.Has("docker") {
		b.r.Add(&unit.Unit{ID: "docker-prune", Tier: unit.TierIrreplaceable, Reversible: false,
			Label: "docker prune", Kind: unit.KindCmd, Flag: "--docker",
			Command:   "docker builder prune -f; docker container prune -f; docker network prune -f; docker image prune -f",
			MountHint: "/var/lib/docker"})
		b.r.Add(&unit.Unit{ID: "docker-volumes", Tier: unit.TierIrreplaceable, Reversible: false,
			Label: "docker volumes", Kind: unit.KindCmd, Flag: "--docker-volumes",
			Command: "docker volume prune -f", MountHint: "/var/lib/docker"})
	}
	return b.r
}

// cmd registers a command unit when its tool is installed.
func (b *builder) cmd(id, label, bin, command string) {
	b.cmdAt(id, label, bin, command, unit.TierNative)
}

// cmdAt registers a native command at a tier other than free. Most of these
// cost nothing; a few cost a re-download of whatever they discard.
func (b *builder) cmdAt(id, label, bin, command string, tier unit.Tier) {
	if b.env.Has == nil || !b.env.Has(bin) {
		return
	}
	b.r.Add(&unit.Unit{ID: id, Tier: tier, Reversible: true, Label: label,
		Kind: unit.KindCmd, Command: command, MountHint: b.env.Home})
}

// appCaches registers one unit covering the "cache" directory of every app
// under root that has one.
//
// One unit rather than one per app: these are all the same kind of thing and a
// per-app breakdown would bury the rest of the report under however many
// flatpaks happen to be installed.
func (b *builder) appCaches(id, label, flag, root string) {
	apps, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var present []string
	for _, a := range apps {
		if !a.IsDir() {
			continue
		}
		p := filepath.Join(root, a.Name(), "cache")
		if exists(p) {
			present = append(present, p)
		}
	}
	if len(present) == 0 {
		return
	}
	b.r.Add(&unit.Unit{ID: id, Tier: unit.TierPkgCache, Reversible: true, Label: label,
		Kind: unit.KindPaths, Paths: present, Flag: flag, MountHint: b.env.Home})
}

// paths registers a path unit, keeping only the paths that exist here.
func (b *builder) paths(id, label string, tier unit.Tier, reversible bool, flag string, rel ...string) {
	var present []string
	for _, x := range rel {
		p := filepath.Join(b.env.Home, x)
		if exists(p) {
			present = append(present, p)
		}
	}
	if len(present) == 0 {
		return
	}
	b.r.Add(&unit.Unit{ID: id, Tier: tier, Reversible: reversible, Label: label,
		Kind: unit.KindPaths, Paths: present, Flag: flag, MountHint: b.env.Home})
}

// jetbrainsVersion matches a per-version directory: "RustRover2025.3".
var jetbrainsVersion = regexp.MustCompile(`^([A-Za-z]+)(\d{4})\.(\d+)$`)

// jetbrainsOld registers the directories earlier IDE versions left behind.
// Every upgrade starts a new versioned directory and abandons the last one; a
// version is obsolete only when a newer one of the same product sits beside
// it. The newest of each product is the one in use and is never touched.
//
// Data (plugins and indexes in ~/.local/share) re-downloads or rebuilds, so it
// is reversible. Settings are not: an upgrade imports them once, if asked, and
// the old directory is the only copy of anything that was not.
// ~/.cache/JetBrains is not scanned: jetbrains-cache already owns it whole.
func (b *builder) jetbrainsOld() {
	data := oldVersions(filepath.Join(b.env.Home, ".local/share/JetBrains"))
	conf := oldVersions(filepath.Join(b.env.Home, ".config/JetBrains"))
	if len(data) > 0 {
		b.r.Add(&unit.Unit{ID: "jetbrains-old-data", Tier: unit.TierColdReload,
			Reversible: true, Label: "old JetBrains version data", Kind: unit.KindPaths,
			Paths: data, Flag: "--obsolete", MountHint: b.env.Home})
	}
	if len(conf) > 0 {
		b.r.Add(&unit.Unit{ID: "jetbrains-old-config", Tier: unit.TierLossy,
			Reversible: false, Label: "old JetBrains version settings", Kind: unit.KindPaths,
			Paths: conf, Flag: "--obsolete", MountHint: b.env.Home})
	}
}

// oldVersions lists every versioned directory in root that has a newer
// version of the same product beside it.
func oldVersions(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	type ver struct {
		dir         string
		year, minor int
	}
	byProduct := map[string][]ver{}
	for _, e := range entries {
		m := jetbrainsVersion.FindStringSubmatch(e.Name())
		if m == nil || !e.IsDir() {
			continue
		}
		y, _ := strconv.Atoi(m[2])
		n, _ := strconv.Atoi(m[3])
		p := strings.ToLower(m[1])
		byProduct[p] = append(byProduct[p], ver{filepath.Join(root, e.Name()), y, n})
	}
	var out []string
	for _, vs := range byProduct {
		sort.Slice(vs, func(i, j int) bool {
			if vs[i].year != vs[j].year {
				return vs[i].year > vs[j].year
			}
			return vs[i].minor > vs[j].minor
		})
		for _, v := range vs[1:] {
			out = append(out, v.dir)
		}
	}
	sort.Strings(out)
	return out
}

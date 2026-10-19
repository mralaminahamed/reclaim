// Package apps reports installed applications that have not been used in a
// long time.
//
// Like installers, it deletes nothing, and that is the design. Everything the
// cleaner removes comes back by itself; an uninstalled application does not,
// and neither does whatever it kept. So the answer is a report with the command
// that would remove each app, and the decision stays with the user.
//
// The report only ever names an app it has evidence for. Every signal used here
// can be wrong in one direction only -- something other than the user reading
// a binary makes the app look used, never unused -- and an app with no signal
// at all is left out rather than guessed at.
package apps

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Source is where an application came from, which decides how it is removed
// and where its usage evidence lives.
type Source string

const (
	Deb     Source = "deb"
	Snap    Source = "snap"
	Flatpak Source = "flatpak"
	Local   Source = "local"
)

// App is one idle application.
type App struct {
	Name     string
	Source   Source
	Package  string
	Bytes    int64
	LastUsed time.Time
	// Dir is the directory a hand-installed app owns outright, or "".
	Dir string
	// Remove is the command that would uninstall it, for display.
	Remove string
	// Steps are the same removal as argv lists, run in order only by an
	// explicit, confirmed request. Never built from a shell string.
	Steps []Step
}

// Step is one command of a removal.
type Step struct {
	Argv []string
	// Root says the command needs privilege; the caller supplies it.
	Root bool
}

// hint renders steps as the one-line command shown in a report.
func hint(steps []Step) string {
	parts := make([]string, 0, len(steps))
	for _, s := range steps {
		c := strings.Join(s.Argv, " ")
		if s.Root {
			c = "sudo " + c
		}
		parts = append(parts, c)
	}
	return strings.Join(parts, " && ")
}

// Dir is a directory of .desktop entries and the source that owns them.
type Dir struct {
	Path   string
	Source Source
}

// Env describes the machine. Every probe is injected so the selection can be
// tested against machines this one is not.
type Env struct {
	Home string
	Dirs []Dir
	Now  time.Time
	// Owner names the package that installed a file, or "".
	Owner func(path string) string
	// Auto reports whether a package was installed only as a dependency.
	Auto func(pkg string) bool
	// Needed reports whether some other installed package depends on or
	// recommends this one. Removing such a package pulls on the other:
	// removing ibus removes ubuntu-desktop, and the next autoremove takes
	// everything that metapackage was holding.
	Needed func(pkg string) bool
	// PkgBytes is a package's installed size.
	PkgBytes func(src Source, pkg string) int64
	// Used returns when a file was last read, and false when the filesystem
	// does not record it (noatime) -- an absent answer, not an old one.
	Used func(path string) (time.Time, bool)
	// DirBytes measures a directory.
	DirBytes func(path string) int64
	// LookPath resolves a bare command name.
	LookPath func(name string) (string, error)
	// ShellSeen is the desktop shell's own record of when each app was last
	// seen running, keyed by desktop file name.
	ShellSeen map[string]time.Time
}

// entry is the part of a .desktop file that matters here.
type entry struct {
	file   string
	name   string
	exec   string
	hidden bool
}

// Result is what Find reports.
type Result struct {
	// Apps are idle past the bound, largest first.
	Apps []App
	// Unknown were passed over for want of any usage evidence.
	Unknown int
	// Needed were idle but something else installed depends on them.
	Needed int
}

// Find returns the applications idle for at least idle.
func Find(env Env, idle time.Duration) Result {
	var res Result
	cutoff := env.Now.Add(-idle)
	type acc struct {
		app  App
		seen bool
	}
	byKey := map[string]*acc{}
	var order []string

	for _, d := range env.Dirs {
		files, _ := filepath.Glob(filepath.Join(d.Path, "*.desktop"))
		for _, f := range files {
			e, ok := readDesktop(f)
			if !ok || e.hidden {
				continue
			}
			app, key, ok := identify(env, d.Source, e)
			if !ok {
				continue
			}
			last, seen := evidence(env, d.Source, app.Package, e)
			a, exists := byKey[key]
			if !exists {
				a = &acc{app: app}
				byKey[key] = a
				order = append(order, key)
			}
			// One package, several launchers: it is as used as its most
			// used one.
			if seen {
				a.seen = true
				if last.After(a.app.LastUsed) {
					a.app.LastUsed = last
				}
			}
		}
	}

	for _, k := range order {
		a := byKey[k]
		if !a.seen {
			res.Unknown++
			continue
		}
		if a.app.LastUsed.After(cutoff) {
			continue
		}
		// Asked only of idle candidates: it shells out.
		if a.app.Source == Deb && env.Needed != nil && env.Needed(a.app.Package) {
			res.Needed++
			continue
		}
		if env.PkgBytes != nil && a.app.Bytes == 0 {
			a.app.Bytes = env.PkgBytes(a.app.Source, a.app.Package)
		}
		res.Apps = append(res.Apps, a.app)
	}
	sort.SliceStable(res.Apps, func(i, j int) bool { return res.Apps[i].Bytes > res.Apps[j].Bytes })
	return res
}

// identify works out which package a launcher belongs to and how it would be
// removed. A deb launcher installed as a dependency is not reported: it was
// never the user's choice, and removing it would pull on whatever needs it.
func identify(env Env, src Source, e entry) (App, string, bool) {
	base := strings.TrimSuffix(filepath.Base(e.file), ".desktop")
	app := App{Name: e.name, Source: src}
	switch src {
	case Deb:
		if env.Owner == nil {
			return app, "", false
		}
		pkg := env.Owner(e.file)
		if pkg == "" || (env.Auto != nil && env.Auto(pkg)) {
			return app, "", false
		}
		app.Package = pkg
		app.Steps = []Step{{Argv: []string{"apt", "remove", pkg}, Root: true}}
	case Snap:
		// snapd names launchers "<snap>_<app>.desktop".
		name, _, _ := strings.Cut(base, "_")
		app.Package = name
		app.Steps = []Step{{Argv: []string{"snap", "remove", name}, Root: true}}
	case Flatpak:
		app.Package = base
		app.Steps = []Step{{Argv: []string{"flatpak", "uninstall", base}}}
	case Local:
		// A hand-installed app is a launcher plus whatever it starts. Under
		// /opt or /usr/local/<name> that is the app's own directory; anywhere
		// else only the launcher is named, since its target may be shared.
		app.Package = e.file
		app.Steps = []Step{{Argv: []string{"rm", e.file}}}
		// /usr/bin/Postman -> /opt/Postman/Postman: the link is not the app.
		if dir := OwnedDir(follow(resolve(env, e.exec))); dir != "" {
			app.Dir = dir
			app.Steps = []Step{
				{Argv: []string{"rm", "-rf", dir}, Root: true},
				{Argv: []string{"rm", e.file}},
			}
			if env.DirBytes != nil {
				app.Bytes = env.DirBytes(dir)
			}
		}
	}
	app.Remove = hint(app.Steps)
	return app, string(src) + ":" + app.Package, true
}

// evidence is the latest sign of an app being used, and whether there was any.
func evidence(env Env, src Source, pkg string, e entry) (time.Time, bool) {
	var last time.Time
	seen := false
	note := func(t time.Time) {
		seen = true
		if t.After(last) {
			last = t
		}
	}
	if t, ok := env.ShellSeen[filepath.Base(e.file)]; ok {
		note(t)
	}
	switch src {
	case Snap:
		if t, ok := newest(filepath.Join(env.Home, "snap", pkg), 2); ok {
			note(t)
		}
	case Flatpak:
		if t, ok := newest(filepath.Join(env.Home, ".var", "app", pkg), 2); ok {
			note(t)
		}
	default:
		if bin := resolve(env, e.exec); bin != "" && env.Used != nil {
			if t, ok := env.Used(bin); ok {
				note(t)
			}
		}
	}
	return last, seen
}

// resolve finds the binary a desktop Exec line starts. "env FOO=1 app %U"
// starts app.
func resolve(env Env, execLine string) string {
	for _, f := range strings.Fields(execLine) {
		f = strings.Trim(f, `"'`)
		if f == "env" || strings.Contains(f, "=") {
			continue
		}
		if filepath.IsAbs(f) {
			return f
		}
		if env.LookPath == nil {
			return ""
		}
		p, err := env.LookPath(f)
		if err != nil {
			return ""
		}
		return p
	}
	return ""
}

// newest is the latest modification time at most depth levels into dir. An
// app's own data directory changes when it runs; a missing one is no evidence.
func newest(dir string, depth int) (time.Time, bool) {
	fi, err := os.Stat(dir)
	if err != nil {
		return time.Time{}, false
	}
	last := fi.ModTime()
	if depth > 0 && fi.IsDir() {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if t, ok := newest(filepath.Join(dir, e.Name()), depth-1); ok && t.After(last) {
				last = t
			}
		}
	}
	return last, true
}

// readDesktop reads the [Desktop Entry] group of a .desktop file.
func readDesktop(path string) (entry, bool) {
	f, err := os.Open(path)
	if err != nil {
		return entry{}, false
	}
	defer f.Close()
	e := entry{file: path}
	isApp := false
	inMain := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inMain = line == "[Desktop Entry]"
			continue
		}
		if !inMain {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Type":
			isApp = strings.TrimSpace(v) == "Application"
		case "Name":
			e.name = strings.TrimSpace(v)
		case "Exec":
			e.exec = strings.TrimSpace(v)
		case "NoDisplay", "Hidden":
			if strings.TrimSpace(v) == "true" {
				e.hidden = true
			}
		}
	}
	return e, isApp && e.exec != ""
}

// follow resolves a chain of symlinks by reading them, so it answers even when
// the final target is missing or unreadable.
func follow(p string) string {
	for i := 0; i < 16 && p != ""; i++ {
		t, err := os.Readlink(p)
		if err != nil {
			return p
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(p), t)
		}
		p = t
	}
	return p
}

// sharedLocal are the /usr/local directories every local tool shares. A binary
// directly inside one belongs to no app of its own.
var sharedLocal = map[string]bool{
	"bin": true, "sbin": true, "lib": true, "lib64": true, "libexec": true,
	"share": true, "etc": true, "include": true, "src": true, "man": true,
	"games": true, "opt": true,
}

// OwnedDir is the top directory of a hand-installed app that bin lives in:
// /opt/<name> or /usr/local/<name>. It is "" for anything else, including a
// binary that merely sits in a shared directory.
func OwnedDir(bin string) string {
	if !filepath.IsAbs(bin) {
		return ""
	}
	for _, root := range []string{"/opt/", "/usr/local/"} {
		rest, ok := strings.CutPrefix(bin, root)
		if !ok {
			continue
		}
		top, tail, _ := strings.Cut(rest, "/")
		// A file straight in the root has no directory of its own.
		if top == "" || tail == "" || top == "." || top == ".." {
			return ""
		}
		if root == "/usr/local/" && sharedLocal[top] {
			return ""
		}
		return root + top
	}
	return ""
}

// Leftover is data an app kept in the user's home. Reported, never removed.
type Leftover struct {
	Path  string
	Bytes int64
}

// Leftovers names the places an app commonly keeps its own data, when they
// exist. Only an exact (case-insensitive) match on the app's name, package or
// install directory counts, so one app is never blamed for another's data.
func Leftovers(env Env, a App) []Leftover {
	seen := map[string]bool{}
	var cands []string
	add := func(c string) {
		c = strings.TrimSpace(c)
		if c == "" || c == "." || c == ".." || strings.ContainsAny(c, "/\\") || seen[strings.ToLower(c)] {
			return
		}
		seen[strings.ToLower(c)] = true
		cands = append(cands, c)
	}
	add(a.Name)
	add(strings.ReplaceAll(a.Name, " ", ""))
	if a.Source != Local {
		add(a.Package)
	}
	if a.Dir != "" {
		add(filepath.Base(a.Dir))
	}

	roots := []string{".config", ".cache", ".local/share", ".var/app", "snap"}
	var out []Leftover
	for _, root := range roots {
		entries, err := os.ReadDir(filepath.Join(env.Home, root))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if matches(cands, e.Name()) {
				out = append(out, leftover(env, filepath.Join(env.Home, root, e.Name())))
			}
		}
	}
	// Versioned data lives under the vendor: Google/AndroidStudio2026.1.3. Only
	// "<name><digit>..." counts, so AndroidStudioPreview is not claimed.
	for _, root := range []string{".config", ".cache", ".local/share"} {
		for _, vendor := range []string{"Google", "JetBrains"} {
			entries, err := os.ReadDir(filepath.Join(env.Home, root, vendor))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if versioned(cands, e.Name()) {
					out = append(out, leftover(env, filepath.Join(env.Home, root, vendor, e.Name())))
				}
			}
		}
	}
	entries, _ := os.ReadDir(env.Home)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && matches(cands, e.Name()[1:]) {
			out = append(out, leftover(env, filepath.Join(env.Home, e.Name())))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// versioned reports whether name is a candidate followed by a version number.
func versioned(cands []string, name string) bool {
	for _, c := range cands {
		if len(name) > len(c) && strings.EqualFold(name[:len(c)], c) &&
			name[len(c)] >= '0' && name[len(c)] <= '9' {
			return true
		}
	}
	return false
}

func matches(cands []string, name string) bool {
	for _, c := range cands {
		if strings.EqualFold(c, name) {
			return true
		}
	}
	return false
}

func leftover(env Env, path string) Leftover {
	l := Leftover{Path: path}
	if env.DirBytes != nil {
		l.Bytes = env.DirBytes(path)
	}
	return l
}

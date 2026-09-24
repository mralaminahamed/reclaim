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
	// Remove is the command that would uninstall it. Shown, never run.
	Remove string
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
		app.Remove = "sudo apt remove " + pkg
	case Snap:
		// snapd names launchers "<snap>_<app>.desktop".
		name, _, _ := strings.Cut(base, "_")
		app.Package = name
		app.Remove = "sudo snap remove " + name
	case Flatpak:
		app.Package = base
		app.Remove = "flatpak uninstall " + base
	case Local:
		// A hand-installed app is a launcher plus whatever it starts. Under
		// /opt that is the app's own directory; anywhere else only the
		// launcher is named, since its target may be shared.
		app.Package = e.file
		app.Remove = "rm " + e.file
		bin := resolve(env, e.exec)
		// /usr/bin/Postman -> /opt/Postman/Postman: the link is not the app.
		bin = follow(bin)
		if strings.HasPrefix(bin, "/opt/") {
			top := strings.SplitN(strings.TrimPrefix(bin, "/opt/"), "/", 2)[0]
			if top != "" {
				dir := "/opt/" + top
				app.Remove = "sudo rm -rf " + dir + " && rm " + e.file
				if env.DirBytes != nil {
					app.Bytes = env.DirBytes(dir)
				}
			}
		}
	}
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

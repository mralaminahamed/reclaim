package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mralaminahamed/reclaim/internal/discover"
	"github.com/mralaminahamed/reclaim/internal/fsutil"
	"github.com/mralaminahamed/reclaim/internal/index"
)

// indexFresh is how old an index may be before analyze stops trusting it and
// walks instead. A day is long enough that a nightly refresh keeps analyze
// instant, and short enough that the numbers are about today's disk.
const indexFresh = 24 * time.Hour

func cmdIndex(args []string) int {
	sub := "build"
	if len(args) > 0 && (args[0] == "show" || args[0] == "cold" || args[0] == "build") {
		sub, args = args[0], args[1:]
	}
	// Under sudo the index still belongs to the user who asked: root can read
	// every directory, but analyze runs as the user and looks in their home.
	who := invokingOwner()
	home := who.home
	path := index.DefaultPath(home)
	switch sub {
	case "show":
		return indexShow(path, home, args)
	case "cold":
		return indexCold(path, home, args)
	}
	return indexBuild(path, who, args)
}

func indexBuild(path string, who owner, args []string) int {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	full := fs.Bool("full", false, "re-read every directory instead of only changed ones")
	workers := fs.Int("workers", runtime.NumCPU()*2, "parallel walkers")
	var roots multiFlag
	fs.Var(&roots, "root", "index only this directory (repeatable; default: every filesystem)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(roots) == 0 {
		roots = defaultRoots()
	}

	x, err := index.Load(path)
	if errors.Is(err, index.ErrNone) {
		x, err = &index.Index{}, nil
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, r := range roots {
		r, _ = filepath.Abs(r)
		opts := index.Options{Workers: *workers, Skip: otherDevice(r)}
		if prev := x.Tree(r); prev != nil && prev.Root == r && !*full {
			opts.Prev = prev
		}
		start := time.Now()
		t, err := index.Build(r, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", r, err)
			continue
		}
		x.Put(t)
		mode := "full"
		if opts.Prev != nil {
			mode = "refresh"
		}
		line := fmt.Sprintf("  %-30s %10s  %7d dirs  %s in %s", r, fsutil.Human(t.Total(0)),
			len(t.Dirs), mode, time.Since(start).Round(time.Millisecond))
		if t.Unreadable > 0 {
			line += fmt.Sprintf("  (%d unreadable, run as root to include them)", t.Unreadable)
		}
		fmt.Println(line)
	}
	if err := x.Save(path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := who.handBack(path); err != nil {
		fmt.Fprintln(os.Stderr, "index written but not handed back:", err)
		return 1
	}
	fmt.Println("index written to", path)
	return 0
}

// defaultRoots is every real filesystem. Each is indexed as its own tree and
// never crossed into from another, so a directory is counted once.
func defaultRoots() []string {
	ms, err := fsutil.Mounts()
	if err != nil || len(ms) == 0 {
		home, _ := os.UserHomeDir()
		return []string{home}
	}
	var out []string
	for _, m := range ms {
		out = append(out, m.Path)
	}
	return out
}

// otherDevice skips directories on a different filesystem from root: another
// mount is indexed as its own tree, and /proc and friends not at all.
func otherDevice(root string) func(string, fs.FileInfo) bool {
	dev, ok := deviceOf(root)
	if !ok {
		return nil
	}
	return func(_ string, fi fs.FileInfo) bool {
		d, ok := deviceOfInfo(fi)
		return ok && d != dev
	}
}

func loadFor(path, target string) (*index.Tree, int) {
	x, err := index.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "no index yet; run: reclaim index")
		return nil, 1
	}
	t := x.Tree(target)
	if t == nil {
		fmt.Fprintf(os.Stderr, "%s is not indexed; run: reclaim index --root %s\n", target, target)
		return nil, 1
	}
	fmt.Printf("index of %s, built %s ago\n", t.Root, time.Since(t.Built).Round(time.Minute))
	return t, 0
}

func indexShow(path, home string, args []string) int {
	fs := flag.NewFlagSet("index show", flag.ContinueOnError)
	n := fs.Int("n", 20, "how many entries")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	target := home
	if fs.NArg() > 0 {
		target, _ = filepath.Abs(fs.Arg(0))
	}
	t, code := loadFor(path, target)
	if t == nil {
		return code
	}
	kids, ok := t.Children(target)
	if !ok {
		fmt.Fprintf(os.Stderr, "%s is not in the index\n", target)
		return 1
	}
	for i, k := range kids {
		if i == *n || k.Bytes == 0 {
			break
		}
		fmt.Printf("  %-60s %10s\n", k.Path, fsutil.Human(k.Bytes))
	}
	return 0
}

func indexCold(path, home string, args []string) int {
	fs := flag.NewFlagSet("index cold", flag.ContinueOnError)
	days := fs.Int("days", 365, "untouched for at least this many days")
	min := fs.String("min", "1G", "only report directories at least this large")
	n := fs.Int("n", 20, "how many entries")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	size, err := fsutil.ParseSize(*min)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	target := home
	if fs.NArg() > 0 {
		target, _ = filepath.Abs(fs.Arg(0))
	}
	t, code := loadFor(path, target)
	if t == nil {
		return code
	}
	// Cold is a fact about modification times, not a verdict. An archive is
	// supposed to be untouched; so is a photo library.
	fmt.Printf("untouched for %d+ days (advisory, never deleted):\n", *days)
	cold := t.Cold(time.Now().Add(-time.Duration(*days)*24*time.Hour), size)
	shown := 0
	for _, c := range cold {
		if !within(c.Path, target) {
			continue
		}
		if shown == *n {
			break
		}
		shown++
		fmt.Printf("  %-60s %10s  newest %s\n", c.Path, fsutil.Human(c.Bytes),
			c.Newest.Format("2006-01-02"))
	}
	if shown == 0 {
		fmt.Println("  none")
	}
	return 0
}

// heavyFromIndex answers analyze's question from the index, if one covering
// home is fresh enough to trust.
func heavyFromIndex(home string, min int64) ([]discover.Heavy, time.Duration, bool) {
	x, err := index.Load(index.DefaultPath(home))
	if err != nil {
		return nil, 0, false
	}
	t := x.Tree(home)
	if t == nil {
		return nil, 0, false
	}
	age := time.Since(t.Built)
	if age > indexFresh {
		return nil, 0, false
	}
	kids, ok := t.Children(home)
	if !ok {
		return nil, 0, false
	}
	var out []discover.Heavy
	for _, k := range kids {
		if k.Bytes >= min {
			out = append(out, discover.Heavy{Path: k.Path, Bytes: k.Bytes})
		}
	}
	return out, age, true
}

func within(p, root string) bool {
	return p == root || root == "/" || len(p) > len(root) && p[:len(root)] == root && p[len(root)] == '/'
}

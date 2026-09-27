package testharness

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

type Kind int

const (
	File Kind = iota
	Dir
	Symlink
	Hardlink
	// Sparse is a file of Size bytes with no blocks allocated.
	Sparse
)

// Entry is one thing in a fixture. Path, and Target unless absolute, are
// relative to the sandbox Root, so a fixture can place things in home/ or in
// outside/.
type Entry struct {
	Path      string
	Kind      Kind
	Size      int
	Mode      fs.FileMode
	Target    string
	Protected bool
}

// Build materialises entries in order. Modes are applied after everything is
// made, deepest first, so a directory that ends up read-only can still be
// filled.
func (s *Sandbox) Build(entries ...Entry) {
	s.t.Helper()
	for _, e := range entries {
		p := filepath.Join(s.Root, e.Path)
		s.must(os.MkdirAll(filepath.Dir(p), 0o755))
		switch e.Kind {
		case File:
			s.must(os.WriteFile(p, bytes.Repeat([]byte{'x'}, e.Size), 0o644))
		case Dir:
			s.must(os.MkdirAll(p, 0o755))
		case Symlink:
			s.must(os.Symlink(s.resolve(e.Target), p))
		case Hardlink:
			s.must(os.Link(s.resolve(e.Target), p))
		case Sparse:
			f, err := os.Create(p)
			s.must(err)
			s.must(f.Truncate(int64(e.Size)))
			s.must(f.Close())
		}
		if e.Protected {
			s.protected = append(s.protected, p)
		}
	}
	for _, e := range slices.Backward(entries) {
		if e.Mode == 0 {
			continue
		}
		p := filepath.Join(s.Root, e.Path)
		s.must(os.Chmod(p, e.Mode))
		if e.Kind == Dir {
			// t.TempDir cannot clean up a read-only directory.
			s.t.Cleanup(func() { os.Chmod(p, 0o755) })
		}
	}
}

func (s *Sandbox) resolve(target string) string {
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(s.Root, target)
}

func (s *Sandbox) must(err error) {
	s.t.Helper()
	if err != nil {
		s.t.Fatal(err)
	}
}

// snapshot records mode and content hash for every protected entry, and for
// everything under a protected directory. Absence is recorded too: a
// protected file that disappears is the failure this exists to catch.
func (s *Sandbox) snapshot() map[string]string {
	out := map[string]string{}
	for _, root := range s.protected {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				out[p] = "absent"
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				out[p] = "unreadable"
				return nil
			}
			sum := ""
			if fi.Mode().IsRegular() {
				data, err := os.ReadFile(p)
				if err == nil {
					sum = fmt.Sprintf("%x", sha256.Sum256(data))
				}
			}
			out[p] = fmt.Sprintf("%v %s", fi.Mode(), sum)
			return nil
		})
	}
	return out
}

// Apply runs a deleting command and fails the test if anything protected
// changed. Every apply scenario goes through here, so the check cannot be
// forgotten.
func (s *Sandbox) Apply(args ...string) Result {
	s.t.Helper()
	if !slices.Contains(args, "--apply") {
		s.t.Fatalf("Apply without --apply: %v", args)
	}
	before := s.snapshot()
	r := s.Run(args...)
	after := s.snapshot()
	for p, was := range before {
		if now, ok := after[p]; !ok || now != was {
			s.t.Errorf("protected %s changed: %q -> %q\n%s", p, was, now, r.Out)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			s.t.Errorf("protected tree gained %s\n%s", p, r.Out)
		}
	}
	return r
}

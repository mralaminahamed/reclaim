package system

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// Residue is a package dpkg still lists as "rc": removed, configuration kept.
type Residue struct {
	Package string
	// Files are the conffiles it left behind.
	Files []string
}

// addObsolete registers what outlived the thing that needed it: module
// directories of purged kernels, configuration of removed packages, and
// rotated logs past any reasonable investigation.
func addObsolete(r *unit.Registry, env Env) {
	var res []Residue
	if env.Residual != nil {
		res = env.Residual()
	}
	if env.KernelPackages != nil && env.RunningKernel != nil && env.ModulesRoot != "" {
		addKernelResidue(r, env, res)
	}
	addResidualConfig(r, res)
	if env.LogDir != "" {
		addOldLogs(r, env.LogDir)
	}
}

// addKernelResidue takes what a removed kernel leaves after dpkg is done with
// it. The modules package's own removal script reruns depmod, which rewrites
// the module indexes into /lib/modules/<version> -- so every removed kernel
// leaves a directory of indexes for modules that no longer exist, anywhere
// from one to several megabytes, and a dpkg entry in the "rc" state.
//
// A version qualifies only when nothing still claims it: no installed package
// of that version, not the running kernel, and no image in /boot. That last
// check matters for a kernel installed outside dpkg, which has a module
// directory and an image and no package at all; a stale rc entry sharing its
// version must not be enough to take it.
func addKernelResidue(r *unit.Registry, env Env, res []Residue) {
	running := env.RunningKernel()
	var claimed []string
	for _, p := range env.KernelPackages() {
		if m := kernelPkg.FindStringSubmatch(p); m != nil {
			claimed = append(claimed, m[1])
		}
	}
	claimed = append(claimed, running)

	var pkgs, dirs []string
	seen := map[string]bool{}
	for _, rc := range res {
		m := kernelPkg.FindStringSubmatch(rc.Package)
		if m == nil {
			continue
		}
		v := m[1]
		if related(v, claimed) || exists(filepath.Join(env.BootDir, "vmlinuz-"+v)) {
			continue
		}
		pkgs = append(pkgs, rc.Package)
		dir := filepath.Join(env.ModulesRoot, v)
		if !seen[dir] && isDir(dir) {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	if len(pkgs) == 0 {
		return
	}
	sort.Strings(pkgs)
	sort.Strings(dirs)

	var steps []string
	if len(dirs) > 0 {
		steps = append(steps, "sudo rm -rf -- "+quoteAll(dirs))
	}
	steps = append(steps, "sudo dpkg --purge "+strings.Join(pkgs, " "))
	r.Add(&unit.Unit{
		ID: "system-kernel-residue", Tier: unit.TierPkgCache, Reversible: true,
		Label: "leftovers of removed kernels", Kind: unit.KindCmd, Flag: "--kernels",
		MountHint: "/", NeedsRoot: true,
		Command:   strings.Join(steps, " && "),
		Detail:    append(append([]string{}, pkgs...), dirs...),
		SizePaths: dirs,
	})
}

// related reports whether version v belongs to any claimed kernel, in either
// direction: "7.0.0-30" is the headers half of "7.0.0-30-generic".
func related(v string, claimed []string) bool {
	for _, c := range claimed {
		if sameKernel(v, c) || sameKernel(c, v) {
			return true
		}
	}
	return false
}

// dpkgName is dpkg's package name grammar, with an optional architecture
// qualifier. Names reach a shell, so anything else is dropped, not quoted.
var dpkgName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+(:[a-z0-9-]+)?$`)

// addResidualConfig purges packages removed without --purge. What remains of
// them is configuration in /etc, and configuration is the one thing a package
// cannot give back: a reinstall restores the shipped defaults, not the edits.
// So this is lossy, behind its own flag and --allow-lossy both.
func addResidualConfig(r *unit.Registry, res []Residue) {
	var pkgs, files []string
	for _, rc := range res {
		if kernelPkg.MatchString(rc.Package) || !dpkgName.MatchString(rc.Package) {
			continue
		}
		pkgs = append(pkgs, rc.Package)
		files = append(files, rc.Files...)
	}
	if len(pkgs) == 0 {
		return
	}
	sort.Strings(pkgs)
	r.Add(&unit.Unit{
		ID: "system-residual-config", Tier: unit.TierLossy, Reversible: false,
		Label: "config left by removed packages", Kind: unit.KindCmd, Flag: "--obsolete",
		MountHint: "/", NeedsRoot: true,
		Command:   "sudo dpkg --purge " + strings.Join(pkgs, " "),
		Detail:    pkgs,
		SizePaths: files,
	})
}

// logAge is how old a rotated log must be. Past a month a rotated log is
// rarely the one anyone is reading, but it is still a record: the unit stays
// lossy regardless.
const logAge = 30 * 24 * time.Hour

// rotated matches the names logrotate and friends give a log they have moved
// aside: numbered (syslog.1, syslog.2.gz), dated (dpkg.log-20250101.xz),
// compressed, or ".old". A live log never carries any of these.
var rotated = regexp.MustCompile(
	`(\.\d+(\.(gz|xz|bz2|zst|lz4))?|\.(gz|xz|bz2|zst|lz4)|\.old|-\d{8}(\.(gz|xz|bz2|zst|lz4))?)$`)

// addOldLogs takes rotated logs older than logAge. The command names every
// file the dry run measured rather than repeating the search, so what is
// deleted is exactly what was shown -- a find run later would see whatever
// logrotate has done in between.
func addOldLogs(r *unit.Registry, dir string) {
	cutoff := time.Now().Add(-logAge)
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// The journal has its own bounded unit.
			if p != dir && d.Name() == "journal" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !rotated.MatchString(d.Name()) {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().Before(cutoff) {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		return
	}
	r.Add(&unit.Unit{
		ID: "system-old-logs", Tier: unit.TierLossy, Reversible: false,
		Label: "rotated logs older than 30 days", Kind: unit.KindCmd, Flag: "--obsolete",
		MountHint: "/var", NeedsRoot: true,
		Command:   "sudo rm -f -- " + quoteAll(files),
		SizePaths: files,
	})
}

// quoteAll single-quotes each path for sh.
func quoteAll(paths []string) string {
	q := make([]string, len(paths))
	for i, p := range paths {
		q[i] = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func isDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

// residualPackages lists every package dpkg holds in the "rc" state, with the
// conffiles it left.
func residualPackages() []Residue {
	out, err := exec.Command("dpkg-query", "-W",
		"-f", "${db:Status-Abbrev}|${Package}|${Conffiles}\n").Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	return parseResidue(string(out))
}

// parseResidue reads dpkg-query output. Conffiles span lines: the first shares
// the package's line, the rest follow indented, each "path md5 [obsolete]".
func parseResidue(out string) []Residue {
	var res []Residue
	// An index, not a pointer: appending to res may move it.
	cur := -1
	conf := func(s string) {
		if cur < 0 {
			return
		}
		if f := strings.Fields(s); len(f) > 0 && strings.HasPrefix(f[0], "/") {
			res[cur].Files = append(res[cur].Files, f[0])
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, " ") {
			conf(line)
			continue
		}
		cur = -1
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 || strings.TrimSpace(parts[0]) != "rc" {
			continue
		}
		res = append(res, Residue{Package: parts[1]})
		cur = len(res) - 1
		conf(parts[2])
	}
	return res
}

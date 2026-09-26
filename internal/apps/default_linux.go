//go:build linux

package apps

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mralaminahamed/reclaim/internal/fsutil"
)

// FindIdle reports applications idle for at least idle, from this machine's
// .desktop launchers.
func FindIdle(home string, idle time.Duration) Result {
	return Find(DefaultEnv(home), idle)
}

// DefaultEnv returns an Env for this machine.
func DefaultEnv(home string) Env {
	owners := dpkgOwners()
	auto := aptAuto()
	mounts := atimeMounts()
	return Env{
		Home: home,
		Now:  time.Now(),
		Dirs: []Dir{
			{"/usr/share/applications", Deb},
			{"/usr/local/share/applications", Local},
			{"/var/lib/snapd/desktop/applications", Snap},
			{"/var/lib/flatpak/exports/share/applications", Flatpak},
			{filepath.Join(home, ".local/share/flatpak/exports/share/applications"), Flatpak},
			{filepath.Join(home, ".local/share/applications"), Local},
		},
		Owner:    func(p string) string { return owners[p] },
		Auto:     func(pkg string) bool { return auto[pkg] },
		PkgBytes: func(src Source, pkg string) int64 { return pkgBytes(home, src, pkg) },
		Used:     func(p string) (time.Time, bool) { return atime(p, mounts) },
		LookPath: exec.LookPath,
		Needed:   aptNeeded,
		DirBytes: func(p string) int64 { n, _ := fsutil.PathBytes(p); return n },
		ShellSeen: shellSeen(filepath.Join(home,
			".local/share/gnome-shell/application_state")),
	}
}

// dpkgOwners maps each installed launcher to its package, read from dpkg's
// file lists in one pass rather than a dpkg -S per file.
func dpkgOwners() map[string]string {
	out := map[string]string{}
	lists, _ := filepath.Glob("/var/lib/dpkg/info/*.list")
	for _, l := range lists {
		pkg := strings.TrimSuffix(filepath.Base(l), ".list")
		pkg, _, _ = strings.Cut(pkg, ":") // drop the architecture qualifier
		f, err := os.Open(l)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasSuffix(line, ".desktop") && strings.Contains(line, "/applications/") {
				out[line] = pkg
			}
		}
		f.Close()
	}
	return out
}

// aptNeeded reports whether any installed package depends on, or recommends,
// pkg. Recommends count: apt installs them by default, and removing one breaks
// the metapackage that asked for it.
func aptNeeded(pkg string) bool {
	out, err := exec.Command("apt-cache", "rdepends", "--installed",
		"--no-suggests", "--no-enhances", pkg).Output()
	if err != nil {
		// Unknown is treated as needed: the safe answer drops the app.
		return true
	}
	_, rest, _ := strings.Cut(string(out), "Reverse Depends:")
	for _, l := range strings.Split(rest, "\n") {
		if n := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "|")); n != "" && n != pkg {
			return true
		}
	}
	return false
}

// aptAuto reads which packages apt installed only as dependencies.
func aptAuto() map[string]bool {
	out := map[string]bool{}
	f, err := os.Open("/var/lib/apt/extended_states")
	if err != nil {
		return out
	}
	defer f.Close()
	var pkg string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), ": ")
		switch k {
		case "Package":
			pkg = v
		case "Auto-Installed":
			if v == "1" {
				out[pkg] = true
			}
		}
	}
	return out
}

func pkgBytes(home string, src Source, pkg string) int64 {
	switch src {
	case Deb:
		out, err := exec.Command("dpkg-query", "-W", "-f", "${Installed-Size}", pkg).Output()
		if err != nil {
			return 0
		}
		kib, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		return kib << 10
	case Snap:
		// Every retained revision is a separate squashfs image.
		var n int64
		imgs, _ := filepath.Glob("/var/lib/snapd/snaps/" + pkg + "_*.snap")
		for _, i := range imgs {
			if fi, err := os.Stat(i); err == nil {
				n += fi.Size()
			}
		}
		return n
	case Flatpak:
		for _, root := range []string{"/var/lib/flatpak/app", filepath.Join(home, ".local/share/flatpak/app")} {
			if n, err := fsutil.PathBytes(filepath.Join(root, pkg)); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// mount is a mount point and whether it records access times.
type mount struct {
	path  string
	atime bool
}

// atimeMounts reads which filesystems record access times. relatime still
// does -- at most once a day, which is far finer than months of disuse needs.
// noatime does not, and there an old atime means nothing at all.
func atimeMounts() []mount {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	var out []mount
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		opts := "," + f[5] + ","
		out = append(out, mount{path: f[4], atime: !strings.Contains(opts, ",noatime,")})
	}
	return out
}

// atime is when a file was last read, if its filesystem records that.
func atime(path string, mounts []mount) (time.Time, bool) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return time.Time{}, false
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return time.Time{}, false
	}
	best := -1
	for i, m := range mounts {
		if within(real, m.path) && (best < 0 || len(m.path) >= len(mounts[best].path)) {
			best = i
		}
	}
	if best < 0 || !mounts[best].atime {
		return time.Time{}, false
	}
	return time.Unix(st.Atim.Sec, st.Atim.Nsec), true
}

func within(p, root string) bool {
	return root == "/" || p == root || strings.HasPrefix(p, root+"/")
}

var shellApp = regexp.MustCompile(`<application id="([^"]+)"[^>]*last-seen="(\d+)"`)

// shellSeen reads GNOME Shell's record of when each app was last running.
func shellSeen(path string) map[string]time.Time {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]time.Time{}
	for _, m := range shellApp.FindAllStringSubmatch(string(data), -1) {
		if s, err := strconv.ParseInt(m[2], 10, 64); err == nil {
			out[m[1]] = time.Unix(s, 0)
		}
	}
	return out
}

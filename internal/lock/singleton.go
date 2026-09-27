package lock

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// singletonDepth is how many directories above a unit's path are searched for
// a SingletonLock: a profile cache such as <app>/Partitions/<id>/Cache sits
// three below the user-data directory that holds the lock.
const singletonDepth = 4

// Singletons locks units inside any Chromium or Electron user-data directory
// whose SingletonLock says the app is running.
//
// Chromium keeps SingletonLock as a symlink to "<host>-<pid>" for as long as
// it runs, and so does every Electron app. That identifies a running app the
// name table in DefaultRules has never heard of. A lock naming this host
// counts only if its pid is alive, since a crash leaves the link behind. A
// lock naming another host is honoured as it stands: on a shared home the app
// is running over there, and its pid means nothing here.
func Singletons(r *unit.Registry, host string, alive func(pid int) bool) {
	for _, u := range r.All() {
		if u.LockedBy != "" || u.Kind != unit.KindPaths {
			continue
		}
		for _, p := range u.Paths {
			if name, pid, ok := heldBy(p, host, alive); ok {
				u.LockedBy, u.PID = name, pid
				break
			}
		}
	}
}

func heldBy(path, host string, alive func(int) bool) (string, int, bool) {
	dir := filepath.Clean(path)
	for i := 0; i <= singletonDepth; i++ {
		if target, err := os.Readlink(filepath.Join(dir, "SingletonLock")); err == nil {
			cut := strings.LastIndex(target, "-")
			if cut > 0 {
				pid, err := strconv.Atoi(target[cut+1:])
				if err == nil {
					name := filepath.Base(dir)
					if target[:cut] != host {
						return name + " on " + target[:cut], 0, true
					}
					if alive(pid) {
						return name, pid, true
					}
				}
			}
			return "", 0, false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", 0, false
}

// Alive reports whether a process with this pid exists. EPERM means it does
// but belongs to someone else, which is still alive.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

package main

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// owner is whose home a file belongs in, and whether it must be handed to
// them after root writes it.
type owner struct {
	home     string
	uid, gid int
	chown    bool
}

// ownerFor resolves the user a run is really for. Under sudo HOME is root's,
// and anything written there is invisible to the user who asked for it.
func ownerFor(euid int, home string, getenv func(string) string,
	lookup func(string) (*user.User, error)) owner {
	name := getenv("SUDO_USER")
	if euid != 0 || name == "" || name == "root" {
		return owner{home: home}
	}
	u, err := lookup(name)
	if err != nil || u.HomeDir == "" {
		return owner{home: home}
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return owner{home: home}
	}
	return owner{home: u.HomeDir, uid: uid, gid: gid, chown: true}
}

func invokingOwner() owner {
	home, _ := os.UserHomeDir()
	return ownerFor(os.Geteuid(), home, os.Getenv, user.Lookup)
}

// handBack gives a file root wrote to the user it was written for, along with
// every directory root created on the way to it. Only directories inside the
// user's home and owned by root are touched: those are the ones this run made.
func (o owner) handBack(path string) error {
	if !o.chown {
		return nil
	}
	home := filepath.Clean(o.home)
	for d := filepath.Dir(path); d != home && strings.HasPrefix(d, home+"/"); d = filepath.Dir(d) {
		fi, err := os.Lstat(d)
		if err != nil {
			return err
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid == 0 {
			if err := os.Chown(d, o.uid, o.gid); err != nil {
				return err
			}
		}
	}
	return os.Chown(path, o.uid, o.gid)
}

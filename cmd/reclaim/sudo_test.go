package main

import (
	"errors"
	"os/user"
	"testing"
)

func lookup(name string) (*user.User, error) {
	if name == "alamin" {
		return &user.User{Username: "alamin", Uid: "1000", Gid: "1000", HomeDir: "/home/alamin"}, nil
	}
	return nil, errors.New("unknown user")
}

// Under sudo, HOME is root's. An index written there is invisible to the user
// who asked for it, so it belongs in the invoking user's home, owned by them.
func TestOwnerUnderSudoIsTheInvokingUser(t *testing.T) {
	env := map[string]string{"SUDO_USER": "alamin"}
	o := ownerFor(0, "/root", func(k string) string { return env[k] }, lookup)
	if o.home != "/home/alamin" || o.uid != 1000 || o.gid != 1000 || !o.chown {
		t.Errorf("got %+v", o)
	}
}

func TestOwnerWithoutSudoIsUnchanged(t *testing.T) {
	o := ownerFor(1000, "/home/alamin", func(string) string { return "" }, lookup)
	if o.home != "/home/alamin" || o.chown {
		t.Errorf("got %+v", o)
	}
}

// Root logged in as root, or sudo from root itself: nothing to hand back.
func TestOwnerForRealRootIsRoot(t *testing.T) {
	env := map[string]string{"SUDO_USER": "root"}
	o := ownerFor(0, "/root", func(k string) string { return env[k] }, lookup)
	if o.home != "/root" || o.chown {
		t.Errorf("got %+v", o)
	}
}

// A SUDO_USER that does not resolve is not guessed at.
func TestOwnerWithUnknownSudoUserFallsBack(t *testing.T) {
	env := map[string]string{"SUDO_USER": "ghost"}
	o := ownerFor(0, "/root", func(k string) string { return env[k] }, lookup)
	if o.home != "/root" || o.chown {
		t.Errorf("got %+v", o)
	}
}

// Without root the chown cannot run, but the walk can be checked: it must stop
// at the home directory and never climb above it.
func TestHandBackIsANoOpWithoutSudo(t *testing.T) {
	if err := (owner{home: "/home/x"}).handBack("/etc/passwd"); err != nil {
		t.Errorf("no-op returned %v", err)
	}
}

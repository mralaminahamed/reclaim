package catalog

import (
	"strings"
	"testing"
)

func podmanEnv(d func() (PodmanState, error)) Env {
	return Env{Home: "/nonexistent", Has: func(b string) bool { return b == "podman" }, Podman: d}
}

// A stopped container is not a cache: its writable layer can hold state that
// exists nowhere else. --podman must never remove one.
func TestPodmanPruneLeavesContainersAlone(t *testing.T) {
	r := Build(podmanEnv(func() (PodmanState, error) { return PodmanState{}, nil }))
	u, ok := r.Get("podman-prune")
	if !ok {
		t.Fatal("podman-prune missing")
	}
	if strings.Contains(u.Command, "container") || strings.Contains(u.Command, "podman rm") {
		t.Errorf("podman-prune removes containers: %q", u.Command)
	}
}

func TestPodmanContainersUnitNamesAndRemovesExactlyWhatItListed(t *testing.T) {
	r := Build(podmanEnv(func() (PodmanState, error) {
		return PodmanState{Stopped: []Container{
			{ID: "3f2a9c1b7d4e", Name: "infra-postgres-1", Image: "postgres:17", Status: "Exited (0) 2 hours ago"},
		}}, nil
	}))
	u, ok := r.Get("podman-containers")
	if !ok {
		t.Fatal("podman-containers missing")
	}
	if u.Flag != "--podman-containers" || u.Reversible {
		t.Errorf("flag %q reversible %v", u.Flag, u.Reversible)
	}
	if u.Command != "podman rm 3f2a9c1b7d4e" {
		t.Errorf("command %q", u.Command)
	}
	if len(u.Detail) != 1 || !strings.Contains(u.Detail[0], "infra-postgres-1") {
		t.Errorf("detail %v does not name the container", u.Detail)
	}
}

func TestPodmanContainersRefusesMalformedIDs(t *testing.T) {
	r := Build(podmanEnv(func() (PodmanState, error) {
		return PodmanState{Stopped: []Container{{ID: "abc; rm -rf ~", Name: "x"}}}, nil
	}))
	if _, ok := r.Get("podman-containers"); ok {
		t.Error("registered a unit for a malformed id")
	}
}

func TestPodmanContainersAbsentWhenTheListFails(t *testing.T) {
	r := Build(podmanEnv(func() (PodmanState, error) {
		return PodmanState{}, errTest
	}))
	if _, ok := r.Get("podman-containers"); ok {
		t.Error("registered without a list")
	}
}

func TestPodmanPruneSaysWhatItWillTake(t *testing.T) {
	r := Build(podmanEnv(func() (PodmanState, error) {
		return PodmanState{Dangling: 3}, nil
	}))
	u, _ := r.Get("podman-prune")
	if !strings.Contains(strings.Join(u.Detail, " "), "3 dangling images") {
		t.Errorf("detail %v", u.Detail)
	}
}

// docker and podman are independent: a machine that only has podman must get
// a podman-prune unit without a docker one, and vice versa.
func TestPodmanDoesNotRegisterWithoutTheBinary(t *testing.T) {
	r := Build(Env{Home: "/nonexistent", Has: func(string) bool { return false }})
	if _, ok := r.Get("podman-prune"); ok {
		t.Error("registered podman-prune though podman is not installed")
	}
}

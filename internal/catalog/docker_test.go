package catalog

import (
	"strings"
	"testing"
)

func dockerEnv(d func() (DockerState, error)) Env {
	return Env{Home: "/nonexistent", Has: func(b string) bool { return b == "docker" }, Docker: d}
}

// A stopped container is not a cache: its writable layer can hold state that
// exists nowhere else. --docker must never remove one.
func TestDockerPruneLeavesContainersAlone(t *testing.T) {
	r := Build(dockerEnv(func() (DockerState, error) { return DockerState{}, nil }))
	u, ok := r.Get("docker-prune")
	if !ok {
		t.Fatal("docker-prune missing")
	}
	if strings.Contains(u.Command, "container") || strings.Contains(u.Command, "docker rm") {
		t.Errorf("docker-prune removes containers: %q", u.Command)
	}
}

// The containers unit removes exactly what the dry run listed, by id -- a
// prune run later would take whatever had stopped in between.
func TestDockerContainersUnitNamesAndRemovesExactlyWhatItListed(t *testing.T) {
	r := Build(dockerEnv(func() (DockerState, error) {
		return DockerState{Stopped: []Container{
			{ID: "3f2a9c1b7d4e", Name: "infra-postgres-1", Image: "postgres:17", Status: "Exited (0) 2 hours ago"},
		}}, nil
	}))
	u, ok := r.Get("docker-containers")
	if !ok {
		t.Fatal("docker-containers missing")
	}
	if u.Flag != "--docker-containers" || u.Reversible {
		t.Errorf("flag %q reversible %v", u.Flag, u.Reversible)
	}
	if u.Command != "docker rm 3f2a9c1b7d4e" {
		t.Errorf("command %q", u.Command)
	}
	if len(u.Detail) != 1 || !strings.Contains(u.Detail[0], "infra-postgres-1") ||
		!strings.Contains(u.Detail[0], "postgres:17") {
		t.Errorf("detail %v does not name the container", u.Detail)
	}
}

// Ids come from docker, but they reach a shell. Anything that is not a hex id
// is dropped.
func TestDockerContainersRefusesMalformedIDs(t *testing.T) {
	r := Build(dockerEnv(func() (DockerState, error) {
		return DockerState{Stopped: []Container{{ID: "abc; rm -rf ~", Name: "x"}}}, nil
	}))
	if _, ok := r.Get("docker-containers"); ok {
		t.Error("registered a unit for a malformed id")
	}
}

// A container list that cannot be read cannot be previewed, so nothing that
// removes containers is offered.
func TestDockerContainersAbsentWhenTheListFails(t *testing.T) {
	r := Build(dockerEnv(func() (DockerState, error) {
		return DockerState{}, errTest
	}))
	if _, ok := r.Get("docker-containers"); ok {
		t.Error("registered without a list")
	}
}

func TestDockerPruneSaysWhatItWillTake(t *testing.T) {
	r := Build(dockerEnv(func() (DockerState, error) {
		return DockerState{Dangling: 3}, nil
	}))
	u, _ := r.Get("docker-prune")
	if !strings.Contains(strings.Join(u.Detail, " "), "3 dangling images") {
		t.Errorf("detail %v", u.Detail)
	}
}

type testErr struct{}

func (testErr) Error() string { return "daemon unreachable" }

var errTest error = testErr{}

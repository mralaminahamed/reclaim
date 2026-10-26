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

// docker volume prune deleted every unused volume, databases included, and the
// dry run could not say which. The unit now removes only volumes shown not to
// hold a store, by name, and says how many it kept.
func TestDockerVolumesRemovesOnlyNonDataByName(t *testing.T) {
	r := Build(dockerEnv(func() (DockerState, error) {
		return DockerState{Volumes: []Volume{
			{Name: "sitemon_go-build", Bytes: 500},
			{Name: "act-toolcache", Bytes: 250},
			{Name: "carelane_pg", Data: true},
		}}, nil
	}))
	u, ok := r.Get("docker-volumes")
	if !ok {
		t.Fatal("docker-volumes missing")
	}
	if u.Command != "docker volume rm sitemon_go-build act-toolcache" {
		t.Errorf("command %q", u.Command)
	}
	if u.Bytes != 750 {
		t.Errorf("bytes %d, want only the removable volumes (750)", u.Bytes)
	}
	if strings.Contains(u.Command, "carelane_pg") || strings.Contains(u.Command, "prune") {
		t.Errorf("command takes a data volume or prunes blindly: %q", u.Command)
	}
	if !strings.Contains(strings.Join(u.Detail, " "), "1 kept") {
		t.Errorf("detail %v does not say what was kept", u.Detail)
	}
}

func TestDockerVolumesAbsentWhenEverythingIsData(t *testing.T) {
	r := Build(dockerEnv(func() (DockerState, error) {
		return DockerState{Volumes: []Volume{{Name: "x_pg", Data: true}}}, nil
	}))
	if _, ok := r.Get("docker-volumes"); ok {
		t.Error("registered a unit with nothing safe to remove")
	}
}

func TestDockerVolumesAbsentWhenTheListFails(t *testing.T) {
	r := Build(dockerEnv(func() (DockerState, error) { return DockerState{}, errTest }))
	if _, ok := r.Get("docker-volumes"); ok {
		t.Error("registered without a list")
	}
}

// Names reach a shell; anything outside docker's alphabet is dropped.
func TestDockerVolumesRefusesMalformedNames(t *testing.T) {
	r := Build(dockerEnv(func() (DockerState, error) {
		return DockerState{Volumes: []Volume{{Name: "a; rm -rf ~"}}}, nil
	}))
	if _, ok := r.Get("docker-volumes"); ok {
		t.Error("registered a unit for a malformed name")
	}
}

func TestVolumeIsData(t *testing.T) {
	cases := []struct {
		name   string
		found  []string
		probed bool
		want   bool
	}{
		{"3a1ef870", []string{"PG_VERSION", "base"}, true, true},
		{"3a1ef870", []string{"WiredTiger.wt"}, true, true},
		{"3a1ef870", []string{"dump.rdb"}, true, true},
		{"app.sqlite3x", []string{"app.sqlite3"}, true, true},
		{"jobpulse_postgres-data", nil, true, true},
		{"infra_mongo", nil, true, true},
		{"sitemon_go-build", []string{"cache"}, true, false},
		{"3a1ef870", nil, true, false},
		{"3a1ef870", nil, false, true}, // unreadable: keep
	}
	for _, c := range cases {
		if got := volumeIsData(c.name, c.found, c.probed); got != c.want {
			t.Errorf("volumeIsData(%q, %v, %v) = %v, want %v", c.name, c.found, c.probed, got, c.want)
		}
	}
}

// The probe once returned an empty listing for every volume, and every
// database read as removable. Content with nothing listed is unreadable.
func TestJudgeVolumeTreatsAnEmptyListingOfANonEmptyVolumeAsData(t *testing.T) {
	if !judgeVolume("3a1ef870", nil, 48<<20, true) {
		t.Error("48 MiB volume with no listing was judged removable")
	}
	if judgeVolume("3a1ef870", nil, 4096, true) {
		t.Error("an empty volume was kept")
	}
}

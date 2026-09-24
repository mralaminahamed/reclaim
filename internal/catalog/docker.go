package catalog

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// Container is a stopped container.
type Container struct {
	ID, Name, Image, Status string
}

// DockerState is what docker holds that nothing is using.
type DockerState struct {
	Stopped  []Container
	Dangling int
}

// containerID is docker's id alphabet. Ids reach a shell; anything else is
// dropped rather than quoted.
var containerID = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

func (b *builder) docker() {
	var st DockerState
	var err error = fmt.Errorf("docker not queried")
	if b.env.Docker != nil {
		st, err = b.env.Docker()
	}

	// Build cache, dangling images and unused networks. Stopped containers
	// used to be pruned here too, and a run with --docker deleted a stopped
	// database container nobody had been shown. They have their own unit now.
	detail := []string{"build cache no image references"}
	if err == nil && st.Dangling > 0 {
		detail = append(detail, fmt.Sprintf("%d dangling images", st.Dangling))
	}
	b.r.Add(&unit.Unit{ID: "docker-prune", Tier: unit.TierIrreplaceable, Reversible: false,
		Label: "docker prune", Kind: unit.KindCmd, Flag: "--docker",
		Command:   "docker builder prune -f; docker network prune -f; docker image prune -f",
		Detail:    detail,
		MountHint: "/var/lib/docker"})

	// A stopped container's writable layer can hold state that exists nowhere
	// else. Offered only when the list could be read, and removing exactly
	// the ids listed: a prune run later takes whatever stopped in between.
	if err == nil {
		var ids, names []string
		for _, c := range st.Stopped {
			if !containerID.MatchString(c.ID) {
				continue
			}
			ids = append(ids, c.ID)
			names = append(names, fmt.Sprintf("%s (%s, %s)", c.Name, c.Image, c.Status))
		}
		if len(ids) > 0 {
			b.r.Add(&unit.Unit{ID: "docker-containers", Tier: unit.TierIrreplaceable,
				Reversible: false, Label: "stopped docker containers", Kind: unit.KindCmd,
				Flag: "--docker-containers", Command: "docker rm " + strings.Join(ids, " "),
				Detail: names, MountHint: "/var/lib/docker"})
		}
	}

	b.r.Add(&unit.Unit{ID: "docker-volumes", Tier: unit.TierIrreplaceable, Reversible: false,
		Label: "docker volumes", Kind: unit.KindCmd, Flag: "--docker-volumes",
		Command: "docker volume prune -f", MountHint: "/var/lib/docker"})
}

// dockerState asks the daemon what is stopped and what is dangling.
func dockerState() (DockerState, error) {
	var st DockerState
	out, err := exec.Command("docker", "ps", "-a", "--no-trunc",
		"--filter", "status=exited", "--filter", "status=created", "--filter", "status=dead",
		"--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}").Output()
	if err != nil {
		return st, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 4 {
			st.Stopped = append(st.Stopped, Container{ID: f[0], Name: f[1], Image: f[2], Status: f[3]})
		}
	}
	out, err = exec.Command("docker", "images", "-q", "-f", "dangling=true").Output()
	if err != nil {
		return st, err
	}
	st.Dangling = len(strings.Fields(string(out)))
	return st, nil
}

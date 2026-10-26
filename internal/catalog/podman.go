package catalog

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/mralaminahamed/reclaim/internal/unit"
)

// PodmanState is what podman holds that nothing is using. Same shape as
// DockerState: podman's CLI mirrors docker's closely enough that the two
// states carry the same two facts.
type PodmanState struct {
	Stopped  []Container
	Dangling int
}

// podman is docker's unit, read verbatim except for the binary name and
// mount hint: rootless podman keeps its storage under
// ~/.local/share/containers rather than /var/lib/docker, and the system
// install uses /var/lib/containers.
func (b *builder) podman() {
	var st PodmanState
	var err error = fmt.Errorf("podman not queried")
	if b.env.Podman != nil {
		st, err = b.env.Podman()
	}

	detail := []string{"build cache no image references"}
	if err == nil && st.Dangling > 0 {
		detail = append(detail, fmt.Sprintf("%d dangling images", st.Dangling))
	}
	b.r.Add(&unit.Unit{ID: "podman-prune", Tier: unit.TierIrreplaceable, Reversible: false,
		Label: "podman prune", Kind: unit.KindCmd, Flag: "--podman",
		Command:   "podman builder prune -f; podman network prune -f; podman image prune -f",
		Detail:    detail,
		MountHint: b.env.Home})

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
			b.r.Add(&unit.Unit{ID: "podman-containers", Tier: unit.TierIrreplaceable,
				Reversible: false, Label: "stopped podman containers", Kind: unit.KindCmd,
				Flag: "--podman-containers", Command: "podman rm " + strings.Join(ids, " "),
				Detail: names, MountHint: b.env.Home})
		}
	}

	b.r.Add(&unit.Unit{ID: "podman-volumes", Tier: unit.TierIrreplaceable, Reversible: false,
		Label: "podman volumes", Kind: unit.KindCmd, Flag: "--podman-volumes",
		Command: "podman volume prune -f", MountHint: b.env.Home})
}

// podmanState asks podman what is stopped and what is dangling.
func podmanState() (PodmanState, error) {
	var st PodmanState
	out, err := exec.Command("podman", "ps", "-a", "--no-trunc",
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
	out, err = exec.Command("podman", "images", "-q", "-f", "dangling=true").Output()
	if err != nil {
		return st, err
	}
	st.Dangling = len(strings.Fields(string(out)))
	return st, nil
}

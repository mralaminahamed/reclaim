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
	// Volumes are the unused ones, each already judged by what it holds.
	Volumes []Volume
}

// Volume is an unused docker volume. Data is true when it holds, or cannot be
// shown not to hold, a database or other store that exists nowhere else.
type Volume struct {
	Name  string
	Data  bool
	Bytes int64
}

// volumeName is docker's volume name alphabet. Names reach a shell and a bind
// mount spec; anything else is dropped rather than quoted.
var volumeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// dataMarkers are file names a data store leaves at the top of its volume (or
// a few levels in, for images that nest the data directory). Finding one means
// the volume is data, whatever it is called.
var dataMarkers = map[string]bool{
	"PG_VERSION": true, "ibdata1": true, "auto.cnf": true, "WiredTiger.wt": true,
	"dump.rdb": true, "appendonly.aof": true, "appendonlydir": true, "data.mdb": true,
	".minio.sys": true, "collections": true, "wal": true, "mysql": true,
}

// dataNames catches stores the markers do not, by the name the volume was
// given. A name match only ever keeps a volume; it never makes one removable.
var dataNames = regexp.MustCompile(`(?i)(postgres|pgdata|_pg$|mysql|maria|mongo|redis|sqlite|qdrant|minio|meili|nats|snapshot|elastic|influx|clickhouse|cassandra|grafana|prometheus|_db$|_data$)`)

// volumeIsData judges a volume from its name and the marker files found in it.
// probed is false when the contents could not be read, and an unreadable
// volume is treated as data: removing the wrong one cannot be undone.
func volumeIsData(name string, found []string, probed bool) bool {
	if !probed || dataNames.MatchString(name) {
		return true
	}
	for _, f := range found {
		if dataMarkers[f] || strings.HasSuffix(f, ".sqlite") || strings.HasSuffix(f, ".sqlite3") ||
			strings.HasSuffix(f, ".db") {
			return true
		}
	}
	return false
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

	// Volumes go the same way as containers: by name, from a list that could be
	// read, and only the ones shown not to hold a database. A blanket
	// "docker volume prune" used to delete every unused volume, data stores
	// included, and the dry run could not say which.
	if err == nil {
		var names, kept []string
		var rm []string
		var total int64
		for _, v := range st.Volumes {
			if !volumeName.MatchString(v.Name) {
				continue
			}
			if v.Data {
				kept = append(kept, v.Name)
				continue
			}
			rm = append(rm, v.Name)
			total += v.Bytes
			names = append(names, v.Name)
		}
		if len(kept) > 0 {
			names = append(names, fmt.Sprintf("%d kept: hold a database or could not be read", len(kept)))
		}
		if len(rm) > 0 {
			b.r.Add(&unit.Unit{ID: "docker-volumes", Tier: unit.TierIrreplaceable, Reversible: false,
				Label: "docker volumes", Kind: unit.KindCmd, Flag: "--docker-volumes",
				Command: "docker volume rm " + strings.Join(rm, " "), Bytes: total,
				Detail: names, MountHint: "/var/lib/docker"})
		}
	}
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
	st.Volumes = unusedVolumes()
	return st, nil
}

// judgeVolume is volumeIsData plus a check on the probe itself. A volume with
// content but nothing listed means the listing failed for it (a find without
// the flags used, a permission wall), not that it is empty, and unreadable is
// data.
func judgeVolume(name string, found []string, size int64, probed bool) bool {
	seen := probed && (len(found) > 0 || size <= emptyVolume)
	return volumeIsData(name, found, seen)
}

// maxProbed bounds how many volumes one probe container mounts.
const maxProbed = 200

// unusedVolumes lists volumes no container references and judges each by its
// contents. One throwaway container mounts them all read-only, so the cost is
// a single docker run however many volumes there are. If no local image can
// run it, every volume comes back as data and nothing is offered.
func unusedVolumes() []Volume {
	out, err := exec.Command("docker", "volume", "ls", "-q", "-f", "dangling=true").Output()
	if err != nil {
		return nil
	}
	var names []string
	for _, n := range strings.Fields(string(out)) {
		if volumeName.MatchString(n) {
			names = append(names, n)
		}
	}
	found, sizes, probed := probeVolumes(names)
	vols := make([]Volume, 0, len(names))
	for _, n := range names {
		vols = append(vols, Volume{Name: n, Data: judgeVolume(n, found[n], sizes[n], probed), Bytes: sizes[n]})
	}
	return vols
}

// emptyVolume is the most du reports for a volume with nothing in it.
const emptyVolume = 8192

// duMark separates the file listing from the sizes in the probe's output. It
// is NUL-delimited on both sides: a file name cannot contain NUL, and every
// listed entry contains a "/", so no name in a volume can reproduce it.
const duMark = "\x00@@du\x00"

// parseProbe turns the probe's output into per-volume file names and sizes.
// Entries for anything but a requested volume are ignored. A missing marker
// means the output was cut short and is reported as not probed.
func parseProbe(out string, want map[string]bool) (found map[string][]string, sizes map[string]int64, ok bool) {
	listing, du, ok := strings.Cut(out, duMark)
	if !ok {
		return nil, nil, false
	}
	found, sizes = map[string][]string{}, map[string]int64{}
	for _, entry := range strings.Split(listing, "\x00") {
		vol, rest, ok := strings.Cut(strings.TrimPrefix(entry, "./"), "/")
		if !ok || !want[vol] {
			continue
		}
		found[vol] = append(found[vol], rest[strings.LastIndex(rest, "/")+1:])
	}
	for _, line := range strings.Split(du, "\n") {
		var kb int64
		var path string
		if n, _ := fmt.Sscanf(line, "%d\t./%s", &kb, &path); n == 2 && want[path] {
			sizes[path] = kb * 1024
		}
	}
	return found, sizes, true
}

// probeVolumes lists file names a few levels into each volume and measures
// each one. It needs an image with sh, find and du already on disk and never
// pulls one.
func probeVolumes(names []string) (found map[string][]string, sizes map[string]int64, ok bool) {
	if len(names) == 0 || len(names) > maxProbed {
		return nil, nil, false
	}
	imgs, err := exec.Command("docker", "images", "--format", "{{.Repository}}:{{.Tag}}").Output()
	if err != nil {
		return nil, nil, false
	}
	// The image is whatever is already on this machine, so the container gets
	// nothing it does not need: no network, a read-only root and mounts, no
	// capabilities beyond reading files another uid owns (database data
	// directories are mode 700).
	args := []string{"run", "--rm", "--pull", "never", "--network", "none", "--read-only",
		"--cap-drop", "ALL", "--cap-add", "DAC_READ_SEARCH", "--security-opt", "no-new-privileges",
		"--entrypoint", "sh"}
	for _, n := range names {
		args = append(args, "-v", n+":/probe/"+n+":ro")
	}
	script := `cd /probe && find . -mindepth 2 -maxdepth 4 -print0 2>/dev/null; printf '\0@@du\0'; du -sk ./* 2>/dev/null`
	for _, img := range strings.Fields(string(imgs)) {
		if strings.Contains(img, "<none>") {
			continue
		}
		out, err := exec.Command("docker", append(append(args, img), "-c", script)...).Output()
		if err != nil {
			continue
		}
		want := make(map[string]bool, len(names))
		for _, n := range names {
			want[n] = true
		}
		if found, sizes, ok = parseProbe(string(out), want); ok {
			return found, sizes, true
		}
	}
	return nil, nil, false
}

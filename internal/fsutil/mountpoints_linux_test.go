package fsutil

import (
	"slices"
	"testing"
)

func TestParseMountinfoReadsMountPointsAndUnescapesThem(t *testing.T) {
	data := `22 1 259:3 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p3 rw
30 22 259:2 / /home rw,relatime shared:2 - ext4 /dev/nvme0n1p2 rw
41 30 259:2 /alamin/data /home/alamin/.cache/my\040data rw,relatime shared:2 - ext4 /dev/nvme0n1p2 rw
`
	got := parseMountinfo(data)

	want := []string{"/", "/home", "/home/alamin/.cache/my data"}
	if !slices.Equal(got, want) {
		t.Errorf("parseMountinfo = %q, want %q", got, want)
	}
}

func TestMountPointsIncludesRoot(t *testing.T) {
	if !slices.Contains(MountPoints(), "/") {
		t.Errorf("MountPoints() = %q, want / among them", MountPoints())
	}
}

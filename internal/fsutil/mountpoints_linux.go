package fsutil

import (
	"os"
	"strconv"
	"strings"
)

// MountPoints lists every mount point on the system, bind mounts included.
//
// A bind mount shares its source's device number, so comparing st_dev cannot
// tell that a directory inside a cache is really somebody's data mounted
// there. The mount table can. /proc/self/mountinfo is read rather than
// /proc/mounts because it is the one that lists bind mounts faithfully.
func MountPoints() []string {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	return parseMountinfo(string(data))
}

// parseMountinfo takes field five of each line, the mount point, which the
// kernel writes with space, tab, newline and backslash as octal escapes.
func parseMountinfo(data string) []string {
	var out []string
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		out = append(out, unescapeOctal(f[4]))
	}
	return out
}

func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

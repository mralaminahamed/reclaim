package fsutil

import (
	"io/fs"
	"syscall"
	"time"
)

func accessTime(fi fs.FileInfo) (time.Time, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(st.Atimespec.Unix()), true
}

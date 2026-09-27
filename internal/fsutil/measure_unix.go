//go:build linux || darwin

package fsutil

import (
	"io/fs"
	"syscall"
)

type stat struct {
	dev, ino, nlink uint64
	blocks          int64
	uid             uint32
}

func statOf(fi fs.FileInfo) (stat, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return stat{}, false
	}
	return stat{
		dev:    uint64(st.Dev),   //nolint:unconvert // int32 on darwin
		ino:    uint64(st.Ino),   //nolint:unconvert
		nlink:  uint64(st.Nlink), //nolint:unconvert // uint16 on darwin
		blocks: int64(st.Blocks), //nolint:unconvert
		uid:    st.Uid,
	}, true
}

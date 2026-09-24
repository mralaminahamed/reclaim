//go:build unix

package main

import (
	"io/fs"
	"os"
	"syscall"
)

func deviceOf(path string) (uint64, bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	return deviceOfInfo(fi)
}

func deviceOfInfo(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert // int32 on darwin
}

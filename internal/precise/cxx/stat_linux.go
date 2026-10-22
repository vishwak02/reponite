//go:build linux

package cxx

import (
	"fmt"
	"os"
	"syscall"
)

// statID identifies a file's content without reading it: size, modification
// and change times, inode.
func statID(fi os.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d:%d.%d:%d", fi.Size(), fi.ModTime().UnixNano(), st.Ctim.Sec, st.Ctim.Nsec, st.Ino)
	}
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

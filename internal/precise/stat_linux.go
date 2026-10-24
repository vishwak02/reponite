//go:build linux

package precise

import (
	"fmt"
	"os"
	"syscall"
)

// StatID identifies a file's content without reading it: size, modification
// and change times, inode. A copy that keeps the modification time (cp -p,
// tar) still changes the change time.
func StatID(fi os.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d:%d.%d:%d", fi.Size(), fi.ModTime().UnixNano(), st.Ctim.Sec, st.Ctim.Nsec, st.Ino)
	}
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

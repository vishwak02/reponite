//go:build !linux

package precise

import (
	"fmt"
	"os"
)

// StatID identifies a file's content without reading it: size and
// modification time.
func StatID(fi os.FileInfo) string {
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

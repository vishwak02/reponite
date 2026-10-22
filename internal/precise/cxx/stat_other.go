//go:build !linux

package cxx

import (
	"fmt"
	"os"
)

// statID identifies a file's content without reading it: size and
// modification time.
func statID(fi os.FileInfo) string {
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

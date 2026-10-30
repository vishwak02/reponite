package precise

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// BlobID is git's id of a file's content (sha1 of "blob <len>\0" + content),
// so a working tree's files and a ref's share entries.
func BlobID(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// MemAvailable is the memory the system can give without swapping (Linux
// /proc/meminfo MemAvailable), 0 when unknown.
func MemAvailable() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, "MemAvailable:"); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				if kb, err := strconv.ParseInt(f[0], 10, 64); err == nil {
					return kb << 10
				}
			}
		}
	}
	return 0
}

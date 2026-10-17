package main

import (
	"bufio"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// setSoftMemoryLimit makes the collector work harder before the process
// outgrows a share of the machine: indexing a very large tree otherwise keeps
// collected garbage around until the heap doubles (GOGC=100), and the peak is
// twice the live data. A limit is advisory — the process still uses what it
// needs — and GOMEMLIMIT, when set, wins.
func setSoftMemoryLimit() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	total := totalMemory()
	if total <= 0 {
		return
	}
	debug.SetMemoryLimit(total / 2)
}

// totalMemory is the machine's RAM in bytes from /proc/meminfo (Linux); 0
// elsewhere, where no limit is set.
func totalMemory() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err == nil {
				return kb * 1024
			}
		}
	}
	return 0
}

package query

import "strings"

// VersionLess orders version-like strings naturally: digit runs compare as
// numbers (3.10 after 3.9), a leading "v" is ignored, and a pre-release sorts
// before its release (1.2-rc1 < 1.2 < 1.2.1).
func VersionLess(a, b string) bool {
	a, b = strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")
	for a != "" && b != "" {
		ca, ra := versionChunk(a)
		cb, rb := versionChunk(b)
		if ca != cb {
			da, db := isDigitRun(ca), isDigitRun(cb)
			switch {
			case da && db:
				na, nb := strings.TrimLeft(ca, "0"), strings.TrimLeft(cb, "0")
				if len(na) != len(nb) {
					return len(na) < len(nb)
				}
				return na < nb
			case da != db:
				return !da // a number sorts after text at the same place
			default:
				return ca < cb
			}
		}
		a, b = ra, rb
	}
	switch {
	case a == "" && b == "":
		return false
	case a == "":
		return b[0] != '-'
	default:
		return a[0] == '-'
	}
}

func versionChunk(s string) (string, string) {
	d := s[0] >= '0' && s[0] <= '9'
	i := 1
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') == d {
		i++
	}
	return s[:i], s[i:]
}

func isDigitRun(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }

package interfaces

import "testing"

func TestAcceptsEncoding(t *testing.T) {
	for _, c := range []struct {
		header, coding string
		want           bool
	}{
		{"", "gzip", false},
		{"gzip", "gzip", true},
		{"gzip, deflate, br", "br", true},
		{"GZIP", "gzip", true},
		{"br;q=0, gzip", "br", false},
		{"br;q=0, gzip", "gzip", true},
		{"br; q=0.5", "br", true},
		{"*", "br", true},
		{"*;q=0", "gzip", false},
		{"*, gzip;q=0", "gzip", false},
		{"identity", "gzip", false},
	} {
		if got := acceptsEncoding(c.header, c.coding); got != c.want {
			t.Errorf("acceptsEncoding(%q, %q) = %v, want %v", c.header, c.coding, got, c.want)
		}
	}
}

func TestETagMatches(t *testing.T) {
	const tag = `W/"abc"`
	for _, c := range []struct {
		header string
		want   bool
	}{
		{"", false},
		{`W/"abc"`, true},
		{`"abc"`, true},
		{`"x", W/"abc"`, true},
		{`"abcd"`, false},
		{"*", true},
	} {
		if got := etagMatches(c.header, tag); got != c.want {
			t.Errorf("etagMatches(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}

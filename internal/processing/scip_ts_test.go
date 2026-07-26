//go:build treesitter

package processing

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// --- minimal SCIP protobuf encoder (field numbers from scip.proto) ---

func pbVarint(v uint64) []byte {
	b := make([]byte, binary.MaxVarintLen64)
	return b[:binary.PutUvarint(b, v)]
}
func pbBytes(num int, payload []byte) []byte {
	out := append([]byte{}, pbVarint(uint64(num)<<3|2)...)
	out = append(out, pbVarint(uint64(len(payload)))...)
	return append(out, payload...)
}
func pbString(num int, s string) []byte { return pbBytes(num, []byte(s)) }
func pbUint(num int, v uint64) []byte {
	return append(append([]byte{}, pbVarint(uint64(num)<<3|0)...), pbVarint(v)...)
}

// scipOccurrence: range=1 (packed), symbol=2, symbol_roles=3 (Definition=1).
func scipOccurrence(symbol string, line0 uint64, isDef bool) []byte {
	var body []byte
	body = append(body, pbBytes(1, append(pbVarint(line0), append(pbVarint(0), pbVarint(20)...)...))...)
	body = append(body, pbString(2, symbol)...)
	if isDef {
		body = append(body, pbUint(3, 1)...)
	}
	return pbBytes(2, body) // Document.occurrences = 2
}

// scipIndex builds an Index with one Document (relative_path=1, occurrences=2).
func scipIndex(path string, occs ...[]byte) []byte {
	doc := pbString(1, path)
	for _, o := range occs {
		doc = append(doc, o...)
	}
	return pbBytes(2, doc) // Index.documents = 2
}

// End to end through IndexDir: a repo carrying index.scip contributes SCIP
// monikers for its definitions and symbol-resolved external references for
// calls into another repo — the Phase 6b cross-boundary tier. Without the file
// nothing changes.
func TestIndexDirReadsSCIPIndex(t *testing.T) {
	const (
		monHandle  = "scip-go gomod github.com/acme/web v0.1.0 `svc`/Handle()."
		monGetUser = "scip-go gomod github.com/acme/api v1.2.0 `pkg/user`/GetUser()."
	)
	dir := t.TempDir()
	src := "package svc\n" + // line 1
		"\n" + // 2
		"func Handle() error {\n" + // 3  <- definition
		"\treturn api.GetUser()\n" + // 4  <- external reference
		"}\n" // 5
	if err := os.WriteFile(filepath.Join(dir, "handler.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// Occurrence lines are 0-based in SCIP: Handle defined on line 3 (=2),
	// GetUser referenced on line 4 (=3).
	index := scipIndex("handler.go",
		scipOccurrence(monHandle, 2, true),
		scipOccurrence(monGetUser, 3, false),
	)
	if err := os.WriteFile(filepath.Join(dir, SCIPFileName), index, 0o644); err != nil {
		t.Fatal(err)
	}

	m := storage.NewMem()
	if err := IndexDir(m, "web", "HEAD", dir, 1); err != nil {
		t.Fatal(err)
	}

	// The local definition carries its moniker, keyed by the indexer's qid.
	mons := m.MonikersAt("web", "HEAD")
	if got := mons["Handle"]; got != monHandle {
		t.Fatalf("definition moniker not recorded: %v", mons)
	}
	// The cross-repo call is a symbol-resolved external reference.
	hits := m.ExternalRefsToSymbol(monGetUser)
	if len(hits) != 1 {
		t.Fatalf("expected one SCIP-resolved reference to GetUser, got %+v", hits)
	}
	if hits[0].Caller != "Handle" || hits[0].ResolutionMethod != MethodSCIP || hits[0].Confidence != ConfSCIP {
		t.Fatalf("reference must be attributed to Handle and labeled scip-resolved: %+v", hits[0])
	}
	// The in-repo definition's own moniker is NOT also an outward reference.
	if len(m.ExternalRefsToSymbol(monHandle)) != 0 {
		t.Fatal("a locally defined moniker must not become a cross-repo reference")
	}
}

// No index.scip (the common case) leaves every existing tier untouched, and a
// CORRUPT index.scip degrades to that same state rather than failing the index
// or inventing monikers.
func TestIndexDirWithoutOrWithBrokenSCIP(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"absent", ""},
		{"corrupt", "this is not a protobuf index"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\nfunc F() {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.content != "" {
				if err := os.WriteFile(filepath.Join(dir, SCIPFileName), []byte(tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			m := storage.NewMem()
			if err := IndexDir(m, "r", "HEAD", dir, 1); err != nil {
				t.Fatalf("indexing must succeed regardless of SCIP state: %v", err)
			}
			if len(m.MonikersAt("r", "HEAD")) != 0 {
				t.Fatalf("no usable SCIP index must yield no monikers, got %v", m.MonikersAt("r", "HEAD"))
			}
			// The normal index is unaffected.
			if _, ok := m.SymbolAt("r", "F", "HEAD"); !ok {
				if len(m.SymbolsAt("r", "HEAD")) == 0 {
					t.Fatal("symbols must still be indexed")
				}
			}
			var _ query.Store = m
		})
	}
}

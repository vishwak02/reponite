package scip

import (
	"encoding/binary"
	"testing"
)

// --- a minimal protobuf ENCODER, so the decoder is tested against real wire
// bytes rather than its own assumptions. Field numbers come from scip.proto. ---

func tag(num, wire int) []byte { return uvarint(uint64(num)<<3 | uint64(wire)) }

func uvarint(v uint64) []byte {
	buf := make([]byte, binary.MaxVarintLen64)
	return buf[:binary.PutUvarint(buf, v)]
}

func bytesField(num int, payload []byte) []byte {
	out := append([]byte{}, tag(num, wireBytes)...)
	out = append(out, uvarint(uint64(len(payload)))...)
	return append(out, payload...)
}

func stringField(num int, s string) []byte { return bytesField(num, []byte(s)) }

func varintField(num int, v uint64) []byte {
	return append(append([]byte{}, tag(num, wireVarint)...), uvarint(v)...)
}

// packedInts encodes a repeated int32 field the way protobuf packs them.
func packedInts(num int, vals ...uint64) []byte {
	var payload []byte
	for _, v := range vals {
		payload = append(payload, uvarint(v)...)
	}
	return bytesField(num, payload)
}

type occ struct {
	symbol string
	line   uint64 // 0-based, as SCIP stores it
	isDef  bool
}

func encodeOccurrence(o occ) []byte {
	var b []byte
	b = append(b, packedInts(fieldOccRange, o.line, 0, 10)...)
	b = append(b, stringField(fieldOccSymbol, o.symbol)...)
	if o.isDef {
		b = append(b, varintField(fieldOccSymbolRoles, uint64(roleDefinition))...)
	}
	return b
}

func encodeDocument(path string, occs ...occ) []byte {
	b := stringField(fieldDocRelativePath, path)
	// A field the reader does not know (Document.language = 4) must be skipped
	// cleanly — forward compatibility is part of the wire contract.
	b = append(b, stringField(4, "go")...)
	for _, o := range occs {
		b = append(b, bytesField(fieldDocOccurrences, encodeOccurrence(o))...)
	}
	return b
}

func encodeIndex(docs ...[]byte) []byte {
	// Index.metadata = 1: an unknown-to-us message that must be skipped.
	b := bytesField(1, stringField(1, "0.3.0"))
	for _, d := range docs {
		b = append(b, bytesField(fieldIndexDocuments, d)...)
	}
	return b
}

const (
	monGetUser = "scip-go gomod github.com/acme/api v1.2.0 `pkg/user`/GetUser()."
	monHelper  = "scip-go gomod github.com/acme/web v0.1.0 `pkg/svc`/helper()."
)

func TestParseDocumentsAndOccurrences(t *testing.T) {
	data := encodeIndex(
		encodeDocument("pkg/svc/handler.go",
			occ{symbol: monHelper, line: 9, isDef: true},   // definition on line 10
			occ{symbol: monGetUser, line: 11, isDef: false}, // reference on line 12
		),
	)
	idx, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Documents) != 1 || idx.Documents[0].Path != "pkg/svc/handler.go" {
		t.Fatalf("documents = %+v", idx.Documents)
	}
	occs := idx.Documents[0].Occurrences
	if len(occs) != 2 {
		t.Fatalf("occurrences = %+v", occs)
	}
	if occs[0].Symbol != monHelper || !occs[0].IsDef || occs[0].Line != 10 {
		t.Fatalf("definition occurrence wrong (line must be 1-based): %+v", occs[0])
	}
	if occs[1].Symbol != monGetUser || occs[1].IsDef || occs[1].Line != 12 {
		t.Fatalf("reference occurrence wrong: %+v", occs[1])
	}
}

// Non-SCIP or truncated input must error, never decode partially: a half-read
// index would claim SCIP precision for some edges while silently dropping others.
func TestParseRejectsGarbage(t *testing.T) {
	for name, data := range map[string][]byte{
		"random bytes":   []byte("this is not protobuf at all, definitely"),
		"truncated":      encodeIndex(encodeDocument("a.go", occ{symbol: monHelper}))[:6],
		"bad wire type":  {0x07, 0x01}, // field 0, wire 7
		"empty is valid": {},
	} {
		idx, err := Parse(data)
		if name == "empty is valid" {
			if err != nil || len(idx.Documents) != 0 {
				t.Errorf("an empty index is valid and empty, got %+v %v", idx, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: expected an error, got %+v", name, idx)
		}
	}
}

func TestLocalDefsAndMap(t *testing.T) {
	idx, err := Parse(encodeIndex(
		encodeDocument("pkg/svc/handler.go",
			occ{symbol: monHelper, line: 4, isDef: true},    // helper defined here
			occ{symbol: monGetUser, line: 6, isDef: false},  // external reference
			occ{symbol: monHelper, line: 7, isDef: false},   // in-repo reference
			occ{symbol: monGetUser, line: 8, isDef: false},  // duplicate external ref
			occ{symbol: "orphan/moniker", line: 99, isDef: false}, // outside any span
		),
	))
	if err != nil {
		t.Fatal(err)
	}
	local := idx.LocalDefs()
	if !local[monHelper] || local[monGetUser] {
		t.Fatalf("LocalDefs must contain only definitions from this index: %v", local)
	}

	spans := []Span{{Name: "Handle", StartLine: 3, EndLine: 12}, {Name: "outer", StartLine: 1, EndLine: 40}}
	fm := Map(idx.Documents[0], spans, local)

	// The definition attributes to the innermost enclosing symbol.
	if fm.Defs["Handle"] != monHelper {
		t.Fatalf("definition moniker not attributed to the innermost span: %+v", fm.Defs)
	}
	// Only the cross-boundary reference survives, deduped once.
	if len(fm.Refs) != 1 || fm.Refs[0].From != "Handle" || fm.Refs[0].Symbol != monGetUser {
		t.Fatalf("refs must be the deduped external one only: %+v", fm.Refs)
	}
}

// An occurrence inside no known span is dropped rather than attributed to a
// guess — file-level code has no symbol to own it.
func TestMapDropsUnattributableOccurrences(t *testing.T) {
	idx, _ := Parse(encodeIndex(encodeDocument("a.go", occ{symbol: monGetUser, line: 99})))
	fm := Map(idx.Documents[0], []Span{{Name: "f", StartLine: 1, EndLine: 5}}, map[string]bool{})
	if len(fm.Refs) != 0 || len(fm.Defs) != 0 {
		t.Fatalf("unattributable occurrence must be dropped, got %+v", fm)
	}
}

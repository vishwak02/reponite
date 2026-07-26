// Package scip reads SCIP indexes (Sourcegraph's Code Intelligence Protocol)
// to lift cross-repo edges above name/path guessing (§8B.4, Phase 6b).
//
// Why it matters: `symbol_hash` deliberately cannot match across repos (§8B.2),
// so cross-boundary linkage has had to key on (module_path, name) — precise
// about *which module*, but still a name match. A SCIP **moniker** is a
// globally unique symbol string that embeds the package manager, package,
// version, and symbol descriptor, e.g.
//
//	scip-go gomod github.com/acme/api v1.2.0 `pkg/user`/GetUser().
//
// Two repos indexed independently emit the *same* moniker for the same symbol,
// so a caller's reference and a definition in another repo can be matched
// symbol-to-symbol rather than by name — the difference between "medium
// confidence, labeled" and proven.
//
// This package is PURE and stdlib-only (ADR-018): a SCIP index is a protobuf
// file, and the handful of fields needed decode with a small wire-format reader
// (no protobuf dependency, no generated code, no build tag). Only these fields
// are read — everything else is skipped as unknown, which is exactly what the
// protobuf wire format prescribes:
//
//	Index.documents            = 2  (repeated Document)
//	Document.relative_path     = 1  (string)
//	Document.occurrences       = 2  (repeated Occurrence)
//	Occurrence.range           = 1  (repeated int32, packed or not)
//	Occurrence.symbol          = 2  (string)
//	Occurrence.symbol_roles    = 3  (int32 bitmask; Definition = 0x1)
//
// FAIL-SAFE BY CONSTRUCTION: monikers are matched by exact string equality
// between two independently produced indexes. A decoding mistake can therefore
// only *lose* matches — falling back to the existing import-resolved and
// name-based tiers — never fabricate a high-confidence edge that isn't real.
package scip

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// SCIP wire constants.
const (
	fieldIndexDocuments      = 2
	fieldDocRelativePath     = 1
	fieldDocOccurrences      = 2
	fieldOccRange            = 1
	fieldOccSymbol           = 2
	fieldOccSymbolRoles      = 3
	roleDefinition       int = 0x1
)

// Occurrence is one symbol occurrence in a document: which moniker, on which
// line, and whether this occurrence is the symbol's definition.
type Occurrence struct {
	Symbol string // the SCIP moniker
	Line   int    // 1-based (SCIP ranges are 0-based)
	IsDef  bool
}

// Document is one file's occurrences, keyed by its repo-relative path.
type Document struct {
	Path        string
	Occurrences []Occurrence
}

// Index is the decoded subset of a SCIP index.
type Index struct {
	Documents []Document
}

// ErrNotSCIP reports data that is not a readable SCIP index.
var ErrNotSCIP = errors.New("not a readable SCIP index")

// Parse decodes the documents/occurrences of a SCIP protobuf index. Malformed
// input is an error, never a partial guess — a half-read index would silently
// downgrade some edges while claiming SCIP precision for others.
func Parse(data []byte) (Index, error) {
	var idx Index
	err := eachField(data, func(num int, wire int, val []byte) error {
		if num != fieldIndexDocuments || wire != wireBytes {
			return nil // metadata, external_symbols, future fields: skipped
		}
		doc, err := parseDocument(val)
		if err != nil {
			return err
		}
		idx.Documents = append(idx.Documents, doc)
		return nil
	})
	if err != nil {
		return Index{}, fmt.Errorf("%w: %v", ErrNotSCIP, err)
	}
	return idx, nil
}

func parseDocument(data []byte) (Document, error) {
	var doc Document
	err := eachField(data, func(num int, wire int, val []byte) error {
		switch {
		case num == fieldDocRelativePath && wire == wireBytes:
			doc.Path = string(val)
		case num == fieldDocOccurrences && wire == wireBytes:
			occ, err := parseOccurrence(val)
			if err != nil {
				return err
			}
			if occ.Symbol != "" {
				doc.Occurrences = append(doc.Occurrences, occ)
			}
		}
		return nil
	})
	return doc, err
}

func parseOccurrence(data []byte) (Occurrence, error) {
	var occ Occurrence
	gotLine := false
	err := eachField(data, func(num int, wire int, val []byte) error {
		switch {
		case num == fieldOccRange:
			// range is [startLine, startChar, endChar] or 4 ints, encoded
			// packed (length-delimited) by most producers but legal unpacked.
			if gotLine {
				return nil // only the first element (start line) matters
			}
			switch wire {
			case wireBytes:
				v, _, err := varint(val)
				if err != nil {
					return err
				}
				occ.Line, gotLine = int(v)+1, true
			case wireVarint:
				v, _, err := varint(val)
				if err != nil {
					return err
				}
				occ.Line, gotLine = int(v)+1, true
			}
		case num == fieldOccSymbol && wire == wireBytes:
			occ.Symbol = string(val)
		case num == fieldOccSymbolRoles && wire == wireVarint:
			v, _, err := varint(val)
			if err != nil {
				return err
			}
			occ.IsDef = int(v)&roleDefinition != 0
		}
		return nil
	})
	return occ, err
}

// --- minimal protobuf wire reader ---

const (
	wireVarint = 0
	wireI64    = 1
	wireBytes  = 2
	wireI32    = 5
)

// eachField walks a protobuf message, invoking fn per field with its number,
// wire type, and payload (varints are handed over as their raw bytes). An
// unrecognized wire type or a truncated buffer is an error — the reader never
// resynchronizes by guessing, because a mis-framed read could attribute one
// symbol's moniker to another.
func eachField(data []byte, fn func(num, wire int, val []byte) error) error {
	for len(data) > 0 {
		key, n, err := varint(data)
		if err != nil {
			return err
		}
		data = data[n:]
		num, wire := int(key>>3), int(key&0x7)
		if num == 0 {
			return errors.New("field number 0")
		}
		var val []byte
		switch wire {
		case wireVarint:
			_, n, err := varint(data)
			if err != nil {
				return err
			}
			val, data = data[:n], data[n:]
		case wireI64:
			if len(data) < 8 {
				return errors.New("truncated 64-bit field")
			}
			val, data = data[:8], data[8:]
		case wireI32:
			if len(data) < 4 {
				return errors.New("truncated 32-bit field")
			}
			val, data = data[:4], data[4:]
		case wireBytes:
			l, n, err := varint(data)
			if err != nil {
				return err
			}
			data = data[n:]
			if uint64(len(data)) < l {
				return errors.New("truncated length-delimited field")
			}
			val, data = data[:l], data[l:]
		default:
			return fmt.Errorf("unsupported wire type %d", wire)
		}
		if err := fn(num, wire, val); err != nil {
			return err
		}
	}
	return nil
}

func varint(b []byte) (uint64, int, error) {
	v, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, 0, errors.New("malformed varint")
	}
	return v, n, nil
}

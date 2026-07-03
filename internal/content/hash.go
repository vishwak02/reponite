// hash.go implements reponite's three-hash identity model (architecture §6):
// symbol_hash (textual identity, storage dedup key), signature_hash (API shape),
// and behavior_hash (a Merkle hash over the resolved call graph, consulted only
// by the Compatibility Oracle), plus the file/embed/edge/manifest hashes.
//
// Every hash is computed over a length-prefixed, domain-separated encoding of
// its fields, not raw concatenation. Length-prefixing guarantees distinct field
// boundaries can never collide (Repo="a",Lang="bc" vs Repo="ab",Lang="c" differ);
// the per-hash domain tag guarantees two hash kinds never collide even on
// crafted input. This realizes the spec's SHA256(field+field+...) definitions
// without their concatenation ambiguity. norm_ver is folded in so hashes never
// silently collide across canonicalization ruleset versions (invariant 1).
package content

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"sort"
)

// Hash is a content hash rendered as "sha256:<hex>".
type Hash string

const hashPrefix = "sha256:"

// Domain tags keep distinct hash kinds from ever colliding.
const (
	domainFile      = "reponite/file/v1"
	domainSymbol    = "reponite/symbol/v1"
	domainSignature = "reponite/signature/v1"
	domainBehavior  = "reponite/behavior/v1"
	domainEmbed     = "reponite/embed/v1"
	domainEdge      = "reponite/edge/v1"
	domainManifest  = "reponite/manifest/v1"
	domainBlob      = "reponite/blob/v1"
)

// fieldHasher accumulates length-prefixed fields into a SHA-256 digest.
type fieldHasher struct{ h hash.Hash }

func newFieldHasher(domain string) *fieldHasher {
	f := &fieldHasher{h: sha256.New()}
	return f.str(domain)
}

func (f *fieldHasher) bytes(b []byte) *fieldHasher {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	f.h.Write(n[:])
	f.h.Write(b)
	return f
}

func (f *fieldHasher) str(s string) *fieldHasher { return f.bytes([]byte(s)) }

func (f *fieldHasher) num(i int) *fieldHasher {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(i))
	return f.bytes(n[:])
}

func (f *fieldHasher) sum() Hash {
	return Hash(hashPrefix + hex.EncodeToString(f.h.Sum(nil)))
}

// SymbolIdentity is the input to the textual and shape hashes for one symbol.
type SymbolIdentity struct {
	Repo      string
	Lang      string
	Kind      string // function|method|class|interface|struct|enum|type
	Signature string
	CanonBody []byte // canon() output over the body; ignored by SignatureHash
}

// SymbolHash is the textual identity and storage dedup key (architecture §6.1).
// It excludes ref and path, so identical code dedups across refs and survives
// file moves.
func SymbolHash(normVer int, id SymbolIdentity) Hash {
	return newFieldHasher(domainSymbol).
		num(normVer).str(id.Repo).str(id.Lang).str(id.Kind).
		str(id.Signature).bytes(id.CanonBody).sum()
}

// SignatureHash is the body-independent API-shape identity (architecture §6.1).
func SignatureHash(normVer int, id SymbolIdentity) Hash {
	return newFieldHasher(domainSignature).
		num(normVer).str(id.Repo).str(id.Lang).str(id.Kind).
		str(id.Signature).sum()
}

// BehaviorHash is a Merkle hash over the resolved call graph (architecture §6.2):
// a symbol's behavior identity is its own textual identity plus the sorted
// behavior hashes of its callees, so a callee's change propagates to every
// transitive caller. Callees are sorted for order-independence; the caller
// supplies the resolved, deduped callee set with SCCs already condensed (§6.3).
func BehaviorHash(symbolHash Hash, normVer int, calleeBehaviorHashes []Hash) Hash {
	sorted := make([]Hash, len(calleeBehaviorHashes))
	copy(sorted, calleeBehaviorHashes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	f := newFieldHasher(domainBehavior).str(string(symbolHash)).num(normVer)
	for _, c := range sorted {
		f.str(string(c))
	}
	return f.sum()
}

// FileHash is the content identity of a canonicalized file (architecture §6).
func FileHash(normVer int, canonFile []byte) Hash {
	return newFieldHasher(domainFile).num(normVer).bytes(canonFile).sum()
}

// BlobHash is the content-addressing key for an arbitrary stored blob (e.g. a
// file's raw text): a domain-separated SHA-256 of the bytes. Unlike the symbol
// hashes it is independent of norm_ver — it identifies bytes for storage dedup,
// not a canonicalized symbol identity, so identical content stores exactly once
// across refs (architecture §4.3/§9).
func BlobHash(data []byte) Hash {
	return newFieldHasher(domainBlob).bytes(data).sum()
}

// EmbedHash keys a cached embedding; it changes only when the model changes, so
// switching models re-embeds on a cache miss rather than forcing a full rebuild.
func EmbedHash(symbolHash Hash, modelVer string) Hash {
	return newFieldHasher(domainEmbed).str(string(symbolHash)).str(modelVer).sum()
}

// EdgeHash identifies a resolved edge. resolution_method is part of the identity
// (invariant 5, architecture §6.5): a SCIP-proven edge and a heuristic one for
// the same pair are distinct blobs, each carrying its own confidence.
func EdgeHash(fromHash, toHash Hash, kind, resolutionMethod string) Hash {
	return newFieldHasher(domainEdge).
		str(string(fromHash)).str(string(toHash)).
		str(kind).str(resolutionMethod).sum()
}

// ManifestHash is the identity of a ref snapshot: SHA-256 over its sorted blob
// hashes (architecture §4.3). Order-independent by construction.
func ManifestHash(blobHashes []Hash) Hash {
	sorted := make([]Hash, len(blobHashes))
	copy(sorted, blobHashes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	f := newFieldHasher(domainManifest)
	for _, b := range sorted {
		f.str(string(b))
	}
	return f.sum()
}

// GroupHash combines the symbol hashes of a strongly-connected component
// (mutually recursive group) into one order-independent unit identity, used by
// the behavior pass to hash an SCC as a single node (architecture §6.3).
func GroupHash(symbolHashes []Hash) Hash {
	sorted := make([]Hash, len(symbolHashes))
	copy(sorted, symbolHashes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	f := newFieldHasher(domainGroup)
	for _, h := range sorted {
		f.str(string(h))
	}
	return f.sum()
}

const domainGroup = "reponite/group/v1"

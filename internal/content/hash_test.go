package content

import (
	"strings"
	"testing"
)

func sym(repo, lang, kind, sig, body string) SymbolIdentity {
	return SymbolIdentity{Repo: repo, Lang: lang, Kind: kind, Signature: sig, CanonBody: []byte(body)}
}

func TestHashFormat(t *testing.T) {
	h := SymbolHash(1, sym("r", "go", "function", "F()", "x"))
	if !strings.HasPrefix(string(h), "sha256:") {
		t.Fatalf("missing prefix: %s", h)
	}
	if got := len(string(h)) - len("sha256:"); got != 64 {
		t.Fatalf("want 64 hex chars, got %d (%s)", got, h)
	}
}

func TestDeterministic(t *testing.T) {
	a := SymbolHash(1, sym("r", "go", "function", "F()", "body"))
	b := SymbolHash(1, sym("r", "go", "function", "F()", "body"))
	if a != b {
		t.Fatalf("nondeterministic: %s != %s", a, b)
	}
}

func TestSignatureIgnoresBody(t *testing.T) {
	id1 := sym("r", "go", "function", "F()", "body-one")
	id2 := sym("r", "go", "function", "F()", "body-two")
	if SignatureHash(1, id1) != SignatureHash(1, id2) {
		t.Fatal("signature hash must ignore the body")
	}
	if SymbolHash(1, id1) == SymbolHash(1, id2) {
		t.Fatal("symbol hash must change when the body changes")
	}
}

func TestSignatureChangeShiftsBoth(t *testing.T) {
	id1 := sym("r", "go", "function", "min(a,b)", "body")
	id2 := sym("r", "go", "function", "max(a,b)", "body")
	if SignatureHash(1, id1) == SignatureHash(1, id2) {
		t.Fatal("signature change must change the signature hash")
	}
	if SymbolHash(1, id1) == SymbolHash(1, id2) {
		t.Fatal("signature change must change the symbol hash")
	}
}

func TestNormVerChangesHash(t *testing.T) {
	id := sym("r", "go", "function", "F()", "body")
	if SymbolHash(1, id) == SymbolHash(2, id) {
		t.Fatal("norm_ver must be folded into the hash")
	}
}

func TestFieldBoundaryNoCollision(t *testing.T) {
	a := SymbolHash(1, sym("a", "bc", "function", "S", ""))
	b := SymbolHash(1, sym("ab", "c", "function", "S", ""))
	if a == b {
		t.Fatal("field boundaries must not collide (length-prefixing broken)")
	}
}

func TestBehaviorOrderIndependent(t *testing.T) {
	s := SymbolHash(1, sym("r", "go", "function", "F()", "body"))
	c1, c2 := Hash("sha256:aaa"), Hash("sha256:bbb")
	if BehaviorHash(s, 1, []Hash{c1, c2}) != BehaviorHash(s, 1, []Hash{c2, c1}) {
		t.Fatal("behavior hash must be independent of callee order")
	}
}

func TestBehaviorPropagatesCalleeChange(t *testing.T) {
	s := SymbolHash(1, sym("r", "go", "function", "Charge()", "body"))
	before := BehaviorHash(s, 1, []Hash{Hash("sha256:validator-v0")})
	after := BehaviorHash(s, 1, []Hash{Hash("sha256:validator-v1")})
	if before == after {
		t.Fatal("a callee behavior change must propagate to the caller's behavior hash")
	}
}

// The moat at the hash layer: identical text (same symbol_hash) but a changed
// callee => same symbol_hash, different behavior_hash. Answering compatibility
// on symbol_hash alone would call this 'unchanged' and lie (architecture §6.2).
func TestMoatSameTextDifferentBehavior(t *testing.T) {
	id := sym("payments", "go", "function", "Charge(c Card)", "return validateCard(c)")
	s1, s2 := SymbolHash(1, id), SymbolHash(1, id)
	if s1 != s2 {
		t.Fatal("identical text must share a symbol_hash")
	}
	b1 := BehaviorHash(s1, 1, []Hash{Hash("sha256:validateCard-buggy")})
	b2 := BehaviorHash(s2, 1, []Hash{Hash("sha256:validateCard-fixed")})
	if b1 == b2 {
		t.Fatal("same text + different callee behavior must differ in behavior_hash")
	}
}

func TestBehaviorDomainSeparatedFromSymbol(t *testing.T) {
	s := SymbolHash(1, sym("r", "go", "function", "F()", "body"))
	if BehaviorHash(s, 1, nil) == s {
		t.Fatal("behavior hash must be domain-separated from symbol hash")
	}
}

func TestEdgeResolutionMethodMatters(t *testing.T) {
	from, to := Hash("sha256:from"), Hash("sha256:to")
	if EdgeHash(from, to, "CALLS", "scip") == EdgeHash(from, to, "CALLS", "treesitter") {
		t.Fatal("edges differing only in resolution_method must be distinct (invariant 5)")
	}
}

func TestManifestOrderIndependent(t *testing.T) {
	a := ManifestHash([]Hash{"sha256:a", "sha256:b", "sha256:c"})
	b := ManifestHash([]Hash{"sha256:c", "sha256:a", "sha256:b"})
	if a != b {
		t.Fatal("manifest hash must be order-independent")
	}
}

func TestEmbedHashModelVer(t *testing.T) {
	s := SymbolHash(1, sym("r", "go", "function", "F()", "body"))
	if EmbedHash(s, "m1") == EmbedHash(s, "m2") {
		t.Fatal("embed hash must change with model version")
	}
}

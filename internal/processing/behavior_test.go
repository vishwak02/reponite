package processing

import (
	"testing"

	"github.com/vishwak02/reponite/internal/content"
)

func h(s string) content.Hash { return content.Hash("sha256:" + s) }

func TestBehaviorPropagatesAlongChain(t *testing.T) {
	edges := []Edge{{"A", "B", 1}, {"B", "C", 1}}
	r0 := ComputeBehavior([]Node{{"A", h("A")}, {"B", h("B")}, {"C", h("C0")}}, edges, 1)
	r1 := ComputeBehavior([]Node{{"A", h("A")}, {"B", h("B")}, {"C", h("C1")}}, edges, 1)
	if r0.BehaviorHash["A"] == r1.BehaviorHash["A"] {
		t.Fatal("a deep callee change must propagate to the root caller")
	}
	if r0.BehaviorHash["B"] == r1.BehaviorHash["B"] {
		t.Fatal("must propagate to the intermediate caller")
	}
	if r0.BehaviorHash["C"] == r1.BehaviorHash["C"] {
		t.Fatal("the changed leaf's own behavior must change")
	}
}

func TestBehaviorMoatSameTextDifferentCallee(t *testing.T) {
	edges := []Edge{{"Charge", "validate", 1}}
	r0 := ComputeBehavior([]Node{{"Charge", h("charge")}, {"validate", h("val0")}}, edges, 1)
	r1 := ComputeBehavior([]Node{{"Charge", h("charge")}, {"validate", h("val1")}}, edges, 1)
	if r0.BehaviorHash["Charge"] == r1.BehaviorHash["Charge"] {
		t.Fatal("identical text with a changed callee must yield a different behavior_hash (the moat)")
	}
}

func TestBehaviorSCCSharedAndSensitive(t *testing.T) {
	edges := []Edge{{"A", "B", 1}, {"B", "A", 1}} // mutual recursion
	r := ComputeBehavior([]Node{{"A", h("A")}, {"B", h("B")}}, edges, 1)
	if r.BehaviorHash["A"] != r.BehaviorHash["B"] {
		t.Fatal("members of an SCC share one behavior hash")
	}
	r2 := ComputeBehavior([]Node{{"A", h("A2")}, {"B", h("B")}}, edges, 1)
	if r.BehaviorHash["A"] == r2.BehaviorHash["A"] {
		t.Fatal("changing one SCC member's text must change the group behavior hash")
	}
}

func TestBehaviorConfIsMinOverSubgraph(t *testing.T) {
	edges := []Edge{{"A", "B", 1.0}, {"B", "C", 0.6}}
	r := ComputeBehavior([]Node{{"A", h("A")}, {"B", h("B")}, {"C", h("C")}}, edges, 1)
	if r.BehaviorConf["C"] != 1.0 {
		t.Fatalf("leaf conf should be 1.0, got %v", r.BehaviorConf["C"])
	}
	if r.BehaviorConf["A"] != 0.6 || r.BehaviorConf["B"] != 0.6 {
		t.Fatalf("conf must be the min over the transitive subgraph, got A=%v B=%v", r.BehaviorConf["A"], r.BehaviorConf["B"])
	}
}

func TestBehaviorCrossRepoCalleeIsLeaf(t *testing.T) {
	rX := ComputeBehavior([]Node{{"A", h("A")}}, []Edge{{"A", "X", 0.5}}, 1)
	rY := ComputeBehavior([]Node{{"A", h("A")}}, []Edge{{"A", "Y", 0.5}}, 1)
	if rX.BehaviorConf["A"] != 0.5 {
		t.Fatalf("edge conf to an external callee must factor in, got %v", rX.BehaviorConf["A"])
	}
	if rX.BehaviorHash["A"] == rY.BehaviorHash["A"] {
		t.Fatal("different external (cross-repo) callees must change the caller's behavior hash")
	}
}

func TestBehaviorOrderIndependent(t *testing.T) {
	nodes := []Node{{"A", h("A")}, {"B", h("B")}, {"C", h("C")}, {"D", h("D")}}
	edges := []Edge{{"A", "B", 1}, {"A", "C", 1}, {"B", "D", 1}, {"C", "D", 1}} // diamond
	r1 := ComputeBehavior(nodes, edges, 1)
	rn := []Node{{"D", h("D")}, {"C", h("C")}, {"B", h("B")}, {"A", h("A")}}
	re := []Edge{{"C", "D", 1}, {"B", "D", 1}, {"A", "C", 1}, {"A", "B", 1}}
	r2 := ComputeBehavior(rn, re, 1)
	for _, id := range []string{"A", "B", "C", "D"} {
		if r1.BehaviorHash[id] != r2.BehaviorHash[id] {
			t.Fatalf("behavior hash for %s must be independent of input order", id)
		}
	}
}

func TestBehaviorDeterministic(t *testing.T) {
	nodes := []Node{{"A", h("A")}, {"B", h("B")}}
	edges := []Edge{{"A", "B", 1}}
	if ComputeBehavior(nodes, edges, 1).BehaviorHash["A"] != ComputeBehavior(nodes, edges, 1).BehaviorHash["A"] {
		t.Fatal("must be deterministic")
	}
}

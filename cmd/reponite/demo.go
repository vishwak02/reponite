package main

import (
	"fmt"

	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// runDemo loads an in-memory index with the canonical scenario and runs the
// flagship flows, printing their JSON envelopes — an end-to-end smoke of the
// whole stack (identity -> behavior -> Oracle/rootcause/grep) with no external
// dependency. The SQLite adapter and tree-sitter parser replace the in-memory
// store and hand-loaded records with real indexing.
func runDemo() {
	m := storage.NewMem()
	// billing.Charge: identical signature across refs, but prod's Charge calls the
	// pre-fix validator (validateCard differs) — the moat's behavior-changed case.
	m.Put("billing", "HEAD", "Charge", storage.SymbolRecord{
		SymbolHash: "sha256:charge", SignatureHash: "sha256:charge-sig", BehaviorHash: "sha256:charge-new",
		BehaviorConf: 1, Callees: []query.Callee{{Name: "validateCard", Confidence: 1}},
	})
	m.Put("billing", "prod", "Charge", storage.SymbolRecord{
		SymbolHash: "sha256:charge", SignatureHash: "sha256:charge-sig", BehaviorHash: "sha256:charge-old",
		BehaviorConf: 1, Callees: []query.Callee{{Name: "validateCard", Confidence: 1}},
	})
	m.Put("billing", "HEAD", "validateCard", storage.SymbolRecord{SymbolHash: "sha256:vc-new", SignatureHash: "sha256:vc-sig", BehaviorHash: "sha256:vc-new"})
	m.Put("billing", "prod", "validateCard", storage.SymbolRecord{SymbolHash: "sha256:vc-old", SignatureHash: "sha256:vc-sig", BehaviorHash: "sha256:vc-old"})
	m.PutFile("billing", "HEAD", query.File{
		Path:    "internal/billing/charge.go",
		Content: "package billing\n\nfunc Charge() error {\n\treturn validateCard()\n}\n",
		Symbols: []query.SymbolSpan{{Name: "Charge", StartLine: 3, EndLine: 5}},
	})

	fmt.Println("# reponite compat Charge   (origin billing@HEAD)")
	rep, _ := query.CompatSymbol(m, query.RepoRef{Repo: "billing", Ref: "HEAD"}, "Charge",
		[]query.RepoRef{{Repo: "billing", Ref: "prod"}, {Repo: "billing", Ref: "v1.0.0"}})
	printJSON(interfaces.CompatJSON(rep))

	fmt.Println("\n# reponite rootcause Charge --from prod --to HEAD")
	printJSON(interfaces.RootCauseJSON(query.RootCauseBy(m, "billing", "Charge", "prod", "HEAD")))

	fmt.Println("\n# reponite grep validateCard --ref HEAD")
	g, _ := query.GrepRepo(m, "billing", "HEAD", "validateCard", query.GrepOptions{Fixed: true})
	printJSON(interfaces.GrepJSON(g))
}

func printJSON(s string, err error) {
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(s)
}

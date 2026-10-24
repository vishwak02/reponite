//go:build treesitter

package processing

import (
	"path"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

func init() {
	// the members a macro defines, parsed as the class body they expand in
	cppMemberParser = func(src string, normVer int) []Symbol {
		root, _, err := parseFileRules([]byte(src), ".hpp", CppRules)
		if err != nil || root == nil {
			return nil
		}
		defer releaseAST(root)
		return Extract(root, CppRules, normVer)
	}
	cppFunctionBodies = func(p, src string) []cppBody {
		_, ext, ok := RulesForSource(path.Ext(p), []byte(src))
		lang := grammarForExt(ext)
		if !ok || lang == nil {
			return nil
		}
		b := []byte(src)
		tree, err := parseTree(b, lang)
		if err != nil || tree == nil {
			return nil
		}
		defer tree.Close()
		var out []cppBody
		// using namespace X; among n's children: open in n's scope
		usingsIn := func(n *sitter.Node) []string {
			var us []string
			for i := 0; i < int(n.ChildCount()); i++ {
				if u := n.Child(i); u.Type() == "using_declaration" {
					if m := reUsingNS.FindStringSubmatch(u.Content(b)); m != nil {
						us = append(us, strings.Join(strings.Fields(m[1]), ""))
					}
				}
			}
			return us
		}
		var walk func(n *sitter.Node, ns, usings []string)
		walk = func(n *sitter.Node, ns, usings []string) {
			usings = append(usings[:len(usings):len(usings)], usingsIn(n)...)
			for i := 0; i < int(n.ChildCount()); i++ {
				ch := n.Child(i)
				switch ch.Type() {
				case "namespace_definition":
					inner := ns
					if id := ch.ChildByFieldName("name"); id != nil {
						inner = append(append([]string(nil), ns...), strings.ReplaceAll(id.Content(b), " ", ""))
					}
					walk(ch, inner, usings)
					continue
				case "function_definition":
					if body := ch.ChildByFieldName("body"); body != nil {
						scopes := []string{strings.Join(ns, "::")}
						// void ns::Cls::f() { … }: looked up in Cls, then ns
						if d := ch.ChildByFieldName("declarator"); d != nil {
							for d.Type() != "function_declarator" && d.ChildByFieldName("declarator") != nil {
								d = d.ChildByFieldName("declarator")
							}
							if q := d.ChildByFieldName("declarator"); q != nil && q.Type() == "qualified_identifier" {
								name := strings.Join(strings.Fields(q.Content(b)), "")
								if k := strings.LastIndex(name, "::"); k > 0 {
									scopes = append(scopes, name[:k])
									if len(ns) > 0 {
										scopes = append(scopes, strings.Join(ns, "::")+"::"+name[:k])
									}
								}
							}
						}
						// a using directive inside the body opens its namespace there
						var local []string
						for _, m := range reUsingNS.FindAllStringSubmatch(body.Content(b), -1) {
							local = append(local, strings.Join(strings.Fields(m[1]), ""))
						}
						out = append(out, cppBody{start: int(body.StartByte()), end: int(body.EndByte()), scopes: scopes,
							usings: append(append([]string(nil), usings...), local...)})
						continue
					}
				}
				walk(ch, ns, usings)
			}
		}
		walk(tree.RootNode(), nil, nil)
		return out
	}
	cppReparse = func(p, src string, normVer int) []Symbol {
		rules, ext, ok := RulesForSource(path.Ext(p), []byte(src))
		if !ok {
			return nil
		}
		root, _, err := parseFileRules([]byte(src), ext, rules)
		if err != nil || root == nil {
			return nil
		}
		defer releaseAST(root)
		return Extract(root, rules, normVer)
	}
}

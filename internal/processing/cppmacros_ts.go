//go:build treesitter

package processing

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
}

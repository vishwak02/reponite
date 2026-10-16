// ui_component.go reads one component definition: its props (TS interface /
// type, PropTypes, Vue defineProps / props option, Svelte `export let` and
// $props), state (useState/useReducer/useContext, redux/zustand/pinia/vuex
// stores, Vue refs, Svelte runes), effects with their dependencies, the
// event handlers bound in its markup, and what it renders. Pure text analysis
// over the stored file (ADR-018): it recognizes the idioms of the common
// frameworks and reports nothing — never a guess — for the rest.
package query

import (
	"regexp"
	"sort"
	"strings"
)

// UIProp is one prop a component accepts.
type UIProp struct {
	Name     string
	Type     string
	Optional bool
	Default  string
}

// UIState is one piece of state a component reads or owns.
type UIState struct {
	Name string
	Kind string // useState | useReducer | context | selector | store | query | ref | reactive | computed | data | $state | let | connect
	From string // initial value, reducer, context, selector or store hook
	Line int
}

// UIEffect is a side effect and what triggers it.
type UIEffect struct {
	Kind    string   // useEffect | useLayoutEffect | onMounted | watch | watchEffect | onMount | $effect | $: | componentDidMount …
	Trigger string   // mount | every render | deps | update | unmount
	Deps    []string // the dependency list / watched source
	Line    int
	API     []UIAPICall
	bodyOff int
	body    string
}

// UIHandler is one event binding in a component's markup.
type UIHandler struct {
	Event   string // click, submit, change …
	Element string // the tag it is bound on (button, form, OrderRow)
	Label   string // the element's visible text, when plain
	Handler string // the handler function, or "(inline)"
	Prop    bool   // the handler is a prop: the parent decides what happens
	Line    int
	API     []UIAPICall
	exprOff int
	expr    string
}

// UIRender is a child component a component renders.
type UIRender struct {
	Name       string
	Line       int
	Def        *UIRef
	Expanded   bool // its subtree appears under Children
	Seen       bool // already expanded elsewhere in this tree
	def        *uiDef
	resolution string
}

var (
	reUseState    = regexp.MustCompile(`\[\s*([\w$]+)\s*(?:,\s*([\w$]+)\s*)?\]\s*=\s*(?:React\.)?useState\s*(?:<[^=;]*?>)?\s*\(`)
	reUseReducer  = regexp.MustCompile(`\[\s*([\w$]+)\s*(?:,\s*([\w$]+)\s*)?\]\s*=\s*(?:React\.)?useReducer\s*(?:<[^=;]*?>)?\s*\(\s*([\w$.]+)`)
	reUseContext  = regexp.MustCompile(`(?:const|let|var)\s+(\{[^}]*\}|[\w$]+)\s*(?::[^=]+)?=\s*(?:React\.)?useContext\s*(?:<[^=;]*?>)?\s*\(\s*([\w$.]+)`)
	reSelector    = regexp.MustCompile(`(?:const|let|var)\s+(\{[^}]*\}|[\w$]+)\s*(?::[^=]+)?=\s*(use\w*Selector|useStoreState|useAtomValue|useAtom|useRecoilValue|useRecoilState)\s*(?:<[^=;]*?>)?\s*\(`)
	reStoreHook   = regexp.MustCompile(`(?:const|let|var)\s+(\{[^}]*\}|\[[^\]]*\]|[\w$]+)\s*(?::[^=]+)?=\s*(use[A-Z]\w*Store|useStore)\s*(?:<[^=;]*?>)?\s*\(`)
	reStoreToRefs = regexp.MustCompile(`(?:const|let|var)\s+(\{[^}]*\})\s*=\s*storeToRefs\s*\(\s*([\w$]+)`)
	reQueryHook   = regexp.MustCompile(`(?:const|let|var)\s+(\{[^}]*\}|\[[^\]]*\]|[\w$]+)\s*(?::[^=]+)?=\s*(useQuery|useSWR|useSWRInfinite|useInfiniteQuery|useMutation|useLazyQuery|useFetch|useAsyncData|useLazyFetch|createQuery|createMutation)\s*(?:<[^=;]*?>)?\s*\(`)
	reVueRef      = regexp.MustCompile(`(?:const|let|var)\s+([\w$]+)\s*(?::[^=]+)?=\s*(ref|shallowRef|reactive|computed|toRef|useLocalStorage)\s*(?:<[^=;]*?>)?\s*\(`)
	reVueData     = regexp.MustCompile(`\bdata\s*\(\s*\)\s*(?::[^{]+)?\{\s*return\s*\{`)
	reVuexMap     = regexp.MustCompile(`\b(mapState|mapGetters|mapStores)\s*\(\s*(?:['"]([\w/]+)['"]\s*,\s*)?([\[{])`)
	reSvRune      = regexp.MustCompile(`(?m)^\s*(?:let|const)\s+([\w$]+)\s*(?::[^=]+)?=\s*\$(state|derived)(?:\.by)?\s*\(`)
	reSvLet       = regexp.MustCompile(`(?m)^\s*let\s+([\w$]+)\s*(?::[^=;]+)?(?:=|;)`)
	reSvStore     = regexp.MustCompile(`\$([a-z][\w]*)\b`)
	reClassState  = regexp.MustCompile(`(?m)(?:this\.state|^\s*state)\s*(?::[^=]+)?=\s*\{`)
	reConnect     = regexp.MustCompile(`\bconnect\s*\(\s*([\w$]+)`)

	reEffect      = regexp.MustCompile(`\b(?:React\.)?(useEffect|useLayoutEffect|useInsertionEffect)\s*\(`)
	reVueHook     = regexp.MustCompile(`\b(onMounted|onBeforeMount|onUnmounted|onBeforeUnmount|onUpdated|onActivated|watchEffect|watch|onMount|onDestroy|afterUpdate|beforeUpdate)\s*\(`)
	reSvEffect    = regexp.MustCompile(`\$effect(?:\.pre)?\s*\(`)
	reSvReactive  = regexp.MustCompile(`(?m)^\s*\$:\s*`)
	reClassEffect = regexp.MustCompile(`(?m)^\s*(?:async\s+)?(componentDidMount|componentDidUpdate|componentWillUnmount|mounted|created|beforeMount|updated)\s*\(\s*[^)]*\)\s*\{`)

	reDefineProps  = regexp.MustCompile(`\bdefineProps\s*(<)?`)
	reOptionsProps = regexp.MustCompile(`(?m)^\s*props\s*:\s*([\[{])`)
	reSvExportLet  = regexp.MustCompile(`(?m)^\s*export\s+let\s+([\w$]+)\s*(?::\s*([^=;\n]+?))?\s*(?:=\s*([^;\n]+?))?\s*;?\s*$`)
	reSvProps      = regexp.MustCompile(`let\s*(\{[^}]*\})\s*(?::\s*([\w$.]+))?\s*=\s*\$props\s*\(`)
	rePropTypes    = regexp.MustCompile(`\.propTypes\s*=\s*\{`)
	reFCType       = regexp.MustCompile(`\b(?:React\.)?(?:FC|FunctionComponent|VFC|SFC|ComponentType)\s*<\s*([\w$.]+)`)
	reForwardRef   = regexp.MustCompile(`\bforwardRef\s*<\s*[^,<>]+(?:<[^<>]*>)?\s*,\s*([\w$.]+)`)
	reClassProps   = regexp.MustCompile(`\bextends\s+(?:React\.)?(?:Pure)?Component\s*<\s*([\w$.]+)`)
)

// analyzeProps reads the props a component declares.
func analyzeProps(r *uiRepo, d *uiDef) []UIProp {
	uf := d.File
	var out []UIProp
	add := func(ps []UIProp) {
		for _, p := range ps {
			dup := false
			for i := range out {
				if out[i].Name == p.Name {
					dup = true
					if out[i].Type == "" {
						out[i].Type = p.Type
					}
					if out[i].Default == "" {
						out[i].Default = p.Default
					}
				}
			}
			if !dup && p.Name != "" {
				out = append(out, p)
			}
		}
	}
	if uf.SFC {
		sc := uf.Script
		if m := reDefineProps.FindStringSubmatchIndex(sc); m != nil {
			if m[2] >= 0 { // defineProps<…>()
				rest := strings.TrimLeft(sc[m[1]:], " \t\n")
				if strings.HasPrefix(rest, "{") {
					off := len(sc) - len(rest)
					if j := matchClose(sc, off); j > 0 {
						add(typeMembers(sc[off+1 : j]))
					}
				} else if id := identRe.FindString(rest); id != "" {
					add(r.typeFields(uf, id, 0))
				}
			} else if open := strings.IndexAny(sc[m[1]:], "{["); open >= 0 && strings.TrimSpace(sc[m[1]:m[1]+open]) == "(" {
				add(vueRuntimeProps(sc, m[1]+open))
			}
		} else if m := reOptionsProps.FindStringSubmatchIndex(sc); m != nil {
			add(vueRuntimeProps(sc, m[2]))
		}
		for _, m := range reSvExportLet.FindAllStringSubmatch(sc, -1) {
			add([]UIProp{{Name: m[1], Type: strings.TrimSpace(m[2]), Default: strings.TrimSpace(m[3]), Optional: m[3] != ""}})
		}
		if m := reSvProps.FindStringSubmatch(sc); m != nil {
			if m[2] != "" {
				add(r.typeFields(uf, m[2], 0))
			}
			add(destructured(m[1]))
		}
		return out
	}
	region := uf.text(d.Start, d.End)
	head := region
	if d.Kind == "class" {
		if m := reClassProps.FindStringSubmatch(head); m != nil {
			add(r.typeFields(uf, m[1], 0))
		}
	} else {
		// The parameter list: the first '(' after the name.
		if i := strings.Index(region, d.Name); i >= 0 {
			head = region[i:]
		}
		open := strings.IndexByte(head, '(')
		pre := ""
		if open >= 0 {
			pre = head[:open]
			if m := reForwardRef.FindStringSubmatch(head[:min(len(head), open+200)]); m != nil {
				add(r.typeFields(uf, m[1], 0))
			}
			// forwardRef((props, ref) => …) / memo(function X(props) …): the
			// parameters are those of the wrapped function.
			inner := head[open+1:]
			if t := strings.TrimLeft(inner, " \n\t"); strings.HasPrefix(t, "function") || strings.HasPrefix(t, "(") && !strings.HasPrefix(head[:open], d.Name+" =") {
				if k := strings.IndexByte(t, '('); k >= 0 && (strings.HasPrefix(t, "function") || strings.HasPrefix(t, "(")) && strings.Contains(pre, "(") {
					open = open + 1 + (len(inner) - len(t)) + k
				}
			}
		}
		if m := reFCType.FindStringSubmatch(pre); m != nil {
			add(r.typeFields(uf, m[1], 0))
		}
		if open >= 0 {
			if close := matchClose(head, open); close > 0 {
				params := strings.TrimSpace(head[open+1 : close])
				first := firstArg(params)
				pat, typ := first, ""
				if strings.HasPrefix(first, "{") {
					if j := matchClose(first, 0); j > 0 {
						pat = first[:j+1]
						typ = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(first[j+1:]), ":"))
					}
				} else if a, b, ok := strings.Cut(first, ":"); ok {
					pat, typ = strings.TrimSpace(a), strings.TrimSpace(b)
				}
				switch {
				case strings.HasPrefix(typ, "{"):
					if j := matchClose(typ, 0); j > 0 {
						add(typeMembers(typ[1:j]))
					}
				case typ != "":
					if id := identRe.FindString(typ); id != "" && id != "any" {
						if id == "React" || id == "Readonly" || id == "PropsWithChildren" {
							if k := strings.IndexByte(typ, '<'); k >= 0 {
								id = identRe.FindString(typ[k+1:])
							}
						}
						add(r.typeFields(uf, id, 0))
					}
				}
				if strings.HasPrefix(pat, "{") {
					add(destructured(pat))
				}
			}
		}
	}
	// Name.propTypes = { … }
	for _, m := range rePropTypes.FindAllStringIndex(uf.Code, -1) {
		lineStart := strings.LastIndexByte(uf.Code[:m[0]], '\n') + 1
		if strings.TrimSpace(uf.Code[lineStart:m[0]]) != d.Name {
			continue
		}
		if j := matchClose(uf.Code, m[1]-1); j > 0 {
			for _, e := range topLevel(uf.Code[m[1]:j], ",") {
				k, v, ok := strings.Cut(e, ":")
				if !ok {
					continue
				}
				v = strings.TrimSpace(v)
				out = mergeProp(out, UIProp{Name: strings.Trim(strings.TrimSpace(k), `"'`), Type: strings.TrimSuffix(strings.TrimPrefix(v, "PropTypes."), ".isRequired"), Optional: !strings.HasSuffix(v, ".isRequired")})
			}
		}
	}
	return out
}

func mergeProp(ps []UIProp, p UIProp) []UIProp {
	for i := range ps {
		if ps[i].Name == p.Name {
			if ps[i].Type == "" {
				ps[i].Type, ps[i].Optional = p.Type, p.Optional
			}
			return ps
		}
	}
	return append(ps, p)
}

// typeFields resolves a props type name to its members: an interface or
// object type alias in the file, or in the file it is imported from.
func (r *uiRepo) typeFields(uf *uiFile, name string, depth int) []UIProp {
	if depth > 3 || name == "" {
		return nil
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	re := regexp.MustCompile(`\b(?:interface\s+` + regexp.QuoteMeta(name) + `\b[^{=;]*\{|type\s+` + regexp.QuoteMeta(name) + `\b\s*(?:<[^=]*>)?\s*=\s*)`)
	if m := re.FindStringIndex(uf.Script); m != nil {
		rest := uf.Script[m[1]:]
		var out []UIProp
		if uf.Script[m[1]-1] == '{' { // interface
			if j := matchClose(uf.Script, m[1]-1); j > 0 {
				out = typeMembers(uf.Script[m[1]:j])
			}
			// interface P extends Base { … }
			hdr := uf.Script[m[0] : m[1]-1]
			if _, ext, ok := strings.Cut(hdr, " extends "); ok {
				for _, b := range strings.Split(ext, ",") {
					if id := identRe.FindString(b); id != "" {
						out = append(out, r.typeFields(uf, id, depth+1)...)
					}
				}
			}
			return out
		}
		// type P = { … } & Base
		for _, part := range topLevel(strings.SplitN(rest, ";", 2)[0], "&") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "{") {
				if j := matchClose(part, 0); j > 0 {
					out = append(out, typeMembers(part[1:j])...)
				}
			} else if id := identRe.FindString(part); id != "" {
				out = append(out, r.typeFields(uf, id, depth+1)...)
			}
		}
		return out
	}
	if im, ok := uf.Imports[name]; ok {
		if p, found := r.resolveSpec(uf.F.Path, im.Spec); found {
			if t := r.ByPath[p]; t != nil {
				n := im.Name
				if n == "default" || n == "*" {
					n = name
				}
				return r.typeFields(t, n, depth+1)
			}
		}
	}
	return nil
}

var reMember = regexp.MustCompile(`^(?:readonly\s+)?['"]?([\w$-]+)['"]?\s*(\?)?\s*(\(|:)\s*([\s\S]*)$`)

// typeMembers parses the members of a TS object type body.
func typeMembers(body string) []UIProp {
	var out []UIProp
	for _, e := range topLevel(body, ";,\n") {
		e = strings.TrimSpace(e)
		if e == "" || strings.HasPrefix(e, "[") || strings.HasPrefix(e, "//") {
			continue
		}
		m := reMember.FindStringSubmatch(e)
		if m == nil {
			continue
		}
		typ := strings.Join(strings.Fields(m[4]), " ")
		if m[3] == "(" {
			typ = "function"
		}
		if len(typ) > 60 {
			typ = typ[:60] + "…"
		}
		out = append(out, UIProp{Name: m[1], Type: typ, Optional: m[2] == "?"})
	}
	return out
}

// vueRuntimeProps parses defineProps({...}) / props: {...} or [...] at open.
func vueRuntimeProps(src string, open int) []UIProp {
	j := matchClose(src, open)
	if j < 0 {
		return nil
	}
	body := src[open+1 : j]
	var out []UIProp
	if src[open] == '[' {
		for _, e := range topLevel(body, ",") {
			if n := strings.Trim(strings.TrimSpace(e), `"'`); n != "" {
				out = append(out, UIProp{Name: n, Optional: true})
			}
		}
		return out
	}
	for _, e := range topLevel(body, ",") {
		k, v, ok := strings.Cut(e, ":")
		if !ok {
			continue
		}
		p := UIProp{Name: strings.Trim(strings.TrimSpace(k), `"'`), Optional: true}
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "{") {
			if m := regexp.MustCompile(`\btype\s*:\s*(\[[^\]]*\]|[\w$.]+)`).FindStringSubmatch(v); m != nil {
				p.Type = m[1]
			}
			if regexp.MustCompile(`\brequired\s*:\s*true`).MatchString(v) {
				p.Optional = false
			}
			if m := regexp.MustCompile(`\bdefault\s*:\s*([^,}\n]+)`).FindStringSubmatch(v); m != nil {
				p.Default = strings.TrimSpace(m[1])
			}
		} else {
			p.Type = v
		}
		out = append(out, p)
	}
	return out
}

// destructured reads the names (and defaults) of a `{ a, b = 1, c: d }` pattern.
func destructured(pat string) []UIProp {
	pat = strings.TrimSpace(pat)
	pat = strings.TrimSuffix(strings.TrimPrefix(pat, "{"), "}")
	var out []UIProp
	for _, e := range topLevel(pat, ",") {
		e = strings.TrimSpace(e)
		if e == "" || strings.HasPrefix(e, "...") {
			continue
		}
		p := UIProp{}
		if k, v, ok := strings.Cut(e, "="); ok && !strings.HasPrefix(v, ">") {
			e, p.Default, p.Optional = strings.TrimSpace(k), strings.TrimSpace(v), true
		}
		if k, _, ok := strings.Cut(e, ":"); ok {
			e = strings.TrimSpace(k)
		}
		if isIdent(e) {
			p.Name = e
			out = append(out, p)
		}
	}
	return out
}

// topLevel splits s at any of seps outside brackets and strings.
func topLevel(s, seps string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\'':
			if k := strings.IndexByte(s[i+1:], c); k >= 0 {
				i += k + 1
			}
		case c == '`':
			i = skipTemplate(s, i)
		case c == '{' || c == '(' || c == '[' || c == '<':
			depth++
		case c == '}' || c == ')' || c == ']' || c == '>' && (i == 0 || s[i-1] != '='):
			depth--
		case depth <= 0 && strings.IndexByte(seps, c) >= 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// analyzeState reads the component's state and store usage.
func analyzeState(r *uiRepo, d *uiDef) []UIState {
	uf := d.File
	code := uf.Script
	base := 0
	if !uf.SFC {
		base = uf.Lines.offset(d.Start)
		code = uf.text(d.Start, d.End)
	}
	var out []UIState
	seen := map[string]bool{}
	add := func(name, kind, from string, off int) {
		from = strings.Join(strings.Fields(from), " ")
		if len(from) > 50 {
			from = from[:50] + "…"
		}
		for _, n := range bindingNames(name) {
			k := n + "\x00" + kind
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, UIState{Name: n, Kind: kind, From: from, Line: uf.Lines.line(base + off)})
		}
	}
	argOf := func(open int) string { // text of the first argument of the call opened at open
		if j := matchClose(code, open); j > 0 {
			return firstArg(code[open+1 : j])
		}
		return ""
	}
	for _, m := range reUseState.FindAllStringSubmatchIndex(code, -1) {
		add(code[m[2]:m[3]], "useState", argOf(m[1]-1), m[0])
	}
	for _, m := range reUseReducer.FindAllStringSubmatchIndex(code, -1) {
		add(code[m[2]:m[3]], "useReducer", code[m[6]:m[7]], m[0])
	}
	for _, m := range reUseContext.FindAllStringSubmatchIndex(code, -1) {
		add(code[m[2]:m[3]], "context", code[m[4]:m[5]], m[0])
	}
	for _, m := range reSelector.FindAllStringSubmatchIndex(code, -1) {
		sel := argOf(m[1] - 1)
		if i := strings.Index(sel, "=>"); i >= 0 {
			sel = strings.TrimSpace(sel[i+2:])
		}
		add(code[m[2]:m[3]], "selector", sel, m[0])
	}
	for _, m := range reStoreHook.FindAllStringSubmatchIndex(code, -1) {
		add(code[m[2]:m[3]], "store", code[m[4]:m[5]], m[0])
	}
	for _, m := range reStoreToRefs.FindAllStringSubmatchIndex(code, -1) {
		add(code[m[2]:m[3]], "store", "storeToRefs("+code[m[4]:m[5]]+")", m[0])
	}
	for _, m := range reQueryHook.FindAllStringSubmatchIndex(code, -1) {
		add(code[m[2]:m[3]], "query", code[m[4]:m[5]]+"("+argOf(m[1]-1)+")", m[0])
	}
	for _, m := range reVueRef.FindAllStringSubmatchIndex(code, -1) {
		add(code[m[2]:m[3]], code[m[4]:m[5]], argOf(m[1]-1), m[0])
	}
	if m := reVueData.FindStringIndex(code); m != nil {
		if j := matchClose(code, m[1]-1); j > 0 {
			for _, e := range topLevel(code[m[1]:j], ",") {
				if k, v, ok := strings.Cut(e, ":"); ok {
					add(strings.TrimSpace(k), "data", v, m[0])
				}
			}
		}
	}
	for _, m := range reVuexMap.FindAllStringSubmatchIndex(code, -1) {
		if j := matchClose(code, m[6]); j > 0 {
			for _, e := range topLevel(code[m[6]+1:j], ",") {
				k, _, _ := strings.Cut(e, ":")
				ns := ""
				if m[4] >= 0 {
					ns = code[m[4]:m[5]]
				}
				add(strings.Trim(strings.TrimSpace(k), `"'`), "store", strings.TrimSpace(code[m[2]:m[3]]+" "+ns), m[0])
			}
		}
	}
	if uf.Ext == ".svelte" {
		for _, m := range reSvRune.FindAllStringSubmatchIndex(code, -1) {
			add(code[m[2]:m[3]], "$"+code[m[4]:m[5]], argOf(m[1]-1), m[0])
		}
		for _, m := range reSvLet.FindAllStringSubmatchIndex(code, -1) {
			if !seen[code[m[2]:m[3]]+"\x00$state"] && !strings.Contains(code[m[0]:m[1]], "$props") {
				add(code[m[2]:m[3]], "let", "", m[0])
			}
		}
		for _, src := range []string{code, uf.Markup} {
			for _, m := range reSvStore.FindAllStringSubmatchIndex(src, -1) {
				n := src[m[2]:m[3]]
				switch n {
				case "state", "derived", "effect", "props", "bindable", "inspect", "host":
					continue
				}
				if _, imported := uf.Imports[n]; imported {
					add(n, "store", "$"+n, m[0])
				}
			}
		}
	}
	if d.Kind == "class" {
		if m := reClassState.FindStringIndex(code); m != nil {
			if j := matchClose(code, m[1]-1); j > 0 {
				for _, e := range topLevel(code[m[1]:j], ",") {
					if k, v, ok := strings.Cut(e, ":"); ok {
						add(strings.TrimSpace(k), "useState", v, m[0])
					}
				}
			}
		}
	}
	// connect(mapStateToProps)(Component): the redux state it is given.
	if !uf.SFC && uf.Default == d.Name || strings.Contains(uf.Code, "("+d.Name+")") {
		for _, m := range reConnect.FindAllStringSubmatchIndex(uf.Code, -1) {
			fn := uf.Code[m[2]:m[3]]
			if fn == "null" || fn == "undefined" {
				continue
			}
			for _, k := range mappedKeys(uf.Code, fn) {
				add(k, "connect", fn, m[0]-base)
			}
		}
	}
	return out
}

// mappedKeys lists the keys of the object a mapStateToProps function returns.
func mappedKeys(code, fn string) []string {
	re := regexp.MustCompile(`(?:const|let|var|function)\s+` + regexp.QuoteMeta(fn) + `\b[^{]*?(?:=>\s*\(\s*\{|\{\s*return\s*\{|=>\s*\{)`)
	m := re.FindStringIndex(code)
	if m == nil {
		return nil
	}
	j := matchClose(code, m[1]-1)
	if j < 0 {
		return nil
	}
	var out []string
	for _, e := range topLevel(code[m[1]:j], ",") {
		k, _, _ := strings.Cut(e, ":")
		if k = strings.TrimSpace(k); isIdent(k) {
			out = append(out, k)
		}
	}
	return out
}

// bindingNames splits a binding pattern ({a, b: c}, [x, setX], name).
func bindingNames(b string) []string {
	b = strings.TrimSpace(b)
	if strings.HasPrefix(b, "{") || strings.HasPrefix(b, "[") {
		var out []string
		for _, e := range topLevel(b[1:len(b)-1], ",") {
			e = strings.TrimSpace(e)
			if k, v, ok := strings.Cut(e, ":"); ok && b[0] == '{' {
				e = strings.TrimSpace(v)
				if !isIdent(e) {
					e = strings.TrimSpace(k)
				}
			}
			if i := strings.IndexByte(e, '='); i >= 0 {
				e = strings.TrimSpace(e[:i])
			}
			if isIdent(e) {
				out = append(out, e)
			}
		}
		if b[0] == '[' && len(out) > 1 {
			out = out[:1] // [value, setter]: the setter is not state
		}
		return out
	}
	return []string{b}
}

// analyzeEffects reads the component's side effects.
func analyzeEffects(d *uiDef) []UIEffect {
	uf := d.File
	code, base := uf.Script, 0
	if !uf.SFC {
		base = uf.Lines.offset(d.Start)
		code = uf.text(d.Start, d.End)
	}
	var out []UIEffect
	callBody := func(open int) (string, int, []string) {
		j := matchClose(code, open)
		if j < 0 {
			return "", 0, nil
		}
		args := splitArgs(code[open+1 : j])
		return code[open+1 : j], open + 1, args
	}
	for _, m := range reEffect.FindAllStringSubmatchIndex(code, -1) {
		body, off, args := callBody(m[1] - 1)
		e := UIEffect{Kind: code[m[2]:m[3]], Line: uf.Lines.line(base + m[0]), body: body, bodyOff: base + off}
		switch {
		case len(args) < 2:
			e.Trigger = "every render"
		default:
			deps := strings.TrimSpace(args[len(args)-1])
			deps = strings.TrimSuffix(strings.TrimPrefix(deps, "["), "]")
			for _, x := range topLevel(deps, ",") {
				if x = strings.TrimSpace(x); x != "" {
					e.Deps = append(e.Deps, x)
				}
			}
			e.Trigger = "deps"
			if len(e.Deps) == 0 {
				e.Trigger = "mount"
			}
		}
		out = append(out, e)
	}
	for _, m := range reVueHook.FindAllStringSubmatchIndex(code, -1) {
		if m[0] > 0 && (code[m[0]-1] == '.' || code[m[0]-1] == '$') {
			continue
		}
		kind := code[m[2]:m[3]]
		body, off, args := callBody(m[1] - 1)
		e := UIEffect{Kind: kind, Line: uf.Lines.line(base + m[0]), body: body, bodyOff: base + off}
		switch kind {
		case "watch":
			if len(args) > 0 {
				e.Deps = []string{strings.TrimSpace(args[0])}
			}
			e.Trigger = "deps"
		case "watchEffect":
			e.Trigger = "deps"
		case "onUnmounted", "onBeforeUnmount", "onDestroy":
			e.Trigger = "unmount"
		case "onUpdated", "afterUpdate", "beforeUpdate":
			e.Trigger = "update"
		default:
			e.Trigger = "mount"
		}
		out = append(out, e)
	}
	if uf.Ext == ".svelte" {
		for _, m := range reSvEffect.FindAllStringIndex(code, -1) {
			body, off, _ := callBody(m[1] - 1)
			out = append(out, UIEffect{Kind: "$effect", Trigger: "deps", Line: uf.Lines.line(base + m[0]), body: body, bodyOff: base + off})
		}
		for _, m := range reSvReactive.FindAllStringIndex(code, -1) {
			end := m[1]
			if end < len(code) && code[end] == '{' {
				if j := matchClose(code, end); j > 0 {
					end = j + 1
				}
			} else if k := strings.IndexByte(code[end:], '\n'); k >= 0 {
				end += k
			} else {
				end = len(code)
			}
			out = append(out, UIEffect{Kind: "$:", Trigger: "deps", Line: uf.Lines.line(base + m[0]), body: code[m[1]:end], bodyOff: base + m[1]})
		}
	}
	for _, m := range reClassEffect.FindAllStringSubmatchIndex(code, -1) {
		j := matchClose(code, m[1]-1)
		if j < 0 {
			continue
		}
		kind := code[m[2]:m[3]]
		trig := "mount"
		switch kind {
		case "componentDidUpdate", "updated":
			trig = "update"
		case "componentWillUnmount":
			trig = "unmount"
		}
		out = append(out, UIEffect{Kind: kind, Trigger: trig, Line: uf.Lines.line(base + m[0]), body: code[m[1]:j], bodyOff: base + m[1]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// markupOf returns the text holding the component's markup and the file
// offset it starts at: the definition's own lines for JSX, the template for
// an SFC.
func markupOf(d *uiDef) (string, int, bool) {
	uf := d.File
	if uf.SFC {
		return uf.Markup, 0, false
	}
	return uf.text(d.Start, d.End), uf.Lines.offset(d.Start), true
}

var (
	reVueEvent = regexp.MustCompile(`^(?:@|v-on:)([\w-]+)`)
	reSvEvent  = regexp.MustCompile(`^on:([\w-]+)`)
	reCallName = regexp.MustCompile(`([.\w$]*?)([A-Za-z_$][\w$]*)\s*(?:<[^<>()=;]*>)?\s*\(`)
	reIdentRef = regexp.MustCompile(`^(?:this\.|props\.|\$props\.)?([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)$`)
	labelAttrs = []string{"aria-label", "title", "label", "text", "tooltip", "name", "data-testid", "id"}
)

// analyzeHandlers reads the event bindings in the component's markup.
func analyzeHandlers(d *uiDef, props []UIProp) []UIHandler {
	uf := d.File
	markup, base, jsx := markupOf(d)
	isProp := map[string]bool{}
	for _, p := range props {
		isProp[p.Name] = true
	}
	var out []UIHandler
	seen := map[string]bool{}
	for _, t := range scanTags(markup, jsx) {
		for _, a := range attrs(t.Attrs) {
			event := ""
			switch {
			case jsx && len(a.Name) > 2 && strings.HasPrefix(a.Name, "on") && a.Name[2] >= 'A' && a.Name[2] <= 'Z' && a.Expr:
				event = strings.ToLower(a.Name[2:3]) + a.Name[3:]
			case !jsx && reVueEvent.MatchString(a.Name):
				event = reVueEvent.FindStringSubmatch(a.Name)[1]
			case !jsx && reSvEvent.MatchString(a.Name):
				event = reSvEvent.FindStringSubmatch(a.Name)[1]
			case !jsx && uf.Ext == ".svelte" && a.Expr && len(a.Name) > 2 && strings.HasPrefix(a.Name, "on") && a.Name[2] >= 'a' && a.Name[2] <= 'z':
				event = a.Name[2:] // Svelte 5 onclick={…}
			}
			if event == "" || strings.TrimSpace(a.Value) == "" {
				continue
			}
			label := t.Label
			if label == "" {
				for _, la := range attrs(t.Attrs) {
					for _, n := range labelAttrs {
						if la.Name == n && la.Value != "" && !strings.ContainsAny(la.Value, "(){}") {
							label = la.Value
							break
						}
					}
					if label != "" {
						break
					}
				}
			}
			exprOff := base + t.Off + len(t.Name) + 1 + a.Off
			h := UIHandler{Event: event, Element: t.Name, Label: label, Line: uf.Lines.line(exprOff), expr: a.Value, exprOff: exprOff}
			val := strings.TrimSpace(a.Value)
			if m := reIdentRef.FindStringSubmatch(val); m != nil {
				segs := strings.Split(m[1], ".")
				h.Handler = segs[len(segs)-1]
				h.Prop = strings.HasPrefix(val, "props.") || strings.HasPrefix(val, "$props.") || isProp[segs[0]] && len(segs) == 1
			} else {
				names := callNames(val, nil)
				switch {
				case len(names) == 0:
					h.Handler = "(inline)"
				default:
					h.Handler = names[0].name
					h.Prop = isProp[names[0].name] || names[0].qual == "props"
				}
			}
			k := h.Element + "\x00" + h.Event + "\x00" + h.Handler + "\x00" + itoa(h.Line)
			if !seen[k] {
				seen[k] = true
				out = append(out, h)
			}
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// calledName is one call in a body: qual.name(…).
type calledName struct{ qual, name string }

// notCalls are callee names that are language constructs, runtime/library
// plumbing or ubiquitous member names — following them leads nowhere.
var notCalls = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true, "function": true, "return": true,
	"typeof": true, "await": true, "async": true, "new": true, "import": true, "require": true, "super": true,
	"then": true, "finally": true, "map": true, "filter": true, "forEach": true, "reduce": true, "find": true,
	"some": true, "every": true, "includes": true, "push": true, "pop": true, "slice": true, "splice": true,
	"concat": true, "join": true, "split": true, "trim": true, "replace": true, "toString": true, "toFixed": true,
	"keys": true, "values": true, "entries": true, "assign": true, "parse": true, "stringify": true, "log": true,
	"warn": true, "error": true, "info": true, "debug": true, "preventDefault": true, "stopPropagation": true,
	"resolve": true, "reject": true, "all": true, "allSettled": true, "setTimeout": true, "clearTimeout": true,
	"setInterval": true, "clearInterval": true, "useCallback": true, "useMemo": true, "t": true, "dispatch": true,
	"emit": true, "$emit": true, "navigate": true, "push_": true, "Number": true, "String": true, "Boolean": true,
	"Array": true, "Object": true, "Date": true, "Promise": true, "JSON": true, "isArray": true, "from": true,
	"has": true, "add": true, "delete": true, "set": true, "get_": true, "trigger": true, "nextTick": true,
	"startsWith": true, "endsWith": true, "toLowerCase": true, "toUpperCase": true, "indexOf": true, "sort": true,
	"length": true, "focus": true, "blur": true, "click": true, "submit_": true, "reset": true, "bind": true,
	"call": true, "apply": true, "unwrap": true, "catch_": true, "fn": true, "cb": true, "callback": true,
}

// callNames lists the calls in a JS expression/body, in order, deduped.
// setters are state setters (setX) to skip.
func callNames(body string, setters map[string]bool) []calledName {
	var out []calledName
	seen := map[string]bool{}
	for _, m := range reCallName.FindAllStringSubmatchIndex(body, -1) {
		name := body[m[4]:m[5]]
		qual := strings.TrimSuffix(body[m[2]:m[3]], ".")
		if m[4] > 0 && m[2] == m[4] {
			if p := body[m[4]-1]; p == '.' || p == '$' || p == '_' || p >= '0' && p <= '9' || p >= 'A' && p <= 'Z' || p >= 'a' && p <= 'z' {
				continue
			}
		}
		if q := strings.Split(qual, "."); len(q) > 0 {
			qual = q[0]
			if qual == "this" && len(q) > 1 {
				qual = ""
			}
		}
		if notCalls[name] || setters[name] || isUpperIdent(name) && qual == "" {
			continue
		}
		k := qual + "." + name
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, calledName{qual: qual, name: name})
	}
	return out
}

// funcBody locates a function named name declared in code (const/let arrow,
// function declaration, class/object method, class field) and returns its
// body text and offset. ok=false when no declaration is found.
func funcBody(code, name string) (string, int, bool) {
	q := regexp.QuoteMeta(name)
	res := []*regexp.Regexp{
		regexp.MustCompile(`(?:const|let|var)\s+` + q + `\s*(?::[^=]+)?=\s*`),
		regexp.MustCompile(`\bfunction\s*\*?\s*` + q + `\s*(?:<[^>(]*>)?\s*\(`),
		regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|static|readonly)\s+)*(?:async\s+)?` + q + `\s*(?:=\s*(?:async\s*)?(?:\([^)]*\)|[\w$]+)\s*(?::[^=]+)?=>|\([^)]*\)\s*(?::[^{]+)?\{|:\s*(?:async\s+)?function\b)`),
	}
	for _, re := range res {
		m := re.FindStringIndex(code)
		if m == nil {
			continue
		}
		return bodyFrom(code, m[1])
	}
	return "", 0, false
}

// bodyFrom returns the body of the function whose declaration ends at i: the
// next {…} block after the parameter list/arrow, or an expression body up to
// the end of its statement.
func bodyFrom(code string, i int) (string, int, bool) {
	// Skip wrappers like useCallback( and the parameter list.
	limit := min(len(code), i+400)
	for j := i; j < limit; j++ {
		switch code[j] {
		case '(':
			k := matchClose(code, j)
			if k < 0 {
				return "", 0, false
			}
			// an argument list we are inside (useCallback(async () => …)): step in
			rest := strings.TrimLeft(code[k+1:], " \t\n")
			if strings.HasPrefix(rest, "=>") || strings.HasPrefix(rest, "{") || strings.HasPrefix(rest, ":") {
				j = k
				continue
			}
			// a wrapper call: look inside it
			continue
		case '{':
			k := matchClose(code, j)
			if k < 0 {
				return "", 0, false
			}
			return code[j+1 : k], j + 1, true
		case '=':
			if j+1 < len(code) && code[j+1] == '>' {
				rest := code[j+2:]
				t := strings.TrimLeft(rest, " \t\n")
				if strings.HasPrefix(t, "{") {
					continue
				}
				start := j + 2 + (len(rest) - len(t))
				end := start
				for end < len(code) && code[end] != '\n' && code[end] != ';' {
					if code[end] == '(' || code[end] == '{' || code[end] == '[' {
						if k := matchClose(code, end); k > 0 {
							end = k
						}
					}
					end++
				}
				return code[start:end], start, true
			}
		case ';':
			return "", 0, false
		}
	}
	return "", 0, false
}

// renders lists the components a definition renders (deduped, in order).
func renders(d *uiDef) []UIRender {
	markup, base, jsx := markupOf(d)
	var out []UIRender
	seen := map[string]bool{}
	add := func(name string, off int) {
		if name == "" || name == d.Name || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, UIRender{Name: name, Line: d.File.Lines.line(base + off)})
	}
	for _, t := range scanTags(markup, jsx) {
		add(componentTagName(t.Name, !jsx), t.Off)
		if !jsx && t.Name == "component" || t.Name == "svelte:component" {
			for _, a := range attrs(t.Attrs) {
				if (a.Name == ":is" || a.Name == "this") && isUpperIdent(a.Value) {
					add(a.Value, t.Off)
				}
			}
		}
		// component={X} / Component={X} / render={wrap(X)} / as={X}: a component
		// passed to be rendered by the receiving element.
		for _, a := range attrs(t.Attrs) {
			switch a.Name {
			case "component", "Component", "render", "as", "layout":
				if !a.Expr {
					continue
				}
				for _, id := range identRe.FindAllString(a.Value, -1) {
					if isUpperIdent(id) {
						add(id, t.Off)
					}
				}
			}
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

package query

import (
	"path"
	"regexp"
	"strings"
)

// --- JVM: Spring MVC / WebFlux annotations, JAX-RS (Java and Kotlin) -------

var (
	reJVMClass   = regexp.MustCompile(`\b(?:class|interface|object)\s+(\w+)`)
	reJVMMapping = regexp.MustCompile(`@(Get|Post|Put|Patch|Delete|Request)Mapping\b`)
	reJAXRSPath  = regexp.MustCompile(`@Path\b`)
	reJAXRSVerb  = regexp.MustCompile(`@(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)
	reJVMMethod  = regexp.MustCompile(`(?:fun\s+(\w+)\s*[(<]|\b(\w+)\s*\([^;{]*\)\s*(?:throws\s+[\w.,\s]+)?\{)`)
	reJVMReqMeth = regexp.MustCompile(`RequestMethod\.(\w+)|\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)
	reJVMString  = regexp.MustCompile(`"([^"]*)"`)
)

// jvmRoutes reads each class's base path (@RequestMapping / @Path on the
// class) and its handler methods' mappings.
func jvmRoutes(repo string, files []File) []Route {
	var out []Route
	for _, f := range files {
		ext := path.Ext(f.Path)
		if ext != ".java" && ext != ".kt" {
			continue
		}
		src := blankJSComments(f.Content) // same comment syntax
		if !strings.Contains(src, "Mapping") && !strings.Contains(src, "@Path") {
			continue
		}
		// structure is read on a copy whose string contents are blanked: a
		// path like "/articles/{slug}" holds braces that are not code
		shape := blankStringContents(src)
		for _, cls := range reJVMClass.FindAllStringSubmatchIndex(shape, -1) {
			open := strings.Index(shape[cls[1]:], "{")
			if open < 0 {
				continue
			}
			open += cls[1]
			end := goMatchBrace(shape, open)
			// the class's own annotations sit between the previous
			// declaration's end and the class keyword
			headStart := strings.LastIndexAny(shape[:cls[0]], "};")
			head := src[headStart+1 : cls[0]]
			bases := jvmPaths(head, true)
			if len(bases) == 0 {
				bases = []string{""}
			}
			body := src[open+1 : end]
			bshape := shape[open+1 : end]
			depth := 0
			// method-level annotations, at the class body's top level
			for i := 0; i < len(body); i++ {
				switch bshape[i] {
				case '{':
					depth++
					continue
				case '}':
					depth--
					continue
				case '@':
				default:
					continue
				}
				if depth != 0 {
					continue
				}
				ann := body[i:]
				var methods, subs []string
				switch {
				case reJVMMapping.MatchString(ann) && reJVMMapping.FindStringIndex(ann)[0] == 0:
					m := reJVMMapping.FindStringSubmatch(ann)
					args := ""
					if after := ann[len(m[0]):]; strings.HasPrefix(strings.TrimSpace(after), "(") {
						args = callArgs(after, strings.Index(after, "(")+1)
					}
					subs = jvmAnnPaths(args)
					if m[1] == "Request" {
						for _, q := range reJVMReqMeth.FindAllStringSubmatch(jvmKwarg(args, "method"), -1) {
							methods = append(methods, strings.ToUpper(q[1]+q[2]))
						}
						if len(methods) == 0 {
							methods = []string{"*"}
						}
					} else {
						methods = []string{strings.ToUpper(m[1])}
					}
				case reJAXRSVerb.MatchString(ann) && reJAXRSVerb.FindStringIndex(ann)[0] == 0:
					methods = []string{reJAXRSVerb.FindStringSubmatch(ann)[1]}
					// a method's own @Path sits with its other annotations
					nextDecl := reJVMMethod.FindStringIndex(ann)
					if nextDecl != nil {
						subs = jvmPaths(ann[:nextDecl[0]], false)
					}
				default:
					continue
				}
				handler := jvmHandlerAfter(ann)
				if handler == "" {
					continue
				}
				if len(subs) == 0 {
					subs = []string{""}
				}
				line := lineAtOffset(src, open+1+i)
				for _, b := range bases {
					for _, s := range subs {
						for _, mt := range methods {
							fw := "spring"
							if strings.HasPrefix(ann, "@GET") || strings.HasPrefix(ann, "@POST") || strings.HasPrefix(ann, "@PUT") ||
								strings.HasPrefix(ann, "@DELETE") || strings.HasPrefix(ann, "@PATCH") {
								fw = "jax-rs"
							}
							out = append(out, Route{Repo: repo, Path: f.Path, Line: line, Method: mt,
								Pattern: normURL(mountPath(b, s)), Handler: handler, Framework: fw})
						}
					}
				}
			}
		}
	}
	return out
}

// jvmPaths reads the path of a class's (or method's) mapping in its
// annotation block: @RequestMapping(...) or @Path("...").
func jvmPaths(block string, class bool) []string {
	var out []string
	if loc := regexp.MustCompile(`@RequestMapping\b`).FindStringIndex(block); loc != nil && class {
		after := block[loc[1]:]
		if strings.HasPrefix(strings.TrimSpace(after), "(") {
			out = append(out, jvmAnnPaths(callArgs(after, strings.Index(after, "(")+1))...)
		}
	}
	if loc := reJAXRSPath.FindStringIndex(block); loc != nil {
		after := block[loc[1]:]
		if strings.HasPrefix(strings.TrimSpace(after), "(") {
			out = append(out, jvmAnnPaths(callArgs(after, strings.Index(after, "(")+1))...)
		}
	}
	return out
}

// jvmAnnPaths reads the paths of an annotation's arguments: "x", {"x","y"},
// value = "x", path = {"x"}.
func jvmAnnPaths(args string) []string {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil
	}
	pick := args
	if v := jvmKwarg(args, "value"); v != "" {
		pick = v
	} else if v := jvmKwarg(args, "path"); v != "" {
		pick = v
	} else if strings.Contains(args, "=") {
		// named arguments without value/path: the first positional, if any
		first := firstArg(args)
		if strings.Contains(first, "=") {
			return nil
		}
		pick = first
	} else {
		pick = firstArg(args)
	}
	var out []string
	for _, m := range reJVMString.FindAllStringSubmatch(pick, -1) {
		out = append(out, m[1])
	}
	return out
}

// jvmKwarg is an annotation argument's value text (name = value).
func jvmKwarg(args, name string) string {
	for _, a := range splitArgs(args) {
		a = strings.TrimSpace(a)
		if strings.HasPrefix(a, name) {
			rest := strings.TrimSpace(a[len(name):])
			if strings.HasPrefix(rest, "=") {
				return strings.TrimSpace(rest[1:])
			}
		}
	}
	return ""
}

// jvmHandlerAfter is the name of the method an annotation block decorates:
// past the annotations (and their arguments), the identifier before the
// first '(' — Kotlin's "fun name(" or Java's "Type name(".
func jvmHandlerAfter(ann string) string {
	rest := ann
	for {
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "@") {
			break
		}
		j := 1
		for j < len(rest) && (rest[j] == '_' || rest[j] == '.' || rest[j] >= 'a' && rest[j] <= 'z' || rest[j] >= 'A' && rest[j] <= 'Z' || rest[j] >= '0' && rest[j] <= '9') {
			j++
		}
		after := strings.TrimLeft(rest[j:], " \t")
		if strings.HasPrefix(after, "(") {
			k := len(rest) - len(after)
			rest = rest[k+1+len(callArgs(rest, k+1))+1:]
		} else {
			rest = rest[j:]
		}
	}
	p := strings.Index(rest, "(")
	if p <= 0 {
		return ""
	}
	fs := strings.Fields(strings.TrimRight(rest[:p], " <>"))
	if len(fs) == 0 {
		return ""
	}
	name := fs[len(fs)-1]
	if i := strings.LastIndexAny(name, ".>"); i >= 0 {
		name = name[i+1:]
	}
	if !isGoIdent(name) {
		return ""
	}
	return name
}

// blankStringContents replaces what is inside string literals with 'x'
// (quotes and length kept), so braces in strings do not count as code.
func blankStringContents(src string) string {
	b := []byte(src)
	for i := 0; i < len(b); i++ {
		if c := b[i]; c == '"' || c == '\'' {
			for i++; i < len(b) && b[i] != c && b[i] != '\n'; i++ {
				if b[i] == '\\' {
					b[i] = 'x'
					i++
				}
				if i < len(b) {
					b[i] = 'x'
				}
			}
		}
	}
	return string(b)
}

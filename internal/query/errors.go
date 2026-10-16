// errors.go answers "where does this error come from, and who handles it?".
// It generalises logsite from log lines to every way code produces an error —
// raise/throw, error values (errors.New, fmt.Errorf, new XError(…)), panics,
// error-level logging, and HTTP error responses (Response(status=4xx),
// res.status(4xx).json, http.Error, abort(404), ResponseStatusException) —
// and pairs each producer with the code that catches it: except/catch blocks
// naming the type or one of its base classes, errors.Is/As, recover, and the
// framework-level handlers (DRF EXCEPTION_HANDLER, FastAPI/Flask exception
// handlers, Express error middleware, Nest @Catch, Spring @ExceptionHandler,
// client-side status checks and response interceptors). Given the message a
// user saw (placeholders filled), producers are ranked like logsite; given a
// type or an HTTP status, every producer of it. Pure over Store.Files and the
// call graph (ADR-018): nothing is captured at index time.
package query

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DefaultErrorsBudget is the token budget when the caller passes <= 0.
const DefaultErrorsBudget = 2000

// ErrorSite is one place that produces an error.
type ErrorSite struct {
	Repo, Path string
	Line       int
	In         string // enclosing symbol (bare)
	Handle     string // enclosing symbol's qualified id (drill-in for brief/context), when resolvable
	Kind       string // raise | throw | error_value | returns (a Go sentinel) | panic | log_error | http_response | error_class
	Type       string // exception class / error constructor / sentinel; "" when none
	Status     int    // HTTP status when known (literal, named constant, or the type's known status)
	Message    string // the message template as written ("" when none)
	Text       string // the source line, trimmed
	Test       bool
	Fixed      int      // message mode: fixed characters of the template that matched
	Match      string   // why it is listed: message | type | status | code | base_type
	Callers    []string // one hop up: qualified callers of the enclosing symbol
	MoreCalls  int      // callers not listed
	lang       string
	lits       []string
}

// ErrorHandler is one place that catches/handles errors.
type ErrorHandler struct {
	Repo, Path string
	Line       int
	In         string
	Kind       string   // except | catch | errors_is | errors_as | recover | exception_handler | drf_exception_handler | error_middleware | exception_filter | error_boundary | status_check | http_interceptor | promise_catch
	Types      []string // caught types (or "HTTP 404"); empty = catches everything
	Match      string   // exact | base:<Base> | status | catch_all
	OnPath     bool     // in the producing function or one of its direct callers
	Text       string
	Test       bool
	lang       string
}

// ErrorType is one error type in the catalogue.
type ErrorType struct {
	Repo     string
	Type     string
	Bases    []string
	Defined  string // path:line of its class, when defined in the repo
	Status   int
	Default  string // default message (DRF default_detail and friends)
	Produced int    // producing sites
	Handled  int    // handler sites naming it
}

// ErrorsResult is the answer: ranked producers + their handlers, or a catalogue.
type ErrorsResult struct {
	Query    string
	Mode     string // message | type | status | catalogue
	Sites    []ErrorSite
	Handlers []ErrorHandler
	Types    []ErrorType // catalogue mode
	// Data are places the error's name or text is DEFINED as data (a JSON/YAML
	// error catalogue, a message table) rather than produced by a raise: the
	// code then looks it up by key or code at runtime. LoadedBy cites code
	// files that name the data file, the hop back into the producing code.
	Data      []ErrorDataDef
	Total     int // producers matched (message/type/status) or found (catalogue)
	Truncated bool
	Next      string
	Note      string
	Meta      Meta
}

// errLang maps an extension to the scanner's dialect.
func errLang(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py":
		return "py"
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return "js"
	case ".go":
		return "go"
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx":
		return "cpp"
	case ".java", ".kt":
		return "java"
	case ".rs":
		return "rust"
	}
	return ""
}

// litLang is the stringLiterals dialect for a scanner dialect.
func litLang(l string) string {
	switch l {
	case "py", "js", "go":
		return l
	}
	return "c"
}

var (
	// producers
	pyRaise       = regexp.MustCompile(`^\s*raise\s+([A-Za-z_][\w.]*)`)
	pyHTTPResp    = regexp.MustCompile(`\b(Response|JsonResponse|JSONResponse|HttpResponse\w*|abort|make_response|PlainTextResponse|HTMLResponse)\s*\(`)
	pyLogErr      = regexp.MustCompile(`\b(?:\w*log(?:ger)?|logging|LOGGER|LOG)\.(error|exception|critical|fatal)\s*\(|\brospy\.log(?:err|fatal)\w*\s*\(`)
	jsThrow       = regexp.MustCompile(`\bthrow\s+(?:new\s+)?([A-Za-z_$][\w$.]*)\s*\(`)
	jsReject      = regexp.MustCompile(`\b(?:Promise\.)?reject\(\s*new\s+([A-Za-z_$][\w$.]*)\s*\(`)
	jsStatus      = regexp.MustCompile(`\b(?:res|response|reply|ctx|resp)\.(?:status|code)\(\s*(\d{3})\s*\)|\bstatus\s*:\s*(\d{3})\b|\bctx\.throw\(\s*(\d{3})`)
	jsLogErr      = regexp.MustCompile(`\b(?:console|logger|log|this\.logger)\.error\s*\(`)
	newErrType    = regexp.MustCompile(`\bnew\s+([A-Z][\w$.]*(?:Error|Exception|Fault))\s*\(`)
	goErrNew      = regexp.MustCompile(`\b(errors\.New|fmt\.Errorf|errors\.Errorf|errors\.Wrapf?|status\.Errorf|status\.Error|echo\.NewHTTPError|fiber\.NewError)\s*\(`)
	goSentinel    = regexp.MustCompile(`^\s*(?:var\s+)?(Err\w*|err\w+)\s*=\s*(?:errors\.New|fmt\.Errorf)`)
	goPanic       = regexp.MustCompile(`\bpanic\s*\(`)
	goRetSentinel = regexp.MustCompile(`\breturn\b.*?\b(Err[A-Z]\w*)\b`)
	goLogErr      = regexp.MustCompile(`\b(?:log|klog|logger|zap|slog|l)\.(?:Fatal|Fatalf|Panic|Panicf|Error|Errorf|Errorw)\s*\(`)
	goHTTPErr     = regexp.MustCompile(`\bhttp\.Error\s*\(|\.(?:JSON|AbortWithStatusJSON|AbortWithStatus|String|NoContent|WriteHeader)\(\s*(?:http\.(Status\w+)|(\d{3}))`)
	grpcCode      = regexp.MustCompile(`codes\.(\w+)`)
	cppThrow      = regexp.MustCompile(`\bthrow\s+([A-Za-z_][\w:]*)\s*[({]`)
	rosLogErr     = regexp.MustCompile(`\b(ROS_(?:ERROR|FATAL)\w*|RCLCPP_(?:ERROR|FATAL)\w*|ROS_ERROR_STREAM\w*)\s*\(`)
	javaThrow     = regexp.MustCompile(`\bthrow\s+new\s+([\w.]+)\s*\(`)
	javaRespStat  = regexp.MustCompile(`ResponseStatusException\(\s*HttpStatus\.(\w+)|ResponseEntity\.status\(\s*HttpStatus\.(\w+)|ResponseEntity\.(badRequest|notFound|unprocessableEntity|internalServerError)\(`)
	javaLogErr    = regexp.MustCompile(`\b(?:log|logger|LOG|LOGGER)\.error\s*\(`)
	rustPanic     = regexp.MustCompile(`\b(panic|bail|anyhow|unimplemented)!\s*\(|\.expect\(\s*"`)
	rustErr       = regexp.MustCompile(`\bErr\(\s*([A-Z]\w*(?:::\w+)*)`)
	rustLogErr    = regexp.MustCompile(`\b(?:log::|tracing::)?error!\s*\(`)
	statusKwarg   = regexp.MustCompile(`\bstatus(?:_code)?\s*[=:]\s*(?:status\.)?(?:HTTP_)?(\d{3})|\bHTTP_(\d{3})_\w+|\babort\(\s*(\d{3})|\berrorhandler\(\s*(\d{3})`)
	httpNamed     = regexp.MustCompile(`\b(?:http\.Status|HttpStatus\.|HTTPStatus\.)(\w+)`)
	dictKey       = regexp.MustCompile(`["'](\w+)["']\s*:`)
	pyDefault     = regexp.MustCompile(`^\s*(default_detail|default_message|message|detail)\s*=\s*(?:_|gettext_lazy|gettext)?\(?\s*[rbuf]*["']`)
	pyStatusAttr  = regexp.MustCompile(`^\s*(?:status_code|code|http_status)\s*=\s*(?:status\.HTTP_)?(\d{3})`)

	// type definitions
	pyErrClass = regexp.MustCompile(`^\s*class\s+(\w+)\s*\(([^)]*)\)\s*:`)
	jsClass    = regexp.MustCompile(`\bclass\s+(\w+)\s+extends\s+([\w$.]+)`)
	cppClass   = regexp.MustCompile(`^\s*(?:class|struct)\s+(?:\w+\s+)?(\w+)\s*(?:final\s*)?:\s*([^{;]+)`)
	javaClass  = regexp.MustCompile(`\bclass\s+(\w+)\s+extends\s+([\w.]+)`)

	// handlers
	pyExcept    = regexp.MustCompile(`^\s*except\s*(?:\(([^)]*)\)|([\w.]+))?(?:\s+as\s+\w+)?\s*:`)
	pyExcHandle = regexp.MustCompile(`(?:@\w+\.exception_handler|\.add_exception_handler|@\w+\.errorhandler|\.register_error_handler)\(\s*([\w.]+)`)
	drfSetting  = regexp.MustCompile(`["']EXCEPTION_HANDLER["']\s*:\s*["']([\w.]+)["']`)
	drfHandler  = regexp.MustCompile(`^\s*def\s+(\w+)\s*\(\s*exc\s*,\s*context\s*\)`)
	djMiddle    = regexp.MustCompile(`^\s*def\s+process_exception\s*\(`)
	jsCatch     = regexp.MustCompile(`\bcatch\s*\(\s*([A-Za-z_$][\w$]*)?`)
	jsCatchBare = regexp.MustCompile(`\bcatch\s*\{`)
	jsPromCatch = regexp.MustCompile(`\.catch\(\s*(?:\(?\s*([A-Za-z_$][\w$]*)?)`)
	jsErrMW     = regexp.MustCompile(`\(\s*(?:err|error|e)\s*(?::\s*[\w.<>]+)?\s*,\s*req\w*\s*(?::\s*[\w.<>]+)?\s*,\s*res\w*\s*(?::\s*[\w.<>]+)?\s*,\s*next\b`)
	nestCatch   = regexp.MustCompile(`@Catch\(([^)]*)\)`)
	reactBound  = regexp.MustCompile(`\b(componentDidCatch|getDerivedStateFromError)\s*\(|<ErrorBoundary\b|\bonError\s*[:=]`)
	statusCheck = regexp.MustCompile(`\.status(?:Code)?\s*(?:===?|!==?)\s*(\d{3})|\bcase\s+(\d{3})\s*:`)
	interceptor = regexp.MustCompile(`\binterceptors\.response\.use\(`)
	goIs        = regexp.MustCompile(`\berrors\.Is\(\s*\w+\s*,\s*([\w.]+)\s*\)|\berr\s*[!=]=\s*([\w.]*Err\w*)`)
	goAs        = regexp.MustCompile(`\berrors\.As\(\s*\w+\s*,\s*&(\w+)`)
	goRecover   = regexp.MustCompile(`\brecover\(\)`)
	cppCatch    = regexp.MustCompile(`\bcatch\s*\(\s*(?:const\s+)?([\w:]+|\.\.\.)`)
	javaCatch   = regexp.MustCompile(`\bcatch\s*\(\s*(?:final\s+)?([\w.|\s]+?)\s+\w+\s*\)`)
	javaExcH    = regexp.MustCompile(`@ExceptionHandler\(\s*\{?([^)]*)\}?\s*\)`)
)

// httpStatusNames maps a normalized status name to its code.
var httpStatusNames = map[string]int{
	"badrequest": 400, "unauthorized": 401, "paymentrequired": 402, "forbidden": 403, "notfound": 404,
	"methodnotallowed": 405, "notacceptable": 406, "requesttimeout": 408, "conflict": 409, "gone": 410,
	"preconditionfailed": 412, "requestentitytoolarge": 413, "payloadtoolarge": 413, "unsupportedmediatype": 415,
	"unprocessableentity": 422, "locked": 423, "toomanyrequests": 429, "internalservererror": 500,
	"notimplemented": 501, "badgateway": 502, "serviceunavailable": 503, "gatewaytimeout": 504,
}

// knownTypeStatus is the HTTP status common web frameworks give their own
// exception types (DRF, Django, Werkzeug, NestJS, grpc codes).
var knownTypeStatus = map[string]int{
	"ValidationError": 400, "ParseError": 400, "SuspiciousOperation": 400, "BadRequest": 400, "BadRequestException": 400,
	"AuthenticationFailed": 401, "NotAuthenticated": 401, "Unauthorized": 401, "UnauthorizedException": 401,
	"PermissionDenied": 403, "Forbidden": 403, "ForbiddenException": 403,
	"NotFound": 404, "Http404": 404, "NotFoundException": 404, "ObjectDoesNotExist": 404,
	"MethodNotAllowed": 405, "NotAcceptable": 406, "Conflict": 409, "ConflictException": 409,
	"UnsupportedMediaType": 415, "UnprocessableEntityException": 422, "Throttled": 429,
	"InternalServerError": 500, "InternalServerErrorException": 500, "ServiceUnavailable": 503,
}

// catchAllTypes catch (nearly) everything in their language.
var catchAllTypes = map[string]bool{
	"Exception": true, "BaseException": true, "Throwable": true, "std::exception": true, "exception": true,
	"Error": true, "RuntimeException": true, "...": true,
}

func statusFromName(n string) int {
	n = strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(n, "Status"), "_", ""))
	return httpStatusNames[n]
}

func errTypeBase(t string) string {
	t = strings.TrimSpace(t)
	if i := strings.LastIndex(t, "::"); i >= 0 && !strings.HasSuffix(t, "::") {
		t = t[i+2:]
	}
	if i := strings.LastIndex(t, "."); i >= 0 {
		t = t[i+1:]
	}
	return t
}

// errScan is everything found in the files read.
type errScan struct {
	sites    []ErrorSite
	handlers []ErrorHandler
	types    map[string]*ErrorType // repo + "\x00" + bare type
	drfFns   map[string]bool       // functions named by EXCEPTION_HANDLER
}

func scanErrors(s Store, repo, ref string) *errScan {
	sc := &errScan{types: map[string]*ErrorType{}, drfFns: map[string]bool{}}
	for _, rp := range reposFor(s, repo) {
		for _, f := range s.Files(rp, ref) {
			lang := errLang(f.Path)
			if lang == "" {
				continue
			}
			scanErrorFile(sc, rp, f, lang)
		}
	}
	// A site raising a class declared with a status (DRF status_code) or
	// deriving from one gets that status.
	for i := range sc.sites {
		x := &sc.sites[i]
		if x.Status != 0 || x.Type == "" {
			continue
		}
		for _, t := range append([]string{errTypeBase(x.Type)}, sc.ancestors(x.Repo, errTypeBase(x.Type))...) {
			if et := sc.types[x.Repo+"\x00"+t]; et != nil && et.Status != 0 {
				x.Status = et.Status
				break
			}
			if st := knownTypeStatus[t]; st != 0 {
				x.Status = st
				break
			}
		}
	}
	// A function named by DRF's EXCEPTION_HANDLER handles every API exception.
	for i := range sc.handlers {
		h := &sc.handlers[i]
		if h.Kind == "def_exc_context" {
			h.Kind = "drf_exception_handler"
			if !sc.drfFns[h.In] && len(sc.drfFns) > 0 {
				h.Kind = "exception_handler"
			}
		}
	}
	return sc
}

func (sc *errScan) typ(repo, name string) *ErrorType {
	k := repo + "\x00" + name
	t := sc.types[k]
	if t == nil {
		t = &ErrorType{Repo: repo, Type: name}
		sc.types[k] = t
	}
	return t
}

// statement joins line i with its continuation lines (an unclosed paren or
// bracket), at most 8 lines, so a multi-line raise/throw keeps its message.
func statement(lines []string, i int) (string, []string) {
	text := lines[i]
	depth := strings.Count(text, "(") + strings.Count(text, "[") + strings.Count(text, "{") -
		strings.Count(text, ")") - strings.Count(text, "]") - strings.Count(text, "}")
	parts := []string{text}
	for j := i + 1; depth > 0 && j < len(lines) && j < i+8; j++ {
		l := lines[j]
		parts = append(parts, l)
		depth += strings.Count(l, "(") + strings.Count(l, "[") + strings.Count(l, "{") -
			strings.Count(l, ")") - strings.Count(l, "]") - strings.Count(l, "}")
	}
	return strings.Join(parts, "\n"), parts
}

func isCommentLine(t, lang string) bool {
	switch lang {
	case "py":
		return strings.HasPrefix(t, "#")
	}
	return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*")
}

func scanErrorFile(sc *errScan, repo string, f File, lang string) {
	lines := strings.Split(f.Content, "\n")
	test := IsTestPath(f.Path)
	ll := litLang(lang)
	for i := 0; i < len(lines); i++ {
		code := lines[i]
		t := strings.TrimSpace(code)
		if t == "" || isCommentLine(t, lang) {
			continue
		}
		site := func(kind, typ string) {
			stmt, parts := statement(lines, i)
			var lits []string
			for _, p := range parts {
				l, cols := stringLiterals(p, ll)
				for k, lit := range l {
					// A subscript key (args["type"]) is not a message.
					if c := cols[k]; c > 0 && p[c-1] == '[' {
						continue
					}
					lits = append(lits, lit)
				}
			}
			keys := map[string]bool{}
			for _, m := range dictKey.FindAllStringSubmatch(stmt, -1) {
				keys[m[1]] = true
			}
			var msg []string
			for _, l := range lits {
				if keys[l] || len(strings.TrimSpace(l)) < 2 {
					continue
				}
				msg = append(msg, l)
			}
			lits = msg // dict keys ("detail", "error") are structure, not message
			st := 0
			if m := statusKwarg.FindStringSubmatch(stmt); m != nil {
				for _, g := range m[1:] {
					if g != "" {
						st, _ = strconv.Atoi(g)
						break
					}
				}
			}
			if st == 0 {
				if m := httpNamed.FindStringSubmatch(stmt); m != nil {
					st = statusFromName(m[1])
				}
			}
			if st == 0 && typ != "" {
				st = knownTypeStatus[errTypeBase(typ)]
			}
			if kind == "http_response" && st != 0 && st < 400 {
				return // a success response
			}
			if kind == "http_response" && st == 0 {
				return // no error status visible: not an error response
			}
			if m := grpcCode.FindStringSubmatch(stmt); m != nil && typ != "" && strings.HasPrefix(typ, "status.") {
				typ = "codes." + m[1]
			}
			sc.sites = append(sc.sites, ErrorSite{Repo: repo, Path: f.Path, Line: i + 1, In: enclosing(f.Symbols, i+1),
				Kind: kind, Type: typ, Status: st, Message: strings.Join(msg, " … "), Text: strings.TrimSpace(code),
				Test: test, lang: lang, lits: lits})
			if typ != "" {
				sc.typ(repo, errTypeBase(typ)).Produced++
			}
		}
		handler := func(kind string, types []string) {
			var clean []string
			for _, x := range types {
				x = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(x), ".class"))
				if x != "" {
					clean = append(clean, x)
				}
			}
			sc.handlers = append(sc.handlers, ErrorHandler{Repo: repo, Path: f.Path, Line: i + 1, In: enclosing(f.Symbols, i+1),
				Kind: kind, Types: clean, Text: strings.TrimSpace(code), Test: test, lang: lang})
			for _, x := range clean {
				sc.typ(repo, errTypeBase(x)).Handled++
			}
		}
		classDef := func(name string, bases []string) {
			et := sc.typ(repo, name)
			if et.Defined == "" {
				et.Defined = fmt.Sprintf("%s:%d", f.Path, i+1)
				for _, b := range bases {
					b = strings.TrimSpace(b)
					for _, kw := range []string{"public ", "private ", "protected ", "virtual "} {
						b = strings.TrimSpace(strings.TrimPrefix(b, kw))
					}
					if b != "" && !strings.Contains(b, "=") && b != "object" {
						et.Bases = append(et.Bases, b)
					}
				}
			}
		}
		switch lang {
		case "py":
			if m := pyRaise.FindStringSubmatch(code); m != nil && isTypeName(m[1]) {
				site("raise", m[1])
			} else if strings.Contains(code, "(") && pyHTTPResp.MatchString(code) {
				m := pyHTTPResp.FindStringSubmatch(code)
				kind, typ := "http_response", ""
				if st := knownHTTPResponseClass(m[1]); st > 0 {
					typ = m[1]
				}
				site(kind, typ)
			} else if pyLogErr.MatchString(code) {
				site("log_error", "")
			}
			if m := pyErrClass.FindStringSubmatch(code); m != nil {
				classDef(m[1], strings.Split(m[2], ","))
				// DRF/Django-style class attributes carrying the default
				// message and status, within the next few lines.
				for j := i + 1; j < len(lines) && j < i+12; j++ {
					lj := lines[j]
					if strings.TrimSpace(lj) != "" && !strings.HasPrefix(lj, " ") && !strings.HasPrefix(lj, "\t") {
						break
					}
					if mm := pyStatusAttr.FindStringSubmatch(lj); mm != nil {
						sc.typ(repo, m[1]).Status, _ = strconv.Atoi(mm[1])
					}
					if pyDefault.MatchString(lj) {
						if l, _ := stringLiterals(lj, "py"); len(l) > 0 {
							et := sc.typ(repo, m[1])
							et.Default = l[0]
							sc.sites = append(sc.sites, ErrorSite{Repo: repo, Path: f.Path, Line: j + 1, In: m[1],
								Kind: "error_class", Type: m[1], Message: l[0], Text: strings.TrimSpace(lj), Test: test, lang: lang, lits: l})
						}
					}
				}
			}
			if m := pyExcept.FindStringSubmatch(code); m != nil {
				switch {
				case m[1] != "":
					handler("except", strings.Split(m[1], ","))
				case m[2] != "":
					handler("except", []string{m[2]})
				default:
					handler("except", nil)
				}
			}
			if m := pyExcHandle.FindStringSubmatch(code); m != nil {
				if st, err := strconv.Atoi(m[1]); err == nil {
					handler("exception_handler", []string{fmt.Sprintf("HTTP %d", st)})
				} else {
					handler("exception_handler", []string{m[1]})
				}
			}
			if m := drfSetting.FindStringSubmatch(code); m != nil {
				sc.drfFns[errTypeBase(m[1])] = true
			}
			if drfHandler.MatchString(code) {
				handler("def_exc_context", nil)
			}
			if djMiddle.MatchString(code) {
				handler("error_middleware", nil)
			}
		case "js":
			switch {
			case jsThrow.MatchString(code):
				site("throw", jsThrow.FindStringSubmatch(code)[1])
			case jsReject.MatchString(code):
				site("throw", jsReject.FindStringSubmatch(code)[1])
			case newErrType.MatchString(code):
				site("error_value", newErrType.FindStringSubmatch(code)[1])
			case jsStatus.MatchString(code) && !statusCheck.MatchString(code):
				site("http_response", "")
			case jsLogErr.MatchString(code):
				site("log_error", "")
			}
			if m := jsClass.FindStringSubmatch(code); m != nil {
				classDef(m[1], []string{m[2]})
			}
			if m := jsCatch.FindStringSubmatch(code); m != nil && !strings.Contains(code, ".catch(") {
				handler("catch", instanceofTypes(lines, i, m[1]))
			} else if jsCatchBare.MatchString(code) {
				handler("catch", nil)
			} else if m := jsPromCatch.FindStringSubmatch(code); m != nil {
				handler("promise_catch", instanceofTypes(lines, i, m[1]))
			}
			if jsErrMW.MatchString(code) {
				handler("error_middleware", nil)
			}
			if m := nestCatch.FindStringSubmatch(code); m != nil {
				handler("exception_filter", strings.Split(m[1], ","))
			}
			if reactBound.MatchString(code) {
				handler("error_boundary", nil)
			}
			if m := statusCheck.FindStringSubmatch(code); m != nil {
				st := m[1]
				if st == "" {
					st = m[2]
				}
				if n, _ := strconv.Atoi(st); n >= 400 {
					handler("status_check", []string{"HTTP " + st})
				}
			}
			if interceptor.MatchString(code) {
				handler("http_interceptor", nil)
			}
		case "go":
			switch {
			case goSentinel.MatchString(code):
				site("error_value", goSentinel.FindStringSubmatch(code)[1])
			case goErrNew.MatchString(code):
				site("error_value", goErrNew.FindStringSubmatch(code)[1])
			case goHTTPErr.MatchString(code):
				site("http_response", "")
			case goRetSentinel.MatchString(code):
				site("returns", goRetSentinel.FindStringSubmatch(code)[1])
			case goPanic.MatchString(code):
				site("panic", "panic")
			case goLogErr.MatchString(code):
				site("log_error", "")
			}
			if m := goIs.FindStringSubmatch(code); m != nil {
				x := m[1]
				if x == "" {
					x = m[2]
				}
				if x != "nil" {
					handler("errors_is", []string{x})
				}
			}
			if m := goAs.FindStringSubmatch(code); m != nil {
				handler("errors_as", []string{goVarType(lines, i, m[1])})
			}
			if goRecover.MatchString(code) {
				handler("recover", []string{"panic"})
			}
		case "cpp":
			switch {
			case cppThrow.MatchString(code):
				site("throw", cppThrow.FindStringSubmatch(code)[1])
			case rosLogErr.MatchString(code):
				site("log_error", "")
			}
			if m := cppClass.FindStringSubmatch(code); m != nil && !strings.Contains(m[2], "(") {
				classDef(m[1], strings.Split(m[2], ","))
			}
			if m := cppCatch.FindStringSubmatch(code); m != nil {
				if m[1] == "..." {
					handler("catch", nil)
				} else {
					handler("catch", []string{m[1]})
				}
			}
		case "java":
			switch {
			case javaRespStat.MatchString(code):
				site("http_response", "")
			case javaThrow.MatchString(code):
				site("throw", javaThrow.FindStringSubmatch(code)[1])
			case javaLogErr.MatchString(code):
				site("log_error", "")
			}
			if m := javaClass.FindStringSubmatch(code); m != nil {
				classDef(m[1], []string{m[2]})
			}
			if m := javaCatch.FindStringSubmatch(code); m != nil {
				handler("catch", strings.Split(m[1], "|"))
			}
			if m := javaExcH.FindStringSubmatch(code); m != nil {
				handler("exception_handler", strings.Split(m[1], ","))
			}
		case "rust":
			switch {
			case rustPanic.MatchString(code):
				site("panic", "panic")
			case rustErr.MatchString(code):
				site("error_value", rustErr.FindStringSubmatch(code)[1])
			case rustLogErr.MatchString(code):
				site("log_error", "")
			}
		}
	}
}

func knownHTTPResponseClass(name string) int {
	switch name {
	case "HttpResponseBadRequest":
		return 400
	case "HttpResponseForbidden":
		return 403
	case "HttpResponseNotFound":
		return 404
	case "HttpResponseNotAllowed":
		return 405
	case "HttpResponseGone":
		return 410
	case "HttpResponseServerError":
		return 500
	}
	return 0
}

// instanceofTypes reads the types a JS catch narrows to (`e instanceof X`)
// in the next lines of its block; none = it catches everything.
func instanceofTypes(lines []string, i int, v string) []string {
	if v == "" {
		return nil
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\s+instanceof\s+([\w$.]+)|\b` + regexp.QuoteMeta(v) + `\.name\s*===?\s*["'](\w+)["']`)
	var out []string
	for j := i; j < len(lines) && j < i+15; j++ {
		for _, m := range re.FindAllStringSubmatch(lines[j], -1) {
			if m[1] != "" {
				out = append(out, m[1])
			} else {
				out = append(out, m[2])
			}
		}
	}
	return out
}

// goVarType finds the declared type of an errors.As target in the lines above.
func goVarType(lines []string, i int, v string) string {
	re := regexp.MustCompile(`\bvar\s+` + regexp.QuoteMeta(v) + `\s+\*?([\w.]+)|\b` + regexp.QuoteMeta(v) + `\s*:=\s*&?([\w.]+)\{`)
	for j := i; j >= 0 && j > i-15; j-- {
		if m := re.FindStringSubmatch(lines[j]); m != nil {
			if m[1] != "" {
				return m[1]
			}
			return m[2]
		}
	}
	return v
}

// ancestors returns t's base classes, transitively (bare names).
func (sc *errScan) ancestors(repo, t string) []string {
	var out []string
	seen := map[string]bool{t: true}
	queue := []string{t}
	for len(queue) > 0 && len(out) < 20 {
		cur := queue[0]
		queue = queue[1:]
		et := sc.types[repo+"\x00"+cur]
		if et == nil {
			continue
		}
		for _, b := range et.Bases {
			bb := errTypeBase(b)
			if !seen[bb] {
				seen[bb] = true
				out = append(out, bb)
				queue = append(queue, bb)
			}
		}
	}
	return out
}

// Errors answers for one message / type / status, or catalogues errors when
// q is empty.
func Errors(s Store, repo, ref, q string, budget int) ErrorsResult {
	if budget <= 0 {
		budget = DefaultErrorsBudget
	}
	q = strings.TrimSpace(q)
	res := ErrorsResult{Query: q, Meta: NewMeta(s, repo, ref)}
	sc := scanErrors(s, repo, ref)
	if q == "" {
		return errorCatalogue(sc, res, budget)
	}

	typeHint, msg := "", q
	if m := regexp.MustCompile(`^([A-Za-z_][\w.:]*(?:Error|Exception|Exc|Fault)\w*)\s*:\s*(.+)$`).FindStringSubmatch(q); m != nil {
		typeHint, msg = errTypeBase(m[1]), m[2]
	}
	status := 0
	if regexp.MustCompile(`^\d{3}$`).MatchString(q) {
		status, _ = strconv.Atoi(q)
	} else if m := regexp.MustCompile(`(?i)^(?:http\s*)?(\d{3})\b`).FindStringSubmatch(q); m != nil && len(q) <= 40 {
		status, _ = strconv.Atoi(m[1])
	}
	ident := regexp.MustCompile(`^[A-Za-z_][\w.:]*$`).MatchString(q)

	var matched []ErrorSite
	byType := map[string]bool{}
	for _, x := range sc.sites {
		switch {
		case status > 0 && x.Status == status:
			x.Match = "status"
		case ident && x.Type != "" && (errTypeBase(x.Type) == errTypeBase(q) || x.Type == q):
			x.Match = "type"
		case ident && containsStr(x.lits, q):
			x.Match = "code"
		default:
			if fixed := matchMessage(x, msg); fixed > 0 {
				x.Fixed, x.Match = fixed, "message"
				if typeHint != "" && errTypeBase(x.Type) == typeHint {
					x.Fixed += len(typeHint)
				}
			} else {
				continue
			}
		}
		// A class's default message or a package-level sentinel stands for
		// every place that raises/returns it.
		if x.Kind == "error_class" || x.lang == "go" && x.In == "" && x.Type != "" {
			byType[x.Repo+"\x00"+x.Type] = true
		}
		matched = append(matched, x)
	}
	// A matched default message (DRF default_detail) means every bare raise of
	// that class produces it; a matched sentinel, every return of it.
	if len(byType) > 0 {
		listed := map[string]bool{}
		for _, x := range matched {
			listed[fmt.Sprintf("%s\x00%s\x00%d", x.Repo, x.Path, x.Line)] = true
		}
		for _, x := range sc.sites {
			if listed[fmt.Sprintf("%s\x00%s\x00%d", x.Repo, x.Path, x.Line)] {
				continue
			}
			if x.Kind != "error_class" && x.Type != "" && byType[x.Repo+"\x00"+errTypeBase(x.Type)] && x.Message == "" {
				x.Match = "default_message"
				matched = append(matched, x)
			}
		}
	}
	// A template matching only a fragment of what the best one matched is a
	// coincidence ("does not exist"), not another producer.
	best := 0
	for _, x := range matched {
		if x.Match == "message" && x.Fixed > best {
			best = x.Fixed
		}
	}
	if best >= 12 {
		kept := matched[:0]
		for _, x := range matched {
			if x.Match != "message" || 2*x.Fixed >= best {
				kept = append(kept, x)
			}
		}
		matched = kept
	}
	switch {
	case status > 0:
		res.Mode = "status"
	case ident && len(matched) > 0 && matched[0].Match != "message":
		res.Mode = "type"
	default:
		res.Mode = "message"
	}
	sort.SliceStable(matched, func(i, j int) bool {
		a, b := matched[i], matched[j]
		if ra, rb := matchRank(a.Match), matchRank(b.Match); ra != rb {
			return ra > rb
		}
		if a.Test != b.Test {
			return !a.Test
		}
		if sa, sb := siteScore(a), siteScore(b); sa != sb {
			return sa > sb
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	res.Total = len(matched)

	// Budget the producers, attaching one hop of callers to each.
	spent := 30
	snaps := map[string]RefSnapshot{}
	syms := map[string]map[string]SymbolRef{}
	idx := map[string]baseIndex{}
	for _, x := range matched {
		if _, ok := snaps[x.Repo]; !ok {
			snaps[x.Repo] = s.Snapshot(x.Repo, ref)
			syms[x.Repo] = s.SymbolsAt(x.Repo, ref)
		}
		if idx[x.Repo] == nil {
			idx[x.Repo] = baseIndexOf(syms[x.Repo])
		}
		x.Handle = siteHandleIn(idx[x.Repo], x.Path, x.In)
		if x.Handle != "" {
			callers := callersOf(snaps[x.Repo], x.Handle)
			if len(callers) > 5 {
				x.MoreCalls = len(callers) - 5
				callers = callers[:5]
			}
			x.Callers = callers
		}
		cost := estSite(x)
		if spent+cost > budget*2/3 && len(res.Sites) > 0 {
			res.Truncated = true
			break
		}
		spent += cost
		res.Sites = append(res.Sites, x)
	}

	// Handlers for the listed producers.
	res.Handlers = handlersFor(sc, res.Sites, budget-spent, &res.Truncated)
	if res.Truncated {
		res.Next = fmt.Sprintf("%d of %d producers shown; re-run with --budget %d (MCP: budget=%d), or narrow with the exact message/type", len(res.Sites), res.Total, budget*2, budget*2)
	}
	res.Data = errorDataDefs(s, repo, ref, q)
	switch {
	case res.Total == 0 && len(res.Data) > 0:
		res.Note = "no raise/throw/error value/HTTP error response produces this text directly, but it is DEFINED as data (see data): the code reads that catalogue at runtime and raises/reports the entry by key or code — follow loaded_by into the code, then search for the entry's key/code"
	case res.Total == 0:
		res.Note = "no raise/throw/error value/HTTP error response at this ref produces this, and no JSON/YAML catalogue defines it — it may come from a dependency, another repo or version, or be built dynamically"
	default:
		res.Note = "producers ranked: type/status/code match, then message (fixed template text matched); callers = one hop up the call graph; handlers: on_path (same function or a direct caller) first, then exact type, base type, framework-level, catch-all"
	}
	return res
}

func matchRank(m string) int {
	switch m {
	case "type", "status", "code":
		return 3
	case "message", "default_message":
		return 1
	}
	return 0
}

// siteScore orders producers within a rank: more matched text first; a
// raise/throw/HTTP response before a mere error log.
func siteScore(x ErrorSite) int {
	sc := 2 * x.Fixed
	switch x.Kind {
	case "raise", "throw", "http_response", "error_value", "panic", "error_class", "returns":
		sc += 3
	}
	return sc
}

// matchMessage is the fixed text of one of the site's message templates that
// matches msg (0 = no match), with logsite's placeholder rules.
func matchMessage(x ErrorSite, msg string) int {
	if len(x.lits) == 0 || len(msg) < 3 {
		return 0
	}
	cands := make([][]string, 0, len(x.lits)+1)
	if len(x.lits) > 1 {
		cands = append(cands, x.lits)
	}
	for _, l := range x.lits {
		cands = append(cands, []string{l})
	}
	best := 0
	ll := litLang(x.lang)
	for _, c := range cands {
		re, fixed, longest := formatPattern(c, ll)
		if re == nil || fixed < 6 || !strings.Contains(msg, longest) {
			continue
		}
		if re.MatchString(msg) && fixed > best {
			best = fixed
		}
	}
	return best
}

// siteHandle resolves the enclosing symbol of a site to a stored id: the
// symbol in the file's package with that bare name (a unique one).
func siteHandle(syms map[string]SymbolRef, path, in string) string {
	return siteHandleIn(baseIndexOf(syms), path, in)
}

// baseIndex maps a bare symbol name to the qualified ids defining it, so a
// lookup by name is a map read rather than a scan of every symbol. Build it
// once per answer and reuse it for every site.
type baseIndex map[string][]string

func baseIndexOf(syms map[string]SymbolRef) baseIndex {
	idx := baseIndex{}
	for k := range syms {
		b := baseName(k)
		idx[b] = append(idx[b], k)
	}
	return idx
}

func siteHandleIn(idx baseIndex, path, in string) string {
	if in == "" {
		return ""
	}
	pkg := pkgFromPath(path)
	var cands []string
	for _, k := range idx[in] {
		if pkg == "" && !strings.Contains(k, "/") || pkg != "" && strings.HasPrefix(k, pkg+".") {
			cands = append(cands, k)
		}
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Strings(cands)
	return cands[0]
}

func callersOf(snap RefSnapshot, qid string) []string {
	var out []string
	for name, cs := range snap.Callees {
		for _, c := range cs {
			if c.Name == qid {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func estSite(x ErrorSite) int {
	n := 20 + estTokens(x.Path) + estTokens(x.Text) + estTokens(x.Message) + estTokens(x.Handle)
	for _, c := range x.Callers {
		n += estTokens(c)
	}
	return n
}

// handlersFor ranks the handlers that can catch any of sites.
func handlersFor(sc *errScan, sites []ErrorSite, budget int, truncated *bool) []ErrorHandler {
	type key struct {
		repo, path string
		line       int
	}
	best := map[key]ErrorHandler{}
	rank := func(h ErrorHandler) int {
		r := 0
		switch {
		case h.Match == "exact":
			r = 50
		case strings.HasPrefix(h.Match, "base:"):
			r = 40
		case h.Match == "status":
			r = 35
		case h.Kind == "drf_exception_handler" || h.Kind == "exception_handler" || h.Kind == "error_middleware" ||
			h.Kind == "exception_filter" || h.Kind == "http_interceptor" || h.Kind == "error_boundary":
			r = 30
		default:
			r = 10
		}
		if h.OnPath {
			r += 100
		}
		if h.Test {
			r -= 60
		}
		return r
	}
	for _, x := range sites {
		if x.Kind == "log_error" {
			continue // logging raises nothing
		}
		onPath := map[string]bool{}
		if x.In != "" {
			onPath[x.In] = true
		}
		for _, c := range x.Callers {
			onPath[baseName(c)] = true
		}
		bt := errTypeBase(x.Type)
		anc := map[string]bool{}
		for _, a := range sc.ancestors(x.Repo, bt) {
			anc[a] = true
		}
		for _, h := range sc.handlers {
			if !compatibleLang(x.lang, h.lang) {
				continue
			}
			match := ""
			for _, t := range h.Types {
				tt := errTypeBase(t)
				switch {
				case bt != "" && tt == bt:
					match = "exact"
				case strings.HasPrefix(t, "HTTP ") && x.Status > 0 && t == fmt.Sprintf("HTTP %d", x.Status):
					match = "status"
				case anc[tt] && !catchAllTypes[t] && !catchAllTypes[tt]:
					if match == "" {
						match = "base:" + tt
					}
				case catchAllTypes[t] || catchAllTypes[tt]:
					if match == "" {
						match = "catch_all"
					}
				}
			}
			if len(h.Types) == 0 {
				match = "catch_all"
			}
			if match == "" {
				continue
			}
			// HTTP responses are handled on the client (status checks,
			// interceptors) or by the framework, not by except/catch.
			if x.Kind == "http_response" && match == "catch_all" && h.Kind != "http_interceptor" && h.Kind != "error_middleware" {
				continue
			}
			if x.Kind == "panic" && h.Kind != "recover" && x.lang == "go" {
				continue
			}
			if x.Kind != "panic" && h.Kind == "recover" {
				continue
			}
			sameRepo := h.Repo == x.Repo
			h.OnPath = sameRepo && onPath[h.In]
			// A catch-all far from the producer says little: keep it only on
			// the path, or when it is a framework-level handler.
			if match == "catch_all" && !h.OnPath && rank(ErrorHandler{Kind: h.Kind, Match: match}) < 30 {
				continue
			}
			if match == "catch_all" && !sameRepo {
				continue
			}
			h.Match = match
			k := key{h.Repo, h.Path, h.Line}
			if old, ok := best[k]; !ok || rank(h) > rank(old) {
				best[k] = h
			}
		}
	}
	out := make([]ErrorHandler, 0, len(best))
	for _, h := range best {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra > rb
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	spent := 0
	for i, h := range out {
		cost := 14 + estTokens(h.Path) + estTokens(h.Text)
		if spent+cost > budget && i > 0 {
			*truncated = true
			return out[:i]
		}
		spent += cost
	}
	return out
}

// compatibleLang: a JS handler cannot catch a Python raise, but a frontend
// status check does handle a backend HTTP response — the caller decides that.
func compatibleLang(site, h string) bool {
	return site == h || site == "" || h == "js"
}

// errorCatalogue lists each repo's error types and messages, budgeted.
func errorCatalogue(sc *errScan, res ErrorsResult, budget int) ErrorsResult {
	res.Mode = "catalogue"
	errish := func(t *ErrorType) bool {
		if isErrName(t.Type) {
			return true
		}
		for _, b := range sc.ancestors(t.Repo, t.Type) {
			if isErrName(b) {
				return true
			}
		}
		for _, b := range t.Bases {
			if isErrName(errTypeBase(b)) {
				return true
			}
		}
		return false
	}
	var types []ErrorType
	for _, t := range sc.types {
		if !errish(t) || t.Produced == 0 && t.Handled == 0 && t.Defined == "" {
			continue
		}
		if t.Produced == 0 && t.Defined == "" {
			continue // only ever caught here (a library type)
		}
		if t.Status == 0 {
			t.Status = knownTypeStatus[t.Type]
		}
		types = append(types, *t)
	}
	sort.Slice(types, func(i, j int) bool {
		a, b := types[i], types[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Produced != b.Produced {
			return a.Produced > b.Produced
		}
		return a.Type < b.Type
	})
	// Producers worth listing: those with a message, non-test, non-log first.
	var sites []ErrorSite
	for _, x := range sc.sites {
		if x.Message == "" || x.Kind == "log_error" {
			continue
		}
		sites = append(sites, x)
	}
	sort.SliceStable(sites, func(i, j int) bool {
		a, b := sites[i], sites[j]
		if a.Test != b.Test {
			return !a.Test
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	res.Total = len(sc.sites)
	spent := 30
	half := budget / 2
	for _, t := range types {
		cost := 12 + estTokens(t.Type) + estTokens(t.Defined) + estTokens(t.Default) + 3*len(t.Bases)
		if spent+cost > half && len(res.Types) > 0 {
			res.Truncated = true
			break
		}
		spent += cost
		res.Types = append(res.Types, t)
	}
	for _, x := range sites {
		cost := 12 + estTokens(x.Path) + estTokens(x.Message) + estTokens(x.Type)
		if spent+cost > budget && len(res.Sites) > 0 {
			res.Truncated = true
			break
		}
		spent += cost
		x.Text = "" // the message is the point; the line is one drill-in away
		res.Sites = append(res.Sites, x)
	}
	if res.Truncated {
		res.Next = fmt.Sprintf("%d of %d types and %d of %d messages shown; re-run with --budget %d, scope with --repo, or pass a message/type/status to drill in",
			len(res.Types), len(types), len(res.Sites), len(sites), budget*2)
	}
	res.Note = "error catalogue: types (produced = raise/throw/constructor sites, handled = except/catch naming it; status = HTTP status it maps to) and producing sites with a message"
	return res
}

func isErrName(n string) bool {
	for _, suf := range []string{"Error", "Exception", "Exc", "Fault", "Failure", "Err"} {
		if strings.HasSuffix(n, suf) {
			return true
		}
	}
	// Go sentinels: ErrNotFound.
	return len(n) > 3 && strings.HasPrefix(n, "Err") && n[3] >= 'A' && n[3] <= 'Z'
}

// isTypeName: `raise X` names a class, not a re-raised variable (`raise e`,
// `raise exc`): a dotted name or one starting upper-case.
func isTypeName(n string) bool {
	return n != "" && (strings.Contains(n, ".") || n[0] >= 'A' && n[0] <= 'Z')
}

// roscomms.go implements reponite_topics — protocol-aware ROS communication
// edges. A ROS system's real graph is not its call graph: a publisher and a
// subscriber live in different processes and are joined only by a topic *string*
// resolved by the middleware at runtime. So the source call graph (§6) can never
// tell you that node A's publish to "/cmd_vel" is consumed by node B — the edge
// simply isn't in anyone's code. This layer recovers that graph by scanning for
// the pub/sub/service/action idioms of roscpp/rospy/rclcpp/rclpy, extracting the
// name each endpoint binds to, and linking producers to consumers by that name
// across the whole fleet. Pure and stdlib-only (Go regexp) over the file content
// the Store already holds — zero new indexing, like grep/usages — and unit-tested
// in-sandbox (ADR-018). Honest by construction: the link is a name-string match
// (medium confidence), and launch-file/namespace remapping is NOT resolved, so
// the output says so rather than overclaiming a wire connection.
package query

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/vishwak02/reponite/internal/roslaunch"
)

// Communication roles. A ROS endpoint is one of these; producers and consumers
// of the same name (within a family) are the two ends of a runtime edge.
const (
	RolePublisher     = "publisher"
	RoleSubscriber    = "subscriber"
	RoleServiceServer = "service_server"
	RoleServiceClient = "service_client"
	RoleActionServer  = "action_server"
	RoleActionClient  = "action_client"
	// RoleLaunchDeclared is a binding seen only in a launch file: a `<remap>`
	// on a launched node whose source does not show the endpoint (third-party
	// package, or a name built by an expression). The direction is unknown.
	RoleLaunchDeclared = "launch_declared"
)

// CommEndpoint is one ROS communication endpoint found in source: a
// publisher/subscriber/service/action bound to a named topic/service/action.
// It is the runtime-graph analogue of a Callee — but for an edge the call graph
// can't see, because the far end lives in another process.
type CommEndpoint struct {
	Repo    string
	Path    string
	Line    int
	Role    string // RolePublisher, RoleSubscriber, ...
	Name    string // topic/service/action name, normalized (leading "/" stripped)
	Raw     string // the name exactly as written in source (e.g. "/cmd_vel")
	MsgType string // message/service/action type, when captured; "" otherwise
	// MsgTypeSource labels how MsgType was inferred — "template" (C++ <T>),
	// "callback-param" (ROS1 roscpp subscribe() carries no template; the type
	// is recovered from the callback's message parameter, e.g. const
	// T::ConstPtr&), or "positional-arg" (rospy/rclpy pass the type class
	// positionally). Best-effort inference, labeled so it is never mistaken
	// for a declared type ("never lie").
	MsgTypeSource string
	// Effective is the topic name at RUNTIME, after launch-file remapping
	// (§8D.4). It equals Name when nothing remaps it. Remapping is what makes a
	// node whose source says `scan` actually receive `raw_scan_front`.
	Effective string
	// NameResolution labels how Effective was determined: as-written,
	// launch-remapped, or launch-ambiguous (several launch files disagree, so
	// no single answer is asserted).
	NameResolution string
	// RemapVia cites the launch file responsible, or lists the competing
	// targets when ambiguous.
	RemapVia string
	In       string // enclosing symbol (the node/callback), for a hop back into the call graph
	Text     string // the source line, trimmed
	// Param is the ROS parameter the name was read from when the source
	// passes a variable, not a literal (NameResolution then reads
	// "param:<name>=<value> (<file:line>)"), and NameConfidence how surely
	// that value is the runtime one — always below a literal's. Zero for a
	// literal name.
	Param          string
	NameConfidence float64

	expr   string     // the non-literal name expression, pending resolution
	inline *paramRead // a param read written inline as the name argument
	recv   string     // the node handle the call is made on (nh in nh.advertise), for its namespace
}

// CommGroup is every endpoint bound to one name within a family (topic / service
// / action): the producers on one side, the consumers on the other. When both
// sides are non-empty it is a resolved cross-process edge.
type CommGroup struct {
	Family    string // "topic" | "service" | "action"
	Name      string // normalized name
	Producers []CommEndpoint
	Consumers []CommEndpoint
	// Declared are launch-file bindings whose node source shows no endpoint
	// for the name: the node is wired to it at runtime, direction unknown. A
	// group with no producers but a declared binding is NOT "no producer" —
	// the producer may well be that node.
	Declared   []CommEndpoint
	Confidence float64 // name-string linkage: medium (0.75), bumped when a captured type matches
}

// Connected reports whether this group is a real edge (has both ends).
func (g CommGroup) Connected() bool { return len(g.Producers) > 0 && len(g.Consumers) > 0 }

// CommGraphResult is the ROS communication graph over a repo/fleet at a ref.
type CommGraphResult struct {
	Groups     []CommGroup
	Endpoints  int // total endpoints discovered
	Unresolved int // pub/sub/etc. calls whose name was not a string literal (dynamic; not linkable)
	// LaunchFiles is how many launch files informed the remapping, Remapped how
	// many endpoints a launch file rewrote, and UnresolvedIncludes the
	// <include> directives whose $(find …) substitution was not expanded — so a
	// reader knows how complete the remap picture is (§8D.4).
	LaunchFiles        int
	Remapped           int
	UnresolvedIncludes int
	// UnexpandedRemaps counts remaps whose target kept a substitution
	// ($(find …), $(env …), an $(arg …) with no default), so the runtime name is
	// unknown and the remap was NOT applied.
	UnexpandedRemaps int
	// ParamResolved counts endpoints whose name was resolved from a
	// parameter. Unbound are the endpoints whose name comes from a variable or
	// parameter that could not be resolved to a value: they cannot be linked,
	// but they are real producers/consumers, so they are listed (the full graph
	// lists all; a focused query lists those whose parameter or variable name
	// matches the topic) instead of vanishing.
	ParamResolved int
	// ArgResolved counts endpoints whose name a caller passed in (a library
	// publisher's topic argument, its node handle's namespace), resolved by
	// following the arguments back through the call sites (rosargs.go).
	ArgResolved int
	Unbound     []CommEndpoint
	// Declared counts launch-file bindings added because no indexed source
	// endpoint explains them; VizConfigs the rviz configs read as endpoints.
	Declared   int
	VizConfigs int
	Note       string
	Meta       Meta
}

// commIdiom is a client-library idiom for one role: the method-name pattern to
// look for, the languages it appears in, and — for the one genuinely ambiguous
// C++ token, create_client — the role to switch to when the line is an action call.
type commIdiom struct {
	role    string
	re      *regexp.Regexp
	cpp, py bool
	altRole string // if non-empty and the line mentions rclcpp_action, use this role instead
}

// The idiom table. C++ idioms are gated to C/C++ files and Python idioms to .py,
// so a JavaScript observer's `.subscribe(...)` never masquerades as a ROS edge.
// The name each idiom binds to is the FIRST quoted string in the call — true for
// every idiom, because the message type is either a C++ template (`<T>`, no
// quotes) or a Python positional identifier (no quotes), never the first literal.
var commIdioms = []commIdiom{
	// publishers
	{role: RolePublisher, re: regexp.MustCompile(`\badvertise\b`), cpp: true},                  // roscpp
	{role: RolePublisher, re: regexp.MustCompile(`\bcreate_publisher\b`), cpp: true, py: true}, // rclcpp / rclpy
	{role: RolePublisher, re: regexp.MustCompile(`\bPublisher\s*\(`), py: true},                // rospy
	// subscribers
	{role: RoleSubscriber, re: regexp.MustCompile(`\bsubscribe\b`), cpp: true},                     // roscpp
	{role: RoleSubscriber, re: regexp.MustCompile(`\bcreate_subscription\b`), cpp: true, py: true}, // rclcpp / rclpy
	{role: RoleSubscriber, re: regexp.MustCompile(`\bSubscriber\s*\(`), py: true},                  // rospy
	// service servers
	{role: RoleServiceServer, re: regexp.MustCompile(`\badvertiseService\b`), cpp: true},         // roscpp
	{role: RoleServiceServer, re: regexp.MustCompile(`\bcreate_service\b`), cpp: true, py: true}, // rclcpp / rclpy
	{role: RoleServiceServer, re: regexp.MustCompile(`\bService\s*\(`), py: true},                // rospy
	// service clients
	{role: RoleServiceClient, re: regexp.MustCompile(`\bserviceClient\b`), cpp: true},                                      // roscpp
	{role: RoleServiceClient, re: regexp.MustCompile(`\bServiceProxy\s*\(`), py: true},                                     // rospy
	{role: RoleServiceClient, re: regexp.MustCompile(`\bcreate_client\b`), cpp: true, py: true, altRole: RoleActionClient}, // rclcpp/rclpy service; action if rclcpp_action
	// actions
	{role: RoleActionServer, re: regexp.MustCompile(`\bcreate_server\b`), cpp: true}, // rclcpp_action (no service create_server exists)
	{role: RoleActionServer, re: regexp.MustCompile(`\bActionServer\s*\(`), py: true},
	{role: RoleActionClient, re: regexp.MustCompile(`\bActionClient\s*\(`), py: true},
}

var (
	firstQuotedRe = regexp.MustCompile(`["']([^"']+)["']`)
	cppTemplateRe = regexp.MustCompile(`^\s*<([^>]+)>`)

	// cppCallbackRefRe finds the callback bound in a roscpp subscribe(...) call:
	// the first &-qualified name after the topic literal (&Class::cb, this /
	// boost::bind(&Class::cb, ...) / &freeFn).
	cppCallbackRefRe = regexp.MustCompile(`&\s*([\w:]+)`)
	// cppCallbackDefRe matches a void/bool callable definition or declaration
	// with its parameter list ([^)]* spans newlines, so multi-line signatures
	// match). Group 1 = bare name, group 2 = parameters.
	cppCallbackDefRe = regexp.MustCompile(`\b(?:void|bool)\s+(?:[\w~]+\s*::\s*)*(\w+)\s*\(([^)]*)\)`)
	// The message-parameter shapes roscpp callbacks take, most specific first.
	cppMsgParamRes = []*regexp.Regexp{
		regexp.MustCompile(`([\w:]+)\s*::\s*ConstPtr`),                                     // const T::ConstPtr&
		regexp.MustCompile(`boost::shared_ptr<\s*(?:const\s+)?([\w:]+?)(?:\s+const)?\s*>`), // const boost::shared_ptr<T const>&
		regexp.MustCompile(`MessageEvent<\s*([\w:]+?)(?:\s+const)?\s*>`),                   // const ros::MessageEvent<T const>&
		regexp.MustCompile(`([\w:]+)ConstPtr\b`),                                           // typedef'd TConstPtr
		regexp.MustCompile(`const\s+([\w:]+(?:::[\w:]+)+)\s*&`),                            // const pkg::T& (needs a ::)
	}

	// Python positional message-type capture: the identifier immediately before
	// the topic literal (rclpy: create_subscription(T, "topic", …)) or the first
	// identifier argument after it (rospy: Subscriber("topic", T, cb)).
	pyTypeBeforeRe = regexp.MustCompile(`([A-Za-z_][\w.]*)\s*,\s*$`)
	pyTypeAfterRe  = regexp.MustCompile(`^\s*,\s*([A-Za-z_][\w.]*)\s*[,)]`)
)

// cppCallbackTypes maps every void/bool callable in a C++ file to the message
// type of its message-shaped parameter, if it has one. This is the ROS1 gap:
// roscpp's subscribe() usually carries NO template — the middleware infers the
// type from the callback — so the source's only record of the subscribed type
// is the callback's signature.
func cppCallbackTypes(content string) map[string]string {
	var out map[string]string
	for _, m := range cppCallbackDefRe.FindAllStringSubmatch(content, -1) {
		name, params := m[1], m[2]
		if out[name] != "" {
			continue // first definition wins (overloads: best-effort)
		}
		for _, re := range cppMsgParamRes {
			if pm := re.FindStringSubmatch(params); pm != nil {
				if out == nil {
					out = map[string]string{}
				}
				out[name] = strings.TrimSpace(pm[1])
				break
			}
		}
	}
	return out
}

// lastSegment reduces a qualified callback reference (Lidar::scanCB) to its
// bare name.
func lastSegment(s string) string {
	if i := strings.LastIndex(s, "::"); i >= 0 {
		return s[i+2:]
	}
	return s
}

// commLang classifies a path as a ROS client-library language, or "" if neither.
// commWords are substrings every comm idiom contains; a file with none of
// them has no endpoint, and is skipped without running the per-line regexes.
var commWords = []string{"advertise", "subscribe", "Publisher", "Subscriber", "create_", "Service", "serviceClient", "ActionServer", "ActionClient", "subscription"}

func mayHaveComms(content string) bool {
	for _, w := range commWords {
		if strings.Contains(content, w) {
			return true
		}
	}
	return false
}

func commLang(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".h", ".c":
		return "cpp"
	case ".py":
		return "py"
	}
	return ""
}

// scanComms extracts every ROS endpoint from one file, plus a count of
// endpoints whose name is genuinely dynamic (an expression no parameter or
// variable trace applies to — reported, not linkable). An endpoint whose name
// is a variable or an inline parameter read is returned with expr/inline set
// for CommGraph to resolve against the repo's parameter reads and launch
// files. Pure: it reads only the passed content and spans.
func scanComms(repo, path, content string, spans []SymbolSpan) (eps []CommEndpoint, unresolved int) {
	lang := commLang(path)
	if lang == "" || !mayHaveComms(content) {
		return nil, 0
	}
	var callbackTypes map[string]string // lazily built: only when a C++ endpoint needs it
	lines := strings.Split(content, "\n")
	// the code without comments and docstrings, line for line: a usage
	// example in a docstring or a commented-out publisher is not an endpoint
	blanked := content
	switch lang {
	case "py":
		blanked = blankPyDocstrings(content)
	case "cpp":
		blanked = blankJSComments(content) // the same /* */ and // syntax
	}
	codeLines := strings.Split(blanked, "\n")
	for i, line := range lines {
		src := line
		if i < len(codeLines) {
			src = codeLines[i]
		}
		code := stripComment(src, lang)
		if t := strings.TrimSpace(code); lang == "py" && (strings.HasPrefix(t, "class ") || strings.HasPrefix(t, "def ")) {
			continue // `class ServiceProxy(_Service):` declares, it does not call
		}
		for _, id := range commIdioms {
			if lang == "cpp" && !id.cpp || lang == "py" && !id.py {
				continue
			}
			loc := id.re.FindStringIndex(code)
			if loc == nil {
				continue
			}
			role := id.role
			if id.altRole != "" && strings.Contains(code, "rclcpp_action") {
				role = id.altRole
			}
			rest := code[loc[1]:]
			// C++ message type from the template immediately after the method.
			msgType, msgSource := "", ""
			if lang == "cpp" {
				if m := cppTemplateRe.FindStringSubmatch(rest); m != nil {
					msgType, msgSource = strings.TrimSpace(m[1]), "template"
				}
			}
			cbType := func(after string) {
				// ROS1 roscpp subscribe() carries no template — the type is
				// inferred by the middleware from the callback. Recover it from
				// the callback's message parameter in this file (best-effort,
				// labeled; a cross-file callback stays "", never guessed).
				if msgType != "" || lang != "cpp" || role != RoleSubscriber {
					return
				}
				if cb := cppCallbackRefRe.FindStringSubmatch(after); cb != nil {
					if callbackTypes == nil {
						callbackTypes = cppCallbackTypes(content)
					}
					if t := callbackTypes[lastSegment(cb[1])]; t != "" {
						msgType, msgSource = t, "callback-param"
					}
				}
			}
			qloc := firstQuotedRe.FindStringSubmatchIndex(rest)
			// The call's arguments, which may continue on the next lines.
			args, nameAt := callNameArg(lang, id, code, loc, lines[i+1:min(len(lines), i+8)])
			if nameAt >= 0 {
				expr, param, def, hasDef, ok := dynamicName(args[nameAt])
				if !ok && isNameConcat(args[nameAt]) {
					// topic + "_updates": built from a variable, never the literal alone.
					expr, ok = strings.TrimSpace(args[nameAt]), true
				}
				if ok {
					ep := CommEndpoint{
						Repo: repo, Path: path, Line: i + 1, Role: role, Raw: expr, Param: param,
						In: enclosing(spans, i+1), Text: strings.TrimSpace(line), expr: expr,
						recv: recvOf(code[:loc[0]]),
					}
					if param != "" {
						ep.inline = &paramRead{param: param, def: def, hasDef: hasDef, path: path, line: i + 1}
					}
					cbType(strings.Join(args[nameAt+1:], ","))
					if msgType == "" && lang == "py" {
						msgType, msgSource = pyPositionalType(args, nameAt)
					}
					ep.MsgType, ep.MsgTypeSource = msgType, msgSource
					eps = append(eps, ep)
					break
				}
			}
			var raw, after, before string
			switch {
			case qloc != nil:
				raw, after, before = rest[qloc[2]:qloc[3]], rest[qloc[1]:], rest[:qloc[0]]
			case nameAt >= 0:
				// A literal name on a continuation line of the call.
				lit, ok := stringLiteral(args[nameAt])
				if !ok || lit == "" {
					unresolved++
					break
				}
				raw, after = lit, strings.Join(args[nameAt+1:], ",")
				if nameAt > 0 {
					before = args[nameAt-1] + ","
				}
			default:
				unresolved++ // e.g. advertise<T>(topic + "/x", 10) — can't resolve the name
			}
			if raw == "" {
				break // one idiom per line
			}
			if msgType == "" {
				switch lang {
				case "cpp":
					cbType(after)
				case "py":
					// rospy/rclpy pass the type class positionally: right before the
					// topic literal (rclpy) or right after it (rospy).
					if m := pyTypeBeforeRe.FindStringSubmatch(before); m != nil {
						msgType, msgSource = m[1], "positional-arg"
					} else if m := pyTypeAfterRe.FindStringSubmatch(after); m != nil {
						msgType, msgSource = m[1], "positional-arg"
					}
				}
			}
			eps = append(eps, CommEndpoint{
				Repo: repo, Path: path, Line: i + 1, Role: role,
				Name: normalizeTopic(raw), Raw: raw, MsgType: msgType, MsgTypeSource: msgSource,
				In: enclosing(spans, i+1), Text: strings.TrimSpace(line),
			})
			break // first matching idiom wins for this line
		}
	}
	return eps, unresolved
}

// callNameArg parses the arguments of the pub/sub call matched at loc (the
// call may continue on the following lines) and returns them with the index
// of the one carrying the topic/service/action name, or -1 when the match is
// not a call (a declaration, a word in a string).
func callNameArg(lang string, id commIdiom, code string, loc []int, next []string) ([]string, int) {
	text := code[loc[0]:]
	for _, l := range next {
		text += "\n" + stripComment(l, lang)
	}
	i := loc[1] - loc[0]
	if i > 0 && text[i-1] == '(' {
		i-- // the idiom pattern consumed the paren (rospy.Publisher\s*\()
	} else {
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
		if i < len(text) && text[i] == '<' { // a C++ template argument list
			depth := 0
			for ; i < len(text); i++ {
				if text[i] == '<' {
					depth++
				} else if text[i] == '>' {
					if depth--; depth == 0 {
						i++
						break
					}
				}
			}
		}
		for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n') {
			i++
		}
	}
	if i >= len(text) || text[i] != '(' {
		return nil, -1
	}
	args, _, ok := splitCallArgs(text, i)
	if !ok || len(args) == 0 {
		return nil, -1
	}
	fn := strings.TrimRight(strings.TrimSpace(code[loc[0]:loc[1]]), "( \t")
	at := topicArgIndex(lang, "", fn, args)
	if at >= len(args) {
		return nil, -1
	}
	return args, at
}

// pyPositionalType is the message/service type class passed next to the name
// argument (rclpy: before it; rospy: after it).
func pyPositionalType(args []string, nameAt int) (string, string) {
	isType := func(a string) bool {
		a = strings.TrimSpace(a)
		return a != "" && !strings.Contains(a, "=") && identPathRe.MatchString(a) && !strings.HasPrefix(a, "self")
	}
	if nameAt > 0 && isType(args[nameAt-1]) {
		return strings.TrimSpace(args[nameAt-1]), "positional-arg"
	}
	if nameAt+1 < len(args) && isType(args[nameAt+1]) {
		return strings.TrimSpace(args[nameAt+1]), "positional-arg"
	}
	return "", ""
}

// stripComment drops a trailing line comment so commented-out pub/sub calls
// aren't reported as live edges. Topic strings never contain "//" or "#".
func stripComment(line, lang string) string {
	if lang == "cpp" {
		if i := strings.Index(line, "//"); i >= 0 {
			return line[:i]
		}
	} else if i := strings.IndexByte(line, '#'); i >= 0 {
		return line[:i]
	}
	return line
}

// normalizeTopic strips a single leading "/" so an absolute "/scan" and a
// relative "scan" link. Deeper namespace/remapping resolution is a runtime
// concern this layer deliberately does not attempt (see the result Note).
func normalizeTopic(s string) string {
	s = strings.TrimSpace(s)
	return strings.TrimPrefix(s, "/")
}

// roleFamily maps a role to its (family, isProducer) — the two ends that link.
func roleFamily(role string) (family string, producer bool) {
	switch role {
	case RolePublisher:
		return "topic", true
	case RoleSubscriber:
		return "topic", false
	case RoleServiceServer:
		return "service", true
	case RoleServiceClient:
		return "service", false
	case RoleActionServer:
		return "action", true
	case RoleActionClient:
		return "action", false
	}
	return "", false
}

// CommGraph builds the ROS communication graph across repo (FleetRepo "*" = the
// whole fleet) at ref: every publisher/subscriber/service/action endpoint,
// grouped by (family, name) so producers and consumers of the same name sit
// together as a runtime edge. A name read from a parameter is resolved through
// the parameter's launch/YAML value (rosparams.go). Pure over Store.Files, no
// new indexing; each repo's files are read once.
func CommGraph(s Store, repo, ref string) CommGraphResult {
	return cachedFact(s, repo, ref, "commgraph", "", func() CommGraphResult { return commGraph(s, repo, ref) })
}

func commGraph(s Store, repo, ref string) CommGraphResult {
	res := CommGraphResult{Meta: NewMeta(s, repo, ref)}
	type key struct{ family, name string }
	groups := map[key]*CommGroup{}
	var launch, yaml []repoFile
	type pending struct {
		ep CommEndpoint
		ps *paramScope
	}
	var all, viz []CommEndpoint
	var dyn []pending
	srcFiles := map[string][]File{}   // C++/Python sources, for following a name through its callers
	launchRepo := map[string]string{} // launch path -> repo, to cite a declaration
	for _, rp := range reposFor(s, repo) {
		files := s.Files(rp, ref)
		ps := &paramScope{repo: rp, reads: map[string][]paramRead{}, byPkg: map[string][]paramRead{},
			yaml: map[string]string{}, flat: map[string]map[string]roslaunch.YAMLScalar{}}
		repoDyn := 0
		for _, f := range files {
			switch {
			case roslaunch.IsLaunchFile(f.Path):
				launch = append(launch, repoFile{rp, f.Path, f.Content})
				if _, ok := launchRepo[f.Path]; !ok {
					launchRepo[f.Path] = rp
				}
				continue
			case IsVizConfig(f.Path):
				viz = append(viz, scanVizConfig(rp, f.Path, f.Content)...)
				res.VizConfigs++
				continue
			case roslaunch.IsParamFile(f.Path) && ProjectFileKind(f.Path) != ProjCI:
				yaml = append(yaml, repoFile{rp, f.Path, f.Content})
				ps.yaml[f.Path] = f.Content
				continue
			}
			if commLang(f.Path) != "" {
				srcFiles[rp] = append(srcFiles[rp], f)
			}
			eps, un := scanComms(rp, f.Path, f.Content, f.Symbols)
			res.Unresolved += un
			for _, ep := range eps {
				if ep.expr != "" {
					dyn = append(dyn, pending{ep, ps})
					repoDyn++
					continue
				}
				all = append(all, ep)
			}
		}
		if repoDyn > 0 {
			// Only a repo with variable-named endpoints pays for the param scan.
			for _, f := range files {
				for _, r := range scanParamReads(f.Path, f.Content) {
					ps.reads[f.Path] = append(ps.reads[f.Path], r)
					ps.byPkg[packageDir(f.Path)] = append(ps.byPkg[packageDir(f.Path)], r)
				}
			}
		}
	}
	// Launch-file remapping decides the RUNTIME topic name, so it must be known
	// before endpoints are grouped: pairing on pre-remap names links the wrong
	// producers and consumers (§8D.4's former limitation).
	var lf []roslaunch.File
	for _, l := range launch {
		// A malformed launch file still contributes whatever parsed: losing
		// its remaps entirely would silently return to pre-remap names.
		parsed, _ := roslaunch.Parse(l.path, l.content)
		lf = append(lf, parsed)
	}
	remaps := roslaunch.NewTable(lf)
	res.LaunchFiles = remaps.Files
	res.UnresolvedIncludes = remaps.Unresolved
	res.UnexpandedRemaps = remaps.Unexpanded
	if len(dyn) > 0 {
		lp := buildLaunchParams(launch, yaml)
		var ar *argResolver
		for _, d := range dyn {
			var bound, unbound []CommEndpoint
			if d.ep.inline != nil || !isNameConcat(d.ep.expr) {
				bound, unbound = resolveDynamic(d.ep, d.ps, lp)
			} else {
				ep := d.ep
				ep.NameResolution = "expr:" + ep.expr + " (unresolved)"
				unbound = []CommEndpoint{ep}
			}
			if len(bound) == 0 && d.ep.inline == nil {
				// A name passed in by the caller: follow the arguments back.
				if ar == nil {
					ar = newArgResolver(srcFiles, lf)
				}
				if b := ar.resolve(d.ep); len(b) > 0 {
					bound, unbound = b, nil
					res.ArgResolved++
				}
			}
			if len(bound) > 0 && unbound == nil && bound[0].Param != "" {
				res.ParamResolved++
			}
			all = append(all, bound...)
			res.Unbound = append(res.Unbound, unbound...)
		}
		res.Unresolved += len(res.Unbound)
	}
	for i := range all {
		eff, resolution, via := remaps.Effective(all[i].Path, all[i].Name)
		if resolution == roslaunch.ResolvedRemapped {
			res.Remapped++
		}
		all[i].Effective, all[i].RemapVia = eff, via
		if all[i].Param != "" || all[i].NameConfidence > 0 { // resolved from a parameter or an argument
			if resolution != roslaunch.ResolvedAsWritten {
				all[i].NameResolution += "; " + resolution
			}
		} else {
			all[i].NameResolution = resolution
		}
	}
	// A launch remap whose target no remapped source endpoint carries is the
	// only record that its node binds that name (a third-party node, or one
	// whose source builds the name by an expression): keep it, labeled.
	explained := map[string]bool{}
	for _, ep := range all {
		if ep.RemapVia != "" {
			explained[ep.RemapVia+"\x00"+ep.Effective] = true
		}
	}
	var declared []CommEndpoint
	seenDecl := map[string]bool{}
	for _, d := range remaps.Declared {
		if d.Runtime == "" || explained[d.Via+"\x00"+d.Runtime] {
			continue
		}
		k := d.Via + "\x00" + d.Runtime + "\x00" + d.Pkg + "\x00" + d.Node
		if seenDecl[k] {
			continue
		}
		seenDecl[k] = true
		resolution := "launch-declared"
		if d.Prefix != "" {
			resolution += "; namespace prefix " + d.Prefix + " not expanded, read as the root namespace"
		}
		node := d.Pkg + "/" + d.Type
		if d.Node != "" {
			node += " (node " + d.Node + ")"
		}
		declared = append(declared, CommEndpoint{
			Repo: launchRepo[d.Via], Path: d.Via, Line: d.Line, Role: RoleLaunchDeclared,
			Name: d.Runtime, Raw: d.From, Effective: d.Runtime, NameResolution: resolution,
			RemapVia: d.Via, In: node, Text: `<remap from="` + d.From + `" to="` + d.To + `"/>`,
		})
	}
	all = append(all, viz...)
	for _, ep := range declared {
		res.Endpoints++
		res.Declared++
		k := key{"topic", ep.Effective}
		g := groups[k]
		if g == nil {
			g = &CommGroup{Family: "topic", Name: ep.Effective}
			groups[k] = g
		}
		g.Declared = append(g.Declared, ep)
	}
	for _, ep := range all {
		res.Endpoints++
		fam, producer := roleFamily(ep.Role)
		if fam == "" {
			continue
		}
		// Group on the RUNTIME name.
		k := key{fam, ep.Effective}
		g := groups[k]
		if g == nil {
			g = &CommGroup{Family: fam, Name: ep.Effective}
			groups[k] = g
		}
		if producer {
			g.Producers = append(g.Producers, ep)
		} else {
			g.Consumers = append(g.Consumers, ep)
		}
	}
	for _, g := range groups {
		g.Confidence = linkConfidence(*g)
		sortEndpoints(g.Producers)
		sortEndpoints(g.Consumers)
		sortEndpoints(g.Declared)
		res.Groups = append(res.Groups, *g)
	}
	sortGroups(res.Groups)
	sortEndpoints(res.Unbound)
	res.Note = commGraphNote(res)
	return res
}

// resolveDynamic turns an endpoint whose name is a variable or a parameter
// read into bound endpoints (one per candidate value) or an unbound one that
// says why its name is unknown.
func resolveDynamic(ep CommEndpoint, ps *paramScope, lp *launchParamIndex) (bound, unbound []CommEndpoint) {
	var reads []paramRead
	if ep.inline != nil {
		reads = []paramRead{*ep.inline}
	} else {
		reads = lookupRead(ps, ep.Path, ep.expr)
	}
	switch len(reads) {
	case 0:
		ep.NameResolution = "var:" + ep.expr + " (unresolved: no parameter read into it was found in its file or package)"
		return nil, []CommEndpoint{ep}
	case 1:
	default:
		names := make([]string, 0, len(reads))
		for _, r := range reads {
			names = append(names, r.param)
		}
		ep.Param = strings.Join(names, "|")
		ep.NameResolution = "param:" + ep.Param + " (unresolved: several parameters are read into " + memberLeaf(ep.expr) + ")"
		return nil, []CommEndpoint{ep}
	}
	r := withDefault(ps, ep.Path, reads[0])
	ep.Param = r.param
	cands := resolveParamValue(r.param, ep.Repo, ep.Path, r, lp, ps)
	var usable []paramCandidate
	for _, c := range cands {
		if v := strings.TrimSpace(c.value); v != "" && !roslaunch.HasSubstitution(v) {
			usable = append(usable, c)
		}
	}
	switch {
	case len(usable) == 0 && len(cands) > 0:
		ep.NameResolution = "param:" + r.param + "=" + cands[0].value + " (" + cands[0].via + "; empty or unexpanded: no runtime name)"
		return nil, []CommEndpoint{ep}
	case len(usable) == 0:
		ep.NameResolution = "param:" + r.param + " (unresolved)"
		return nil, []CommEndpoint{ep}
	case len(usable) > maxAmbiguous:
		ep.NameResolution = "param:" + r.param + " (unresolved: " + strconv.Itoa(len(usable)) + " different values in scope)"
		return nil, []CommEndpoint{ep}
	}
	for _, c := range usable {
		b := ep
		b.Name = normalizeTopic(c.value)
		b.NameConfidence = c.conf
		b.NameResolution = "param:" + r.param + "=" + c.value + " (" + c.via + ")"
		if len(usable) > 1 {
			b.NameConfidence = paramConfAmbiguous
			var others []string
			for _, o := range usable {
				if o.value != c.value {
					others = append(others, o.value+" ("+o.via+")")
				}
			}
			b.NameResolution += "; ambiguous, also: " + strings.Join(others, ", ")
		}
		b.expr, b.inline = "", nil
		bound = append(bound, b)
	}
	return bound, nil
}

// commGraphNote states what the graph is and, precisely, how far its remapping
// knowledge goes. Launch remapping IS now resolved where a launch file covers
// the endpoint's package; what remains unresolved is named rather than implied.
func commGraphNote(res CommGraphResult) string {
	b := "ROS communication graph: producers and consumers linked by the RUNTIME topic name (medium confidence). "
	if res.LaunchFiles > 0 {
		b += fmt.Sprintf("%d launch file(s) parsed; %d endpoint(s) remapped (each labeled launch-remapped with the file that did it). ",
			res.LaunchFiles, res.Remapped)
	} else {
		b += "No launch files are indexed, so every name is taken as written — a remapped node will appear under its source name. "
	}
	if res.UnexpandedRemaps > 0 {
		b += fmt.Sprintf("%d remap(s) were NOT applied because their target kept an unexpanded substitution ($(find …)/$(env …)/$(arg …) with no default) — those endpoints are labeled launch-unexpanded and keep their source name, since applying the literal would be worse than not remapping. ", res.UnexpandedRemaps)
	}
	if res.UnresolvedIncludes > 0 {
		b += fmt.Sprintf("%d <include> directive(s) were NOT expanded ($(find …) substitution is not resolved), so remaps defined only in included files are missing. ", res.UnresolvedIncludes)
	}
	b += "A remap is associated to source by PACKAGE, which is a heuristic — a launch file names an executable, not a file — and a name remapped inconsistently within one package is reported launch-ambiguous rather than guessed. "
	if res.ParamResolved > 0 || len(res.Unbound) > 0 {
		b += fmt.Sprintf("%d endpoint(s) take their name from a parameter and were resolved through it (name_resolution param:<name>=<value> (<source>), name_confidence below a literal's: a launch value for the node's package > one elsewhere in the repo = the call's default > a package YAML no launch file is seen loading); %d endpoint(s) name a variable/parameter whose value could not be resolved and are listed under unresolved_endpoints, never linked. ",
			res.ParamResolved, len(res.Unbound))
	}
	if res.ArgResolved > 0 {
		b += fmt.Sprintf("%d endpoint(s) take their name, or their node handle's namespace, from a caller and were resolved by following the arguments back through the call sites (name_resolution args: <use> ← <call site> …, name_confidence %.2f; a private ~name is placed under every launched node of the package the chain starts in). ", res.ArgResolved, argNameConf)
	}
	if res.Declared > 0 {
		b += fmt.Sprintf("%d launch-declared binding(s) (role launch_declared, listed under declared): a launched node is remapped onto the name but no indexed source endpoint shows it — typically a third-party package (move_base_flex, drivers) or a name built by an expression; the node binds the name at runtime, its direction is unknown, and the topic/service family is assumed topic. ", res.Declared)
	}
	if res.VizConfigs > 0 {
		b += fmt.Sprintf("%d rviz config(s) read: displays are listed as subscribers and tools as publishers (name_resolution as-configured (rviz)). ", res.VizConfigs)
	}
	b += "Producers can also live in packages that are not indexed at all; an empty producer list means none was FOUND, not that none exists. "
	b += "RPC/DDS wire topology is still inferred from source idioms only, names built by expressions no argument trace explains are counted unresolved, and msg_type is best-effort inference labeled by msg_type_source."
	return b
}

// Topic returns the communication group(s) for one name across repo/fleet at ref
// — the focused "who produces and who consumes <name>" query. A name can be both
// a topic and a service, so every matching family is returned. If nothing matches
// exactly, close names (substring) are offered in the Note instead of an empty box.
func Topic(s Store, repo, ref, name string) CommGraphResult {
	full := CommGraph(s, repo, ref)
	want := normalizeTopic(name)
	res := CommGraphResult{Meta: full.Meta, Note: full.Note, ParamResolved: full.ParamResolved, ArgResolved: full.ArgResolved,
		LaunchFiles: full.LaunchFiles, Remapped: full.Remapped, UnresolvedIncludes: full.UnresolvedIncludes,
		UnexpandedRemaps: full.UnexpandedRemaps, VizConfigs: full.VizConfigs}
	// An endpoint whose name could not be resolved is still a candidate when
	// its parameter or variable is named after the topic (status_topic for
	// /status): listed, labeled, never linked.
	wantCanon := canonName(want)
	for _, ep := range full.Unbound {
		for _, n := range append(strings.Split(ep.Param, "|"), ep.expr, ep.Raw) {
			if n != "" && canonName(n) == wantCanon {
				res.Unbound = append(res.Unbound, ep)
				break
			}
		}
	}
	for _, g := range full.Groups {
		// Groups are keyed on the RUNTIME name, but a reader may well ask using
		// the name they saw in source. Match either, so a remapped topic is
		// findable by both spellings instead of looking absent.
		match := g.Name == want
		if !match {
			for _, ep := range append(append(append([]CommEndpoint{}, g.Producers...), g.Consumers...), g.Declared...) {
				if ep.Name == want {
					match = true
					break
				}
			}
		}
		if match {
			res.Groups = append(res.Groups, g)
			res.Endpoints += len(g.Producers) + len(g.Consumers) + len(g.Declared)
			res.Declared += len(g.Declared)
		}
	}
	if len(res.Groups) == 0 {
		res.Note = topicMiss(name, want, full.Groups, ref)
		if len(res.Unbound) > 0 {
			res.Note += fmt.Sprintf(" — %d endpoint(s) whose name comes from an unresolved parameter/variable named like it are listed under unresolved_endpoints", len(res.Unbound))
		}
	}
	return res
}

// topicMiss turns a name that matches no endpoint into a "did you mean" over the
// discovered names, ranked by substring containment then bounded edit distance —
// so a typo ("scn") still finds "scan", not just a missing prefix. Mirrors the
// symbol-level Suggest self-healing (§ agent UX).
func topicMiss(orig, want string, groups []CommGroup, ref string) string {
	type scored struct {
		name  string
		score int
	}
	var cands []scored
	seen := map[string]bool{}
	for _, g := range groups {
		if seen[g.Name] || g.Name == want {
			continue
		}
		seen[g.Name] = true
		switch {
		case substantialSubstring(want, g.Name):
			cands = append(cands, scored{g.Name, 2 + abs(len(g.Name)-len(want))})
		default:
			d := levenshtein(want, g.Name)
			if d > 3 && d*3 > len(want)+len(g.Name) {
				continue
			}
			cands = append(cands, scored{g.Name, 10 + d})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score < cands[j].score
		}
		return cands[i].name < cands[j].name
	})
	if len(cands) == 0 {
		return "no endpoints bind to " + orig + " across the fleet at " + ref
	}
	near := make([]string, 0, 8)
	for _, c := range cands {
		if len(near) >= 8 {
			break
		}
		near = append(near, c.name)
	}
	return "no endpoints bind to " + orig + "; did you mean: " + strings.Join(near, ", ") + "?"
}

// linkConfidence scores a group's linkage: medium for a bare name-string match,
// bumped when a producer and a consumer share a captured message type (C++), and
// left at the producer/consumer floor for a dangling (one-sided) group.
func linkConfidence(g CommGroup) float64 {
	c := nameLinkConfidence(g)
	// A name read from a parameter is only as sure as its value: the edge
	// claims no more than its best-supported producer and consumer names (a
	// literal counts as certain), so one ambiguous mock does not sink the
	// edge, and a param-only edge never reads as sure as a literal one.
	best := func(eps []CommEndpoint) float64 {
		b := 0.0
		for _, ep := range eps {
			v := ep.NameConfidence
			if v == 0 {
				v = 1
			}
			if v > b {
				b = v
			}
		}
		return b
	}
	for _, side := range [][]CommEndpoint{g.Producers, g.Consumers} {
		if b := best(side); len(side) > 0 && b < c {
			c = b
		}
	}
	return c
}

func nameLinkConfidence(g CommGroup) float64 {
	if !g.Connected() {
		return 0.6 // one-sided: the endpoint is real, the edge is not (yet) proven
	}
	for _, p := range g.Producers {
		if p.MsgType == "" {
			continue
		}
		for _, c := range g.Consumers {
			if c.MsgType != "" && sameType(p.MsgType, c.MsgType) {
				return 0.9
			}
		}
	}
	return 0.75
}

// sameType compares two message type strings by their trailing segment, so
// `std_msgs::msg::String`, `std_msgs::String` (ROS1 vs ROS2 C++ spelling), and
// Python's `std_msgs.msg.String` / bare `String` all match — cross-language
// producer↔consumer type confirmation.
func sameType(a, b string) bool {
	seg := func(s string) string {
		s = strings.TrimSpace(s)
		if i := strings.LastIndex(s, "::"); i >= 0 {
			s = s[i+2:]
		}
		if i := strings.LastIndex(s, "."); i >= 0 {
			s = s[i+1:]
		}
		return s
	}
	return seg(a) == seg(b)
}

func sortEndpoints(eps []CommEndpoint) {
	sort.SliceStable(eps, func(i, j int) bool {
		if eps[i].Repo != eps[j].Repo {
			return eps[i].Repo < eps[j].Repo
		}
		if eps[i].Path != eps[j].Path {
			return eps[i].Path < eps[j].Path
		}
		return eps[i].Line < eps[j].Line
	})
}

// sortGroups orders connected edges first (the useful ones), then by family and
// name, so the most actionable rows lead.
func sortGroups(gs []CommGroup) {
	sort.SliceStable(gs, func(i, j int) bool {
		if gs[i].Connected() != gs[j].Connected() {
			return gs[i].Connected()
		}
		if gs[i].Family != gs[j].Family {
			return gs[i].Family < gs[j].Family
		}
		return gs[i].Name < gs[j].Name
	})
}

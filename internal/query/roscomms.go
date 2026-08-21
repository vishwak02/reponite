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
}

// CommGroup is every endpoint bound to one name within a family (topic / service
// / action): the producers on one side, the consumers on the other. When both
// sides are non-empty it is a resolved cross-process edge.
type CommGroup struct {
	Family     string // "topic" | "service" | "action"
	Name       string // normalized name
	Producers  []CommEndpoint
	Consumers  []CommEndpoint
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
	Note             string
	Meta             Meta
}

// launchRemaps parses every indexed ROS launch file in scope into a remap
// table. Query-time, over content the Store already holds — the same
// zero-new-indexing model the rest of this file uses (§8D.5).
func launchRemaps(s Store, repo, ref string) *roslaunch.Table {
	var lf []roslaunch.File
	for _, rp := range reposFor(s, repo) {
		for _, f := range s.Files(rp, ref) {
			if !roslaunch.IsLaunchFile(f.Path) {
				continue
			}
			// A malformed launch file still contributes whatever parsed: losing
			// its remaps entirely would silently return to pre-remap names.
			parsed, _ := roslaunch.Parse(f.Path, f.Content)
			lf = append(lf, parsed)
		}
	}
	return roslaunch.NewTable(lf)
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
func commLang(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".h", ".c":
		return "cpp"
	case ".py":
		return "py"
	}
	return ""
}

// scanComms extracts every resolvable ROS endpoint from one file, plus a count
// of endpoints whose name was not a string literal (dynamic — reported but not
// linkable). Pure: it reads only the passed content and spans.
func scanComms(repo, path, content string, spans []SymbolSpan) (eps []CommEndpoint, unresolved int) {
	lang := commLang(path)
	if lang == "" {
		return nil, 0
	}
	var callbackTypes map[string]string // lazily built: only when a C++ endpoint needs it
	for i, line := range strings.Split(content, "\n") {
		code := stripComment(line, lang)
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
			qloc := firstQuotedRe.FindStringSubmatchIndex(rest)
			if qloc == nil {
				unresolved++ // e.g. advertise<T>(topic_var, 10) — can't resolve the name
				break        // one idiom per line
			}
			raw := rest[qloc[2]:qloc[3]]
			after := rest[qloc[1]:]
			if msgType == "" {
				switch lang {
				case "cpp":
					// ROS1 roscpp subscribe() carries no template — the type is
					// inferred by the middleware from the callback. Recover it from
					// the callback's message parameter in this file (best-effort,
					// labeled; a cross-file callback stays "", never guessed).
					if role == RoleSubscriber {
						if cb := cppCallbackRefRe.FindStringSubmatch(after); cb != nil {
							if callbackTypes == nil {
								callbackTypes = cppCallbackTypes(content)
							}
							if t := callbackTypes[lastSegment(cb[1])]; t != "" {
								msgType, msgSource = t, "callback-param"
							}
						}
					}
				case "py":
					// rospy/rclpy pass the type class positionally: right before the
					// topic literal (rclpy) or right after it (rospy).
					if m := pyTypeBeforeRe.FindStringSubmatch(rest[:qloc[0]]); m != nil {
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
// together as a runtime edge. Pure over Store.Files, no new indexing.
func CommGraph(s Store, repo, ref string) CommGraphResult {
	res := CommGraphResult{Meta: Meta{Repo: repo, Ref: ref}}
	type key struct{ family, name string }
	groups := map[key]*CommGroup{}
	// Launch-file remapping decides the RUNTIME topic name, so it must be known
	// before endpoints are grouped: pairing on pre-remap names links the wrong
	// producers and consumers (§8D.4's former limitation).
	remaps := launchRemaps(s, repo, ref)
	res.LaunchFiles = remaps.Files
	res.UnresolvedIncludes = remaps.Unresolved
	res.UnexpandedRemaps = remaps.Unexpanded
	for _, rp := range reposFor(s, repo) {
		for _, f := range s.Files(rp, ref) {
			eps, un := scanComms(rp, f.Path, f.Content, f.Symbols)
			res.Unresolved += un
			for i := range eps {
				eff, resolution, via := remaps.Effective(eps[i].Path, eps[i].Name)
				eps[i].Effective, eps[i].NameResolution, eps[i].RemapVia = eff, resolution, via
				if resolution == roslaunch.ResolvedRemapped {
					res.Remapped++
				}
			}
			for _, ep := range eps {
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
		}
	}
	for _, g := range groups {
		g.Confidence = linkConfidence(*g)
		sortEndpoints(g.Producers)
		sortEndpoints(g.Consumers)
		res.Groups = append(res.Groups, *g)
	}
	sortGroups(res.Groups)
	res.Note = commGraphNote(res)
	return res
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
	b += "RPC/DDS wire topology is still inferred from source idioms only, dynamic (non-literal) names are counted unresolved, and msg_type is best-effort inference labeled by msg_type_source."
	return b
}

// Topic returns the communication group(s) for one name across repo/fleet at ref
// — the focused "who produces and who consumes <name>" query. A name can be both
// a topic and a service, so every matching family is returned. If nothing matches
// exactly, close names (substring) are offered in the Note instead of an empty box.
func Topic(s Store, repo, ref, name string) CommGraphResult {
	full := CommGraph(s, repo, ref)
	want := normalizeTopic(name)
	res := CommGraphResult{Meta: full.Meta, Note: full.Note,
		LaunchFiles: full.LaunchFiles, Remapped: full.Remapped, UnresolvedIncludes: full.UnresolvedIncludes}
	for _, g := range full.Groups {
		// Groups are keyed on the RUNTIME name, but a reader may well ask using
		// the name they saw in source. Match either, so a remapped topic is
		// findable by both spellings instead of looking absent.
		match := g.Name == want
		if !match {
			for _, ep := range append(append([]CommEndpoint{}, g.Producers...), g.Consumers...) {
				if ep.Name == want {
					match = true
					break
				}
			}
		}
		if match {
			res.Groups = append(res.Groups, g)
			res.Endpoints += len(g.Producers) + len(g.Consumers)
		}
	}
	if len(res.Groups) == 0 {
		res.Note = topicMiss(name, want, full.Groups, ref)
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

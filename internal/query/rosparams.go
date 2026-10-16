// rosparams.go resolves ROS endpoint names that come from PARAMETERS. Real
// nodes rarely hard-code a topic: they read it into a variable or a config
// struct member (`get_ros_param(pnh, "status_topic", status_topic)`,
// `pnh.param("status_topic", status_topic, std::string("/robot_status"))`,
// rclcpp `declare_parameter`, rospy `get_param`) and pass that to
// advertise/subscribe. A literal-only scan drops every such endpoint, so the
// production publisher of a topic is invisible while a test that hard-codes the
// name shows up — the most misleading answer `topics` can give.
//
// This traces the variable to the parameter read into it (same file, then the
// same package directory — where a params struct's loader lives), then the
// parameter to its value: a launch-file `<param>`/`<rosparam>` for a node of
// that package, then any launch file in the same repo, then a YAML file in the
// package, then the default written in the param call. Every resolved name is
// labeled with the parameter and the file that supplied the value, and carries
// a lower confidence than a literal; a name that cannot be resolved is still
// reported (as unresolved, with its parameter) instead of silently vanishing.
// Pure and stdlib-only, over file content the Store already holds (ADR-018).
package query

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/vishwak02/reponite/internal/roslaunch"
)

// Name-confidence of an endpoint whose name came from a parameter, by where
// the value was found. A literal name has none (it is as certain as source).
const (
	paramConfLaunchPkg  = 0.7  // a launch file sets it for a node of this package
	paramConfLaunchRepo = 0.6  // a launch file in the same repo sets it (node package differs)
	paramConfDefault    = 0.6  // only the default written in the param call
	paramConfYAML       = 0.55 // a YAML file in the package has it, but no launch file is seen loading it
	paramConfAmbiguous  = 0.4  // several sources disagree; listed under each candidate
)

// maxAmbiguous bounds how many candidate values an ambiguous endpoint is
// grouped under; beyond this it is reported unresolved rather than smeared.
const maxAmbiguous = 3

var (
	// paramCallRe finds a parameter read. `param`/`get_param`/`get` are only
	// matched as a member/qualified call so a variable named param is not one.
	paramCallRe = regexp.MustCompile(`(\bget_ros_param|(?:\.|->|::)\s*param|\bgetParam|\bgetParamCached|\bparam::get|(?:\.|->)\s*get_param|\bdeclare_parameter|\bget_parameter_or|\bget_parameter)\s*(<[^;(){}]*>)?\s*\(`)
	// identPathRe is a variable or member path: x, _np.topic, cfg_->t, this->t, self.t.
	identPathRe = regexp.MustCompile(`^(?:[A-Za-z_]\w*\s*(?:\.|->|::)\s*)*[A-Za-z_]\w*$`)
	// receiverRe strips the receiver of a call (nh., this->nh_->, rospy., ns::).
	receiverRe = regexp.MustCompile(`(?:[A-Za-z_]\w*\s*(?:\.|->|::)\s*)+$`)
	// lhsRe captures the variable a returning param read is assigned to:
	// `x = …`, `std::string x = …`, `self.x = …`, `x{…`, a member init `x(…`.
	lhsRe       = regexp.MustCompile(`([A-Za-z_]\w*(?:\s*(?:\.|->)\s*[A-Za-z_]\w*)*)\s*(=|\{|\()\s*$`)
	quotedArgRe = regexp.MustCompile(`^(?:[\w:]+\s*[({]\s*)?(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')\s*[)}]?$`)
	nodeArgRe   = regexp.MustCompile(`^(?:this|shared_from_this\(\)|\w*node\w*(?:\(\))?|\w*nh\w*|self)$`)
)

// cppKeywords are never assignment targets (`if (nh.getParam(...))`).
var cppKeywords = map[string]bool{"if": true, "while": true, "for": true, "switch": true, "return": true,
	"assert": true, "not": true, "and": true, "or": true, "elif": true, "sizeof": true}

// paramRead is one parameter read into a variable.
type paramRead struct {
	target     string // last segment of the variable written
	param      string // normalized parameter name
	def        string // the default written in the call, if a string literal
	hasDef     bool
	path       string
	line       int
	expression string
}

// splitCallArgs splits a call's argument list starting just after its "(" at
// text[open], honoring nesting and string literals. ok is false when the
// closing paren is not found within the scanned window.
func splitCallArgs(text string, open int) (args []string, end int, ok bool) {
	depth := 0
	start := open + 1
	limit := len(text)
	if limit > open+4000 {
		limit = open + 4000
	}
	for i := open + 1; i < limit; i++ {
		c := text[i]
		switch c {
		case '"', '\'':
			// skip a string literal (C++ char literals are short and harmless)
			for j := i + 1; j < limit; j++ {
				if text[j] == '\\' {
					j++
					continue
				}
				if text[j] == c {
					i = j
					break
				}
				if text[j] == '\n' {
					i = j
					break
				}
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				if a := strings.TrimSpace(text[start:i]); a != "" || len(args) > 0 {
					args = append(args, a)
				}
				return args, i, true
			}
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(text[start:i]))
				start = i + 1
			}
		case ';':
			if depth == 0 {
				return nil, i, false
			}
		}
	}
	return nil, limit, false
}

// stringLiteral returns the string an argument spells when it is a single
// literal: "x", 'x', std::string("x"), std::string{"x"}.
func stringLiteral(arg string) (string, bool) {
	m := quotedArgRe.FindStringSubmatch(strings.TrimSpace(arg))
	if m == nil {
		return "", false
	}
	if m[1] != "" || strings.Contains(arg, `""`) {
		return m[1], true
	}
	return m[2], true
}

func memberLeaf(expr string) string {
	expr = strings.TrimSpace(expr)
	for _, sep := range []string{"->", ".", "::"} {
		if i := strings.LastIndex(expr, sep); i >= 0 {
			expr = expr[i+len(sep):]
		}
	}
	return strings.TrimSpace(expr)
}

// paramCallArgs reads the parameter name and default out of one param call's
// arguments, and the target variable for the output-parameter forms.
func paramCallArgs(fn string, args []string) (param, target, def string, hasDef bool) {
	fn = strings.TrimSpace(strings.TrimLeft(fn, ".->:"))
	pi := -1
	for i, a := range args {
		if s, ok := stringLiteral(a); ok {
			param, pi = s, i
			break
		}
		if i >= 1 {
			break // the name is the first or (get_ros_param) second argument
		}
	}
	if pi < 0 {
		return "", "", "", false
	}
	rest := args[pi+1:]
	targetAt, defAt := -1, -1
	switch fn {
	case "get_ros_param":
		if len(rest) >= 1 {
			targetAt = 0
		}
		if len(rest) >= 2 {
			defAt = 1
		}
	case "param":
		if len(rest) >= 2 {
			targetAt, defAt = 0, 1
		} else if len(rest) == 1 {
			defAt = 0
		}
	case "getParam", "getParamCached", "param::get":
		targetAt = 0
	case "get_param", "declare_parameter":
		defAt = 0
	case "get_parameter":
		if len(rest) >= 1 {
			targetAt = 0
		}
	case "get_parameter_or":
		targetAt, defAt = 0, 1
	}
	if targetAt >= 0 && targetAt < len(rest) && identPathRe.MatchString(rest[targetAt]) {
		target = memberLeaf(rest[targetAt])
	}
	if defAt >= 0 && defAt < len(rest) {
		def, hasDef = stringLiteral(rest[defAt])
	}
	return roslaunch.NormParam(param), target, def, hasDef
}

// scanParamReads finds every parameter read in a C++/Python file and the
// variable it lands in.
func scanParamReads(p, content string) []paramRead {
	if commLang(p) == "" || !paramCallRe.MatchString(content) {
		return nil
	}
	var out []paramRead
	for _, m := range paramCallRe.FindAllStringSubmatchIndex(content, -1) {
		open := m[1] - 1
		args, _, ok := splitCallArgs(content, open)
		if !ok {
			continue
		}
		fn := content[m[2]:m[3]]
		param, target, def, hasDef := paramCallArgs(fn, args)
		if param == "" {
			continue
		}
		line := 1 + strings.Count(content[:m[0]], "\n")
		if target == "" {
			// A returning read: the variable is whatever it is assigned to.
			ls := strings.LastIndex(content[:m[0]], "\n") + 1
			prefix := receiverRe.ReplaceAllString(content[ls:m[0]], "")
			if lm := lhsRe.FindStringSubmatch(prefix); lm != nil && !cppKeywords[memberLeaf(lm[1])] {
				target = memberLeaf(lm[1])
			}
		}
		out = append(out, paramRead{target: target, param: param, def: def, hasDef: hasDef, path: p, line: line,
			expression: strings.TrimSpace(content[m[0]:min(len(content), m[1]+80)])})
	}
	return out
}

// packageDir is the ROS package directory a source path belongs to: the path
// before its first src/include/scripts/nodes/... segment, else its directory.
func packageDir(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs[:max(0, len(segs)-1)] {
		switch s {
		case "src", "include", "scripts", "nodes", "lib", "launch", "config", "test", "tests":
			return strings.Join(segs[:i], "/")
		}
	}
	return path.Dir(p)
}

// paramScope holds what the name resolver needs about one repo.
type paramScope struct {
	repo   string
	reads  map[string][]paramRead // by file path
	byPkg  map[string][]paramRead // by package directory
	yaml   map[string]string      // param files by path
	flat   map[string]map[string]roslaunch.YAMLScalar
	loaded bool
}

func (ps *paramScope) flatten(p string) map[string]roslaunch.YAMLScalar {
	if f, ok := ps.flat[p]; ok {
		return f
	}
	f := roslaunch.FlattenYAML(ps.yaml[p])
	ps.flat[p] = f
	return f
}

// launchParamIndex is every launch-provided parameter value in scope.
type launchParamIndex struct {
	byName     map[string][]launchParam
	unresolved int // rosparam loads whose file could not be located
}

type launchParam struct {
	roslaunch.ParamValue
	repo string
	test bool // set by a rostest/test launch file: never the production value
}

// buildLaunchParams expands the parameters of every launch file in scope,
// resolving `<rosparam file="$(find pkg)/…">` against the stored YAML files.
func buildLaunchParams(launch []repoFile, yaml []repoFile) *launchParamIndex {
	idx := &launchParamIndex{byName: map[string][]launchParam{}}
	if len(launch) == 0 {
		return idx
	}
	for _, lf := range launch {
		parsed, _ := roslaunch.Parse(lf.path, lf.content)
		if len(parsed.Params) == 0 && len(parsed.Rosparams) == 0 {
			continue
		}
		lookup := func(pkg, rel string) (string, string, bool) {
			want := pkg + "/" + rel
			var best *repoFile
			for i := range yaml {
				y := &yaml[i]
				if y.path != want && !strings.HasSuffix(y.path, "/"+want) {
					continue
				}
				// Prefer the launch file's own repo, then the shortest path.
				if best == nil || (y.repo == lf.repo) != (best.repo == lf.repo) && y.repo == lf.repo ||
					(y.repo == lf.repo) == (best.repo == lf.repo) && len(y.path) < len(best.path) {
					best = y
				}
			}
			if best == nil {
				return "", "", false
			}
			return best.path, best.content, true
		}
		vals, unresolved := roslaunch.LaunchParams(parsed, lookup)
		idx.unresolved += len(unresolved)
		for _, v := range vals {
			idx.byName[v.Name] = append(idx.byName[v.Name], launchParam{ParamValue: v, repo: lf.repo,
				test: IsTestPath(lf.path) || strings.HasSuffix(lf.path, ".test")})
		}
	}
	return idx
}

type repoFile struct {
	repo, path, content string
}

// paramCandidate is one value a parameter may take, with where it came from.
type paramCandidate struct {
	value string
	via   string
	conf  float64
}

// resolveParamValue finds the runtime value of param for an endpoint in
// repo at srcPath, by the tiers in the file comment. It returns every distinct
// candidate of the strongest tier that has any.
func resolveParamValue(param, repo, srcPath string, read paramRead, lp *launchParamIndex, ps *paramScope) []paramCandidate {
	segs := map[string]bool{}
	for _, s := range strings.Split(srcPath, "/") {
		segs[s] = true
	}
	var pkgTier, repoTier []paramCandidate
	srcTest := IsTestPath(srcPath)
	for _, v := range lp.byName[param] {
		if v.test && !srcTest {
			continue // a test launch configures the test, not production
		}
		c := paramCandidate{value: v.Value, via: v.Via}
		switch {
		case v.Pkg != "" && segs[v.Pkg]:
			c.conf = paramConfLaunchPkg
			pkgTier = append(pkgTier, c)
		case v.repo == repo:
			c.conf = paramConfLaunchRepo
			repoTier = append(repoTier, c)
		}
	}
	if c := distinct(pkgTier); len(c) > 0 {
		return c
	}
	if c := distinct(repoTier); len(c) > 0 {
		return c
	}
	// A YAML file in the endpoint's own package that sets the key — a config
	// no launch file in scope was seen loading, so it ranks below a default
	// only when they disagree (a default is what runs when nothing loads it).
	var yamlTier []paramCandidate
	dir := packageDir(srcPath)
	for p := range ps.yaml {
		if dir != "" && dir != "." && !strings.HasPrefix(p, dir+"/") {
			continue
		}
		if v, ok := ps.flatten(p)[param]; ok {
			yamlTier = append(yamlTier, paramCandidate{value: v.Value, via: p + ":" + strconv.Itoa(v.Line), conf: paramConfYAML})
		}
	}
	if read.hasDef && read.def != "" {
		def := paramCandidate{value: read.def, via: "default at " + read.path + ":" + strconv.Itoa(read.line), conf: paramConfDefault}
		for _, y := range distinct(yamlTier) {
			if roslaunch.NormParam(y.value) == roslaunch.NormParam(def.value) {
				def.via += "; also " + y.via
			}
		}
		return []paramCandidate{def}
	}
	return distinct(yamlTier)
}

// distinct collapses candidates with the same value (keeping the first
// citation, sorted for determinism).
func distinct(cs []paramCandidate) []paramCandidate {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].value != cs[j].value {
			return cs[i].value < cs[j].value
		}
		return cs[i].via < cs[j].via
	})
	var out []paramCandidate
	for _, c := range cs {
		if len(out) > 0 && roslaunch.NormParam(out[len(out)-1].value) == roslaunch.NormParam(c.value) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// topicArgIndex is which argument of a pub/sub call carries the name.
func topicArgIndex(lang, role, fn string, args []string) int {
	if lang == "py" {
		for i, a := range args {
			if k, _, ok := strings.Cut(a, "="); ok {
				switch strings.TrimSpace(k) {
				case "topic", "name", "srv_name", "action_name":
					return i
				}
			}
		}
		switch {
		case strings.HasPrefix(fn, "create_"):
			return 1
		case fn == "ActionServer" || fn == "ActionClient":
			return 2
		}
		return 0
	}
	if len(args) > 1 && nodeArgRe.MatchString(strings.TrimSpace(args[0])) {
		return 1 // free-function / action forms take the node first
	}
	return 0
}

// dynamicName classifies a non-literal name argument: an inline param read
// (param, default) or a variable path (expr). ok=false for anything else
// (string concatenation, a function call): genuinely dynamic.
func dynamicName(arg string) (expr, param, def string, hasDef, ok bool) {
	arg = strings.TrimSpace(arg)
	if k, v, found := strings.Cut(arg, "="); found && identPathRe.MatchString(strings.TrimSpace(k)) && !strings.HasPrefix(v, "=") {
		arg = strings.TrimSpace(v) // a Python keyword argument
	}
	if m := paramCallRe.FindStringSubmatchIndex(arg); m != nil {
		if args, _, ok := splitCallArgs(arg, m[1]-1); ok {
			p, _, d, hd := paramCallArgs(arg[m[2]:m[3]], args)
			if p != "" {
				return arg, p, d, hd, true
			}
		}
	}
	if identPathRe.MatchString(arg) {
		return arg, "", "", false, true
	}
	return "", "", "", false, false
}

// lookupRead finds the parameter read into the variable expr names: in the
// same file first, then the same package directory. Several different
// parameters for one variable name are reported, not guessed between.
func lookupRead(ps *paramScope, srcPath, expr string) []paramRead {
	name := memberLeaf(expr)
	pick := func(reads []paramRead) []paramRead {
		var out []paramRead
		seen := map[string]bool{}
		for _, r := range reads {
			if r.target == name && !seen[r.param] {
				seen[r.param] = true
				out = append(out, r)
			}
		}
		return out
	}
	if r := pick(ps.reads[srcPath]); len(r) > 0 {
		return r
	}
	return pick(ps.byPkg[packageDir(srcPath)])
}

// withDefault fills a read's default from another read of the same parameter
// in scope (rclcpp/rclpy declare the parameter with a default in one place and
// read it elsewhere).
func withDefault(ps *paramScope, srcPath string, r paramRead) paramRead {
	if r.hasDef {
		return r
	}
	for _, reads := range [][]paramRead{ps.reads[srcPath], ps.byPkg[packageDir(srcPath)]} {
		for _, o := range reads {
			if o.param == r.param && o.hasDef {
				r.def, r.hasDef = o.def, true
				r.path, r.line = o.path, o.line
				return r
			}
		}
	}
	return r
}

// canonName folds a name for loose comparison between a topic, a parameter
// and a variable: robot_status, robotStatus and robot_status_topic all fold to
// "robotstatus".
func canonName(s string) string {
	s = strings.ToLower(memberLeaf(roslaunch.NormParam(s)))
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.ReplaceAll(strings.Trim(s, "_"), "_", "")
	for _, suf := range []string{"topicname", "topic", "name"} {
		if strings.HasSuffix(s, suf) && len(s) > len(suf) {
			s = strings.TrimSuffix(s, suf)
			break
		}
	}
	return s
}

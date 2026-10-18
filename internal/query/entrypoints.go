// entrypoints.go finds where execution ENTERS a repo — the first thing to read
// in an unfamiliar codebase: main functions, CLI commands and subcommands
// (argparse, click, typer, cobra, urfave/cli, clap, commander/yargs), HTTP
// servers with their route count per framework (routes.go's extractors), web
// UI roots and page routes (React Router, Vue Router, Angular, Next.js),
// ROS nodes, launch files and plugins, build targets, Dockerfile
// CMD/ENTRYPOINT, docker-compose services, CI jobs, package.json scripts and
// Makefile targets. Lexical over Store.Files and pure (ADR-018); a framework
// it does not recognize is simply absent, never guessed.
package query

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Entry-point kinds, in the order map ranks them.
const (
	EntryMain     = "main"
	EntryCLI      = "cli"
	EntryHTTP     = "http"
	EntryUIRoot   = "ui-root"
	EntryUIRoutes = "ui-routes"
	EntryROSNode  = "ros-node"
	EntryLaunch   = "ros-launch"
	EntryBinary   = "build-target"
	EntryPlugin   = "plugin"
	EntryDocker   = "docker"
	EntryCompose  = "compose"
	EntryScripts  = "scripts"
	EntryMake     = "make"
	EntryCI       = "ci"
)

var entryRank = map[string]int{EntryMain: 0, EntryCLI: 1, EntryHTTP: 2, EntryUIRoot: 3, EntryUIRoutes: 4, EntryROSNode: 5,
	EntryLaunch: 6, EntryBinary: 7, EntryPlugin: 8, EntryDocker: 9, EntryCompose: 10, EntryScripts: 11, EntryMake: 12, EntryCI: 13}

// maxEntryItems bounds the named items (subcommands, routes, jobs, ...) one
// entry point lists; Count keeps the total.
const maxEntryItems = 8

// EntryPoint is one place execution enters the repo.
type EntryPoint struct {
	Kind      string
	Name      string // binary/command/framework/workflow name
	Framework string // the library recognized (argparse, cobra, react-router, ...)
	Repo      string
	Path      string
	Line      int
	Detail    string   // one line: CMD/ENTRYPOINT, triggers, ...
	Items     []string // subcommands / routes / services / jobs / targets (bounded)
	Count     int      // total items
}

var (
	reGoPkgMain     = regexp.MustCompile(`(?m)^package\s+main\b`)
	reGoMainFn      = regexp.MustCompile(`(?m)^func\s+main\s*\(\s*\)`)
	rePyMainGuard   = regexp.MustCompile(`(?m)^if\s+__name__\s*==\s*['"]__main__['"]\s*:`)
	reCMain         = regexp.MustCompile(`(?m)^\s*(?:int|auto|void)\s+main\s*\(`)
	reRustMain      = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?(?:async\s+)?fn\s+main\s*\(`)
	reJavaMain      = regexp.MustCompile(`public\s+static\s+void\s+main\s*\(`)
	reArgparse      = regexp.MustCompile(`\bArgumentParser\s*\(`)
	reArgSub        = regexp.MustCompile(`\.add_parser\(\s*['"]([\w:.-]+)['"]`)
	reClickCmd      = regexp.MustCompile(`^\s*@(?:click|\w+)\.(command|group)\s*\(\s*(?:name\s*=\s*)?(?:['"]([\w:.-]+)['"])?`)
	reTyperApp      = regexp.MustCompile(`\btyper\.Typer\s*\(`)
	reFire          = regexp.MustCompile(`\bfire\.Fire\s*\(`)
	rePyDefName     = regexp.MustCompile(`^\s*(?:async\s+)?def\s+(\w+)`)
	reCobraUse      = regexp.MustCompile(`\bUse:\s*"([\w:.-]+)`)
	reCobraCmd      = regexp.MustCompile(`cobra\.Command\s*\{`)
	reUrfaveName    = regexp.MustCompile(`\bName:\s*"([\w:.-]+)"`)
	reUrfave        = regexp.MustCompile(`\bcli\.(?:App|Command)\s*\{`)
	reClapDerive    = regexp.MustCompile(`#\[derive\([^)]*\b(Parser|Subcommand)\b`)
	reClapCommand   = regexp.MustCompile(`\bCommand::new\(\s*"([\w:.-]+)"`)
	reRustVariant   = regexp.MustCompile(`^\s{2,}([A-Z]\w*)\s*(?:[,{(]|$)`)
	reJSCommander   = regexp.MustCompile(`\.command\(\s*['"\x60]([\w:.-]+)`)
	reJSCLIUse      = regexp.MustCompile(`require\(\s*['"](?:commander|yargs)['"]\)|from\s+['"](?:commander|yargs)(?:/[\w]+)?['"]`)
	reReactRoot     = regexp.MustCompile(`\b(ReactDOM\.render|ReactDOM\.hydrate|createRoot|hydrateRoot)\s*\(`)
	reVueRoot       = regexp.MustCompile(`\bcreateApp\s*\(|\bnew\s+Vue\s*\(`)
	reNgRoot        = regexp.MustCompile(`\bbootstrapModule\s*\(|\bbootstrapApplication\s*\(`)
	reRouteTag      = regexp.MustCompile(`<Route\b[^>]*?\bpath=\{?\s*["'\x60]([^"'\x60]*)["'\x60]`)
	reRouteElem     = regexp.MustCompile(`(?:element=\{\s*<\s*([\w.]+)|component=\{\s*([\w.]+))`)
	reRouteObjPath  = regexp.MustCompile(`\bpath:\s*["'\x60]([^"'\x60]*)["'\x60]`)
	reRouteObjComp  = regexp.MustCompile(`\b(?:component|element|Component|loadComponent|loadChildren|redirectTo)\s*:\s*(?:<\s*)?(?:\(\)\s*=>\s*)?(?:import\(\s*['"]([^'"]+)['"]|['"]([^'"]+)['"]|([\w.]+))`)
	reRouterLib     = regexp.MustCompile(`['"](?:react-router|react-router-dom|vue-router|@angular/router|@tanstack/react-router|@remix-run/react)['"]|\bcreateBrowserRouter\s*\(|\bRouteObject\b|\bRoutes\s*=\s*\[`)
	reRosInit       = regexp.MustCompile(`\bros::init\s*\([^,]+,[^,]+,\s*"([\w/~]+)"`)
	reRospyInit     = regexp.MustCompile(`\brospy\.init_node\(\s*['"]([\w/~]+)['"]`)
	reRclcppNode    = regexp.MustCompile(`(?:rclcpp::Node|rclcpp_lifecycle::LifecycleNode|\bNode)\s*\(\s*"([\w/~]+)"`)
	reRclpyNode     = regexp.MustCompile(`(?:super\(\)\.__init__|Node\.__init__\s*\(\s*self\s*,)\s*\(?\s*(?:node_name\s*=\s*)?['"]([\w/~]+)['"]`)
	reComponentReg  = regexp.MustCompile(`RCLCPP_COMPONENTS_REGISTER_NODE\(\s*([\w:]+)`)
	rePluginExport  = regexp.MustCompile(`PLUGINLIB_EXPORT_CLASS\(\s*([\w:]+)\s*,\s*([\w:]+)`)
	reLaunchNode    = regexp.MustCompile(`<node\b([^>]*)>?`)
	reXMLAttr       = regexp.MustCompile(`\b(name|pkg|type|exec)\s*=\s*"([^"]*)"`)
	reLaunchPyNode  = regexp.MustCompile(`\bNode\s*\(`)
	reLaunchPyExec  = regexp.MustCompile(`\bexecutable\s*=\s*['"]([\w./-]+)['"]`)
	reAddExecutable = regexp.MustCompile(`(?i)\badd_executable\s*\(\s*([\w${}.-]+)`)
	reCatkinPython  = regexp.MustCompile(`(?is)catkin_install_python\s*\(\s*PROGRAMS\s+([^)]*?)\s+DESTINATION`)
	reDockerInstr   = regexp.MustCompile(`(?i)^\s*(FROM|CMD|ENTRYPOINT)\s+(.*)$`)
	reYAMLTopKey    = regexp.MustCompile(`^([\w.-]+):\s*(.*)$`)
	reYAMLChildKey  = regexp.MustCompile(`^(\s+)([\w.-]+):\s*(.*)$`)
	reMakeTarget    = regexp.MustCompile(`^([A-Za-z0-9_][\w./-]*(?:\s+[A-Za-z0-9_][\w./-]*)*)\s*::?(?:\s|$)`)
	reJenkinsStage  = regexp.MustCompile(`\bstage\s*\(\s*['"]([^'"]+)['"]`)
	rePyServer      = regexp.MustCompile(`\buvicorn\.run\s*\(|\bweb\.run_app\s*\(|\bapp\.run\s*\(|\bexecute_from_command_line\s*\(`)
	reJSServer      = regexp.MustCompile(`\.listen\s*\(`)
	reGoServer      = regexp.MustCompile(`\bListenAndServe(?:TLS)?\s*\(|\.Run\s*\(\s*"[:\w.]*"\s*\)|\.Start\s*\(\s*"[:\w.]*"\s*\)`)
	reSpringMapping = regexp.MustCompile(`@(Get|Post|Put|Patch|Delete|Request)Mapping\b(?:\(\s*(?:value\s*=\s*|path\s*=\s*)?"([^"]*)")?`)
	reSpringApp     = regexp.MustCompile(`@SpringBootApplication\b`)
	gitlabReserved  = map[string]bool{"stages": true, "variables": true, "image": true, "include": true, "default": true, "workflow": true,
		"before_script": true, "after_script": true, "services": true, "cache": true, "types": true, "pages": false}
)

// lineOf is the 1-based line of byte offset off in content.
func lineOf(content string, off int) int { return strings.Count(content[:off], "\n") + 1 }

// boundItems keeps at most maxEntryItems distinct items, in order.
func boundItems(items []string) ([]string, int) {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		if it == "" || seen[it] {
			continue
		}
		seen[it] = true
		out = append(out, it)
	}
	n := len(out)
	if n > maxEntryItems {
		out = out[:maxEntryItems]
	}
	return out, n
}

// EntryPoints finds every entry point in one repo's files. Test files are
// skipped: a test's main is not where the product starts.
func EntryPoints(repo string, files []File) []EntryPoint {
	var out []EntryPoint
	add := func(e EntryPoint) {
		e.Repo = repo
		if e.Count == 0 {
			e.Items, e.Count = boundItems(e.Items)
		}
		out = append(out, e)
	}
	nextJS := false
	for _, f := range files {
		if filepath.Base(f.Path) == "package.json" && regexp.MustCompile(`"next"\s*:`).MatchString(f.Content) {
			nextJS = true
		}
	}
	for _, f := range files {
		if IsTestPath(f.Path) || isAuxPath(f.Path) {
			continue
		}
		switch kind := ProjectFileKind(f.Path); kind {
		case ProjDocker:
			if e, ok := dockerEntry(f); ok {
				add(e)
			}
			continue
		case ProjCompose:
			if items := yamlChildren(f.Content, "services"); len(items) > 0 {
				add(EntryPoint{Kind: EntryCompose, Name: f.Path, Path: f.Path, Line: 1, Items: items})
			}
			continue
		case ProjCI:
			if e, ok := ciEntry(f); ok {
				add(e)
			}
			continue
		case ProjMake:
			if items := makeTargets(f.Content); len(items) > 0 {
				add(EntryPoint{Kind: EntryMake, Name: f.Path, Path: f.Path, Line: 1, Items: items})
			}
			continue
		case ProjManifest:
			for _, e := range manifestEntries(f) {
				add(e)
			}
			continue
		case ProjReadme:
			continue
		}
		lang := langOfPath(f.Path)
		switch {
		case lang == "ros-launch":
			if e, ok := launchEntry(f); ok {
				add(e)
			}
			continue
		case strings.HasSuffix(f.Path, ".launch.py"):
			if n := len(reLaunchPyNode.FindAllStringIndex(f.Content, -1)); n > 0 {
				var items []string
				for _, m := range reLaunchPyExec.FindAllStringSubmatch(f.Content, -1) {
					items = append(items, m[1])
				}
				it, _ := boundItems(items)
				add(EntryPoint{Kind: EntryLaunch, Name: filepath.Base(f.Path), Framework: "ros2-launch", Path: f.Path, Line: 1, Items: it, Count: n})
			}
			continue
		}
		for _, e := range codeEntries(f, lang) {
			add(e)
		}
		if nextJS {
			if r, ok := nextRoute(f.Path); ok {
				add(EntryPoint{Kind: EntryUIRoutes, Name: r, Framework: "next", Path: f.Path, Line: 1, Items: []string{r}})
			}
		}
	}
	return out
}

// codeEntries reads one source file's entry points.
func codeEntries(f File, lang string) []EntryPoint {
	c := f.Content
	var out []EntryPoint
	at := func(loc []int) int {
		if loc == nil {
			return 1
		}
		return lineOf(c, loc[0])
	}
	rosNode := ""
	rosFramework := ""
	rosLine := 0
	switch lang {
	case "go":
		if reGoPkgMain.MatchString(c) {
			if loc := reGoMainFn.FindStringIndex(c); loc != nil {
				name := pkgFromPath(f.Path)
				if name == "" {
					name = "."
				}
				out = append(out, EntryPoint{Kind: EntryMain, Name: path.Base(name), Framework: "go", Path: f.Path, Line: at(loc)})
			}
		}
		if loc := reCobraCmd.FindStringIndex(c); loc != nil {
			var items []string
			for _, m := range reCobraUse.FindAllStringSubmatch(c, -1) {
				items = append(items, m[1])
			}
			out = append(out, EntryPoint{Kind: EntryCLI, Name: firstOr(items, f.Path), Framework: "cobra", Path: f.Path, Line: at(loc), Items: items})
		} else if loc := reUrfave.FindStringIndex(c); loc != nil && strings.Contains(c, "urfave/cli") {
			var items []string
			for _, m := range reUrfaveName.FindAllStringSubmatch(c, -1) {
				items = append(items, m[1])
			}
			out = append(out, EntryPoint{Kind: EntryCLI, Name: firstOr(items, f.Path), Framework: "urfave/cli", Path: f.Path, Line: at(loc), Items: items})
		}
		if loc := reGoServer.FindStringIndex(c); loc != nil {
			out = append(out, EntryPoint{Kind: EntryHTTP, Name: "server start", Framework: "go", Path: f.Path, Line: at(loc), Detail: strings.TrimSpace(lineAt(c, at(loc)))})
		}
	case "python":
		guard := rePyMainGuard.FindStringIndex(c)
		if loc := reArgparse.FindStringIndex(c); loc != nil {
			var items []string
			for _, m := range reArgSub.FindAllStringSubmatch(c, -1) {
				items = append(items, m[1])
			}
			out = append(out, EntryPoint{Kind: EntryCLI, Name: f.Path, Framework: "argparse", Path: f.Path, Line: at(loc), Items: items})
		}
		if cmds, line := pyDecoratedCommands(c); len(cmds) > 0 {
			fw := "click"
			if reTyperApp.MatchString(c) {
				fw = "typer"
			}
			out = append(out, EntryPoint{Kind: EntryCLI, Name: f.Path, Framework: fw, Path: f.Path, Line: line, Items: cmds})
		}
		if loc := reFire.FindStringIndex(c); loc != nil {
			out = append(out, EntryPoint{Kind: EntryCLI, Name: f.Path, Framework: "fire", Path: f.Path, Line: at(loc)})
		}
		if m := reRospyInit.FindStringSubmatchIndex(c); m != nil {
			rosNode, rosFramework, rosLine = c[m[2]:m[3]], "rospy", lineOf(c, m[0])
		} else if m := reRclpyNode.FindStringSubmatchIndex(c); m != nil && strings.Contains(c, "rclpy") {
			rosNode, rosFramework, rosLine = c[m[2]:m[3]], "rclpy", lineOf(c, m[0])
		}
		if loc := rePyServer.FindStringIndex(c); loc != nil {
			out = append(out, EntryPoint{Kind: EntryHTTP, Name: "server start", Framework: "python", Path: f.Path, Line: at(loc), Detail: strings.TrimSpace(lineAt(c, at(loc)))})
		}
		if guard != nil && rosNode == "" {
			out = append(out, EntryPoint{Kind: EntryMain, Name: f.Path, Framework: "python", Path: f.Path, Line: at(guard)})
		}
	case "c", "cpp":
		if m := reRosInit.FindStringSubmatchIndex(c); m != nil {
			rosNode, rosFramework, rosLine = c[m[2]:m[3]], "roscpp", lineOf(c, m[0])
		} else if m := reRclcppNode.FindStringSubmatchIndex(c); m != nil && strings.Contains(c, "rclcpp") {
			rosNode, rosFramework, rosLine = c[m[2]:m[3]], "rclcpp", lineOf(c, m[0])
		}
		if loc := reCMain.FindStringIndex(c); loc != nil && rosNode == "" {
			out = append(out, EntryPoint{Kind: EntryMain, Name: f.Path, Framework: "c/c++", Path: f.Path, Line: at(loc)})
		}
		for _, m := range reComponentReg.FindAllStringSubmatchIndex(c, -1) {
			out = append(out, EntryPoint{Kind: EntryROSNode, Name: c[m[2]:m[3]], Framework: "rclcpp-component", Path: f.Path, Line: lineOf(c, m[0])})
		}
		for _, m := range rePluginExport.FindAllStringSubmatchIndex(c, -1) {
			out = append(out, EntryPoint{Kind: EntryPlugin, Name: c[m[2]:m[3]], Framework: "pluginlib", Path: f.Path, Line: lineOf(c, m[0]), Detail: "implements " + c[m[4]:m[5]]})
		}
	case "rust":
		if loc := reRustMain.FindStringIndex(c); loc != nil {
			out = append(out, EntryPoint{Kind: EntryMain, Name: f.Path, Framework: "rust", Path: f.Path, Line: at(loc)})
		}
		if loc := reClapDerive.FindStringIndex(c); loc != nil {
			var items []string
			for _, m := range reClapCommand.FindAllStringSubmatch(c, -1) {
				items = append(items, m[1])
			}
			items = append(items, rustSubcommands(c)...)
			out = append(out, EntryPoint{Kind: EntryCLI, Name: f.Path, Framework: "clap", Path: f.Path, Line: at(loc), Items: items})
		} else if loc := reClapCommand.FindStringIndex(c); loc != nil {
			var items []string
			for _, m := range reClapCommand.FindAllStringSubmatch(c, -1) {
				items = append(items, m[1])
			}
			out = append(out, EntryPoint{Kind: EntryCLI, Name: firstOr(items, f.Path), Framework: "clap", Path: f.Path, Line: at(loc), Items: items})
		}
	case "java":
		if loc := reJavaMain.FindStringIndex(c); loc != nil {
			out = append(out, EntryPoint{Kind: EntryMain, Name: f.Path, Framework: "java", Path: f.Path, Line: at(loc)})
		}
		if loc := reSpringApp.FindStringIndex(c); loc != nil {
			out = append(out, EntryPoint{Kind: EntryHTTP, Name: "server start", Framework: "spring", Path: f.Path, Line: at(loc)})
		}
	case "javascript", "typescript":
		if loc := reReactRoot.FindStringIndex(c); loc != nil {
			out = append(out, EntryPoint{Kind: EntryUIRoot, Name: f.Path, Framework: "react", Path: f.Path, Line: at(loc)})
		} else if loc := reVueRoot.FindStringIndex(c); loc != nil && strings.Contains(c, "vue") {
			out = append(out, EntryPoint{Kind: EntryUIRoot, Name: f.Path, Framework: "vue", Path: f.Path, Line: at(loc)})
		} else if loc := reNgRoot.FindStringIndex(c); loc != nil {
			out = append(out, EntryPoint{Kind: EntryUIRoot, Name: f.Path, Framework: "angular", Path: f.Path, Line: at(loc)})
		}
		if e, ok := uiRoutes(f); ok {
			out = append(out, e)
		}
		if reJSCLIUse.MatchString(c) {
			var items []string
			for _, m := range reJSCommander.FindAllStringSubmatch(c, -1) {
				items = append(items, m[1])
			}
			fw := "commander"
			if strings.Contains(c, "yargs") {
				fw = "yargs"
			}
			out = append(out, EntryPoint{Kind: EntryCLI, Name: f.Path, Framework: fw, Path: f.Path, Line: 1, Items: items})
		}
		if loc := reJSServer.FindStringIndex(c); loc != nil && regexp.MustCompile(`['"](?:express|fastify|koa|http|https|@hapi/hapi|@nestjs/core)['"]`).MatchString(c) {
			out = append(out, EntryPoint{Kind: EntryHTTP, Name: "server start", Framework: "node", Path: f.Path, Line: at(loc), Detail: strings.TrimSpace(lineAt(c, at(loc)))})
		}
	}
	if rosNode != "" {
		out = append(out, EntryPoint{Kind: EntryROSNode, Name: rosNode, Framework: rosFramework, Path: f.Path, Line: rosLine})
	}
	return out
}

func firstOr(items []string, def string) string {
	if len(items) > 0 {
		return items[0]
	}
	return def
}

// pyDecoratedCommands reads click/typer commands: a @x.command()/@x.group()
// decorator names the function below it (or the name= it passes).
func pyDecoratedCommands(c string) ([]string, int) {
	if !strings.Contains(c, "click") && !strings.Contains(c, "typer") {
		return nil, 0
	}
	lines := strings.Split(c, "\n")
	var out []string
	first := 0
	for i, ln := range lines {
		m := reClickCmd.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		if first == 0 {
			first = i + 1
		}
		name := m[2]
		if name == "" {
			for k := i + 1; k < len(lines) && k < i+8; k++ {
				if d := rePyDefName.FindStringSubmatch(lines[k]); d != nil {
					name = strings.ReplaceAll(d[1], "_", "-")
					break
				}
			}
		}
		out = append(out, name)
	}
	return out, first
}

// rustSubcommands lists the variants of a #[derive(Subcommand)] enum.
func rustSubcommands(c string) []string {
	lines := strings.Split(c, "\n")
	var out []string
	for i, ln := range lines {
		if !strings.Contains(ln, "Subcommand") || !strings.Contains(ln, "derive") {
			continue
		}
		k := i + 1
		for k < len(lines) && !strings.Contains(lines[k], "enum ") {
			k++
		}
		for k++; k < len(lines) && strings.TrimSpace(lines[k]) != "}"; k++ {
			if m := reRustVariant.FindStringSubmatch(lines[k]); m != nil && !strings.HasPrefix(strings.TrimSpace(lines[k]), "//") {
				out = append(out, strings.ToLower(m[1]))
			}
		}
	}
	return out
}

// uiRoutes reads a router definition: JSX <Route path=…> elements, or route
// objects ({ path: '…', component: … }) in a file that uses a router library.
func uiRoutes(f File) (EntryPoint, bool) {
	c := f.Content
	var items []string
	first := 0
	fw := ""
	for _, m := range reRouteTag.FindAllStringSubmatchIndex(c, -1) {
		tagEnd := m[1] + 200
		if tagEnd > len(c) {
			tagEnd = len(c)
		}
		item := c[m[2]:m[3]]
		if e := reRouteElem.FindStringSubmatch(c[m[0]:tagEnd]); e != nil {
			item += " → " + e[1] + e[2]
		}
		items = append(items, item)
		if first == 0 {
			first = lineOf(c, m[0])
		}
		fw = "react-router"
	}
	if fw == "" && reRouterLib.MatchString(c) {
		lines := strings.Split(c, "\n")
		for i, ln := range lines {
			m := reRouteObjPath.FindStringSubmatch(ln)
			if m == nil {
				continue
			}
			item := m[1]
			for k := i; k < len(lines) && k < i+4; k++ {
				if cm := reRouteObjComp.FindStringSubmatch(lines[k]); cm != nil {
					item += " → " + cm[1] + cm[2] + cm[3]
					break
				}
			}
			items = append(items, item)
			if first == 0 {
				first = i + 1
			}
		}
		switch {
		case strings.Contains(c, "vue-router"):
			fw = "vue-router"
		case strings.Contains(c, "@angular/router") || strings.Contains(c, "Routes = ["):
			fw = "angular-router"
		default:
			fw = "react-router"
		}
	}
	if len(items) == 0 {
		return EntryPoint{}, false
	}
	it, n := boundItems(items)
	return EntryPoint{Kind: EntryUIRoutes, Name: f.Path, Framework: fw, Path: f.Path, Line: first, Items: it, Count: n}, true
}

// nextRoute maps a Next.js file-system route (pages/… or app/…/page.tsx) to
// its URL; API routes (pages/api) are HTTP, not pages, and are skipped.
func nextRoute(p string) (string, bool) {
	ext := filepath.Ext(p)
	switch ext {
	case ".js", ".jsx", ".ts", ".tsx":
	default:
		return "", false
	}
	segs := strings.Split(filepath.ToSlash(p), "/")
	for i, s := range segs {
		rest := segs[i+1:]
		switch {
		case s == "pages" && len(rest) > 0:
			if rest[0] == "api" || strings.HasPrefix(rest[len(rest)-1], "_") {
				return "", false
			}
			r := strings.TrimSuffix(strings.Join(rest, "/"), ext)
			r = strings.TrimSuffix(strings.TrimSuffix(r, "index"), "/")
			return "/" + r, true
		case s == "app" && len(rest) > 0 && strings.TrimSuffix(rest[len(rest)-1], ext) == "page":
			var keep []string
			for _, x := range rest[:len(rest)-1] {
				if !strings.HasPrefix(x, "(") {
					keep = append(keep, x)
				}
			}
			return "/" + strings.Join(keep, "/"), true
		}
	}
	return "", false
}

func launchEntry(f File) (EntryPoint, bool) {
	var items []string
	n := 0
	first := 0
	for _, m := range reLaunchNode.FindAllStringSubmatchIndex(f.Content, -1) {
		attrs := map[string]string{}
		for _, a := range reXMLAttr.FindAllStringSubmatch(f.Content[m[2]:m[3]], -1) {
			attrs[a[1]] = a[2]
		}
		n++
		if first == 0 {
			first = lineOf(f.Content, m[0])
		}
		exe := attrs["type"]
		if exe == "" {
			exe = attrs["exec"]
		}
		items = append(items, fmt.Sprintf("%s (%s/%s)", attrs["name"], attrs["pkg"], exe))
	}
	if n == 0 {
		return EntryPoint{}, false
	}
	it, _ := boundItems(items)
	return EntryPoint{Kind: EntryLaunch, Name: filepath.Base(f.Path), Framework: "roslaunch", Path: f.Path, Line: first, Items: it, Count: n}, true
}

func dockerEntry(f File) (EntryPoint, bool) {
	var from []string
	var cmd, entry string
	cmdLine, entryLine := 0, 0
	lines := strings.Split(f.Content, "\n")
	for i := 0; i < len(lines); i++ {
		ln, start := lines[i], i
		// An instruction continued with trailing backslashes is one instruction.
		for strings.HasSuffix(strings.TrimRight(ln, " \t"), "\\") && i+1 < len(lines) {
			i++
			ln = strings.TrimSuffix(strings.TrimRight(ln, " \t"), "\\") + " " + strings.TrimSpace(lines[i])
		}
		m := reDockerInstr.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		switch strings.ToUpper(m[1]) {
		case "FROM":
			from = append(from, strings.Fields(m[2])[0])
		case "CMD":
			cmd, cmdLine = strings.Join(strings.Fields(m[2]), " "), start+1
		case "ENTRYPOINT":
			entry, entryLine = strings.Join(strings.Fields(m[2]), " "), start+1
		}
	}
	if len(from) == 0 && cmd == "" && entry == "" {
		return EntryPoint{}, false
	}
	var detail []string
	line := 1
	if entry != "" {
		detail = append(detail, "ENTRYPOINT "+entry)
		line = entryLine
	}
	if cmd != "" {
		detail = append(detail, "CMD "+cmd)
		if entry == "" {
			line = cmdLine
		}
	}
	if len(detail) == 0 {
		detail = append(detail, "no CMD/ENTRYPOINT (inherits the base image's)")
	}
	d := strings.Join(detail, "; ")
	if len(d) > 240 {
		d = d[:240] + "…"
	}
	return EntryPoint{Kind: EntryDocker, Name: f.Path, Path: f.Path, Line: line, Detail: d, Items: from}, true
}

// yamlChildren lists the keys directly under a top-level key (compose
// services:, workflow jobs:) — a structural read of indentation, no YAML
// library needed for keys.
func yamlChildren(content, top string) []string {
	var out []string
	in := false
	indent := -1
	for _, ln := range strings.Split(content, "\n") {
		if strings.TrimSpace(ln) == "" || strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		if m := reYAMLTopKey.FindStringSubmatch(ln); m != nil {
			in = m[1] == top && strings.TrimSpace(m[2]) == ""
			indent = -1
			continue
		}
		if !in {
			continue
		}
		if m := reYAMLChildKey.FindStringSubmatch(ln); m != nil {
			if indent < 0 {
				indent = len(m[1])
			}
			if len(m[1]) == indent {
				out = append(out, m[2])
			}
		}
	}
	return out
}

func ciEntry(f File) (EntryPoint, bool) {
	base := strings.ToLower(filepath.Base(f.Path))
	switch {
	case base == "jenkinsfile":
		var items []string
		for _, m := range reJenkinsStage.FindAllStringSubmatch(f.Content, -1) {
			items = append(items, m[1])
		}
		return EntryPoint{Kind: EntryCI, Name: f.Path, Framework: "jenkins", Path: f.Path, Line: 1, Items: items}, len(items) > 0
	case base == ".gitlab-ci.yml":
		var items []string
		for _, ln := range strings.Split(f.Content, "\n") {
			if m := reYAMLTopKey.FindStringSubmatch(ln); m != nil && !gitlabReserved[m[1]] && !strings.HasPrefix(m[1], ".") {
				items = append(items, m[1])
			}
		}
		return EntryPoint{Kind: EntryCI, Name: f.Path, Framework: "gitlab-ci", Path: f.Path, Line: 1, Items: items}, len(items) > 0
	}
	jobs := yamlChildren(f.Content, "jobs")
	if len(jobs) == 0 {
		return EntryPoint{}, false
	}
	name := filepath.Base(f.Path)
	var triggers []string
	for _, ln := range strings.Split(f.Content, "\n") {
		if m := reYAMLTopKey.FindStringSubmatch(ln); m != nil {
			switch m[1] {
			case "name":
				name = strings.Trim(strings.TrimSpace(m[2]), `"'`)
			case "on", `"on"`, "'on'", "true":
				if v := strings.TrimSpace(m[2]); v != "" {
					triggers = append(triggers, strings.Trim(v, "[]"))
				}
			}
		}
	}
	if len(triggers) == 0 {
		for _, k := range []string{"on", "true"} {
			triggers = append(triggers, yamlChildren(f.Content, k)...)
		}
	}
	d := ""
	if len(triggers) > 0 {
		d = "on: " + strings.Join(triggers, ", ")
	}
	return EntryPoint{Kind: EntryCI, Name: name, Framework: "github-actions", Path: f.Path, Line: 1, Detail: d, Items: jobs}, true
}

func makeTargets(content string) []string {
	var out []string
	var phony []string
	for _, ln := range strings.Split(content, "\n") {
		if strings.HasPrefix(ln, "\t") || strings.HasPrefix(ln, " ") || strings.Contains(ln, ":=") || strings.Contains(ln, "?=") {
			continue
		}
		if strings.HasPrefix(ln, ".PHONY:") {
			phony = append(phony, strings.Fields(strings.TrimPrefix(ln, ".PHONY:"))...)
			continue
		}
		if m := reMakeTarget.FindStringSubmatch(ln); m != nil && !strings.Contains(ln, "%") {
			out = append(out, strings.Fields(m[1])...)
		}
	}
	if len(out) == 0 {
		return phony
	}
	return out
}

// manifestEntries reads entry points a manifest declares: package.json
// scripts and bin, pyproject/setup.cfg console scripts, CMake executables and
// catkin-installed scripts, Cargo [[bin]] targets.
func manifestEntries(f File) []EntryPoint {
	base := strings.ToLower(filepath.Base(f.Path))
	var out []EntryPoint
	switch base {
	case "package.json":
		var pj struct {
			Name    string            `json:"name"`
			Scripts map[string]string `json:"scripts"`
			Bin     json.RawMessage   `json:"bin"`
		}
		if json.Unmarshal([]byte(f.Content), &pj) != nil {
			return nil
		}
		if len(pj.Scripts) > 0 {
			keys := sortedKeys(pj.Scripts)
			sort.SliceStable(keys, func(i, j int) bool { return scriptRank(keys[i]) < scriptRank(keys[j]) })
			var items []string
			for _, k := range keys {
				items = append(items, k+": "+truncStr(pj.Scripts[k], 60))
			}
			out = append(out, EntryPoint{Kind: EntryScripts, Name: f.Path, Framework: "npm", Path: f.Path, Line: jsonKeyLine(f.Content, "scripts"), Items: items})
		}
		if len(pj.Bin) > 0 {
			var one string
			var many map[string]string
			var items []string
			if json.Unmarshal(pj.Bin, &one) == nil {
				items = append(items, pj.Name+" → "+one)
			} else if json.Unmarshal(pj.Bin, &many) == nil {
				for _, k := range sortedKeys(many) {
					items = append(items, k+" → "+many[k])
				}
			}
			if len(items) > 0 {
				out = append(out, EntryPoint{Kind: EntryCLI, Name: firstOr(items, f.Path), Framework: "npm bin", Path: f.Path, Line: jsonKeyLine(f.Content, "bin"), Items: items})
			}
		}
	case "pyproject.toml":
		var items []string
		section := ""
		for _, ln := range strings.Split(f.Content, "\n") {
			t := strings.TrimSpace(ln)
			if m := reTomlSection.FindStringSubmatch(t); m != nil {
				section = m[1]
				continue
			}
			if section == "project.scripts" || section == "tool.poetry.scripts" {
				if km := reTomlKey.FindStringSubmatch(t); km != nil {
					items = append(items, km[1]+" → "+strings.Trim(strings.TrimSpace(km[2]), `"'`))
				}
			}
		}
		if len(items) > 0 {
			out = append(out, EntryPoint{Kind: EntryCLI, Name: firstOr(items, f.Path), Framework: "console_scripts", Path: f.Path, Line: 1, Items: items})
		}
	case "cmakelists.txt":
		src := stripCMakeComments(f.Content)
		var items []string
		first := 0
		for _, m := range reAddExecutable.FindAllStringSubmatchIndex(src, -1) {
			items = append(items, src[m[2]:m[3]])
			if first == 0 {
				first = lineOf(src, m[0])
			}
		}
		if len(items) > 0 {
			out = append(out, EntryPoint{Kind: EntryBinary, Name: f.Path, Framework: "cmake", Path: f.Path, Line: first, Items: items})
		}
		if m := reCatkinPython.FindStringSubmatchIndex(src); m != nil {
			out = append(out, EntryPoint{Kind: EntryBinary, Name: f.Path, Framework: "catkin python", Path: f.Path, Line: lineOf(src, m[0]), Items: strings.Fields(src[m[2]:m[3]])})
		}
	case "cargo.toml":
		var items []string
		section := ""
		for _, ln := range strings.Split(f.Content, "\n") {
			t := strings.TrimSpace(ln)
			if strings.HasPrefix(t, "[") {
				section = t
				continue
			}
			if section == "[[bin]]" {
				if km := reTomlKey.FindStringSubmatch(t); km != nil && km[1] == "name" {
					items = append(items, strings.Trim(strings.TrimSpace(km[2]), `"'`))
				}
			}
		}
		if len(items) > 0 {
			out = append(out, EntryPoint{Kind: EntryBinary, Name: f.Path, Framework: "cargo", Path: f.Path, Line: 1, Items: items})
		}
	case "setup.cfg":
		if i := strings.Index(f.Content, "console_scripts"); i >= 0 {
			var items []string
			for _, ln := range strings.Split(f.Content[i:], "\n")[1:] {
				if !strings.HasPrefix(ln, " ") && !strings.HasPrefix(ln, "\t") {
					break
				}
				if t := strings.TrimSpace(ln); t != "" {
					items = append(items, t)
				}
			}
			if len(items) > 0 {
				out = append(out, EntryPoint{Kind: EntryCLI, Name: firstOr(items, f.Path), Framework: "console_scripts", Path: f.Path, Line: lineOf(f.Content, i), Items: items})
			}
		}
	}
	return out
}

// scriptRank orders the scripts an agent runs first ahead of the rest.
func scriptRank(k string) int {
	switch k {
	case "start", "dev", "serve":
		return 0
	case "build":
		return 1
	case "test":
		return 2
	}
	return 3
}

func jsonKeyLine(content, key string) int {
	if i := strings.Index(content, `"`+key+`"`); i >= 0 {
		return lineOf(content, i)
	}
	return 1
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// httpEntries reads every HTTP route the routes extractors recognize and
// groups them per framework: route count, the file serving most of them,
// and a few example routes. Spring request mappings, which routes does not
// pair with clients, are counted here lexically.
func httpEntries(repo string, files []File) []EntryPoint {
	routes := serverRoutes(repo, files)
	byFW := map[string][]Route{}
	for _, r := range routes {
		if IsTestPath(r.Path) {
			continue
		}
		byFW[r.Framework] = append(byFW[r.Framework], r)
	}
	var out []EntryPoint
	for _, fw := range sortedKeys(byFW) {
		rs := byFW[fw]
		perFile := map[string]int{}
		for _, r := range rs {
			perFile[r.Path]++
		}
		top := ""
		for _, p := range sortedKeys(perFile) {
			if top == "" || perFile[p] > perFile[top] {
				top = p
			}
		}
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].Pattern < rs[j].Pattern })
		var items []string
		for _, r := range rs {
			items = append(items, strings.TrimSpace(r.Method+" /"+strings.TrimPrefix(r.Pattern, "/")))
		}
		it, _ := boundItems(items)
		line := 0
		for _, r := range rs {
			if r.Path == top && (line == 0 || r.Line < line) {
				line = r.Line
			}
		}
		out = append(out, EntryPoint{Kind: EntryHTTP, Name: fw + " routes", Framework: fw, Repo: repo, Path: top, Line: line, Items: it, Count: len(rs),
			Detail: fmt.Sprintf("%d routes in %d files", len(rs), len(perFile))})
	}
	return out
}

// RankEntryPoints orders entry points: by kind (main and CLI first, CI last),
// then shallower paths, then path and line — deterministic.
func RankEntryPoints(eps []EntryPoint) {
	sort.SliceStable(eps, func(i, j int) bool {
		a, b := eps[i], eps[j]
		if entryRank[a.Kind] != entryRank[b.Kind] {
			return entryRank[a.Kind] < entryRank[b.Kind]
		}
		if a.Kind == EntryHTTP && (a.Count > 0) != (b.Count > 0) {
			return a.Count > 0
		}
		da, db := strings.Count(a.Path, "/"), strings.Count(b.Path, "/")
		if da != db {
			return da < db
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
}

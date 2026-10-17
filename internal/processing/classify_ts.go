//go:build treesitter

package processing

import (
	"path"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

// classifyFile decides what the index keeps of one tracked file. ig is the
// ignore set (default vendored dirs, .reponiteignore, --exclude); finish, when
// set, adds per-walker facts to a parsed source file (the working tree's SCIP
// monikers).
func classifyFile(in fileInput, ig *Ignore, repo string, normVer int, finish func(*ParsedFile)) (classified, error) {
	p := in.path
	quiet := func(why string) (classified, error) {
		return classified{skip: &SkippedFile{Path: p, Reason: why, Quiet: true}}, nil
	}
	if ig != nil && ig.Excluded(p, false) {
		return quiet("excluded by an ignore rule (vendored, .reponiteignore or --exclude)")
	}
	if inNoiseDir(p) {
		return quiet("tool or cache directory")
	}
	keep := func(pf ParsedFile, ok bool) (classified, error) {
		if !ok {
			return quiet("not stored (empty or over the size limit for its kind)")
		}
		return classified{pf: &pf}, nil
	}
	read := func(limit int64) (string, bool) {
		if limit > 0 && in.size > limit {
			return "", false
		}
		src, err := in.read()
		return src, err == nil
	}
	switch {
	case IsDataFile(p):
		src, ok := read(maxDataFileBytes)
		if !ok {
			return quiet("larger than a schema/config file")
		}
		return keep(dataFile(p, src))
	case IsManifestFile(p):
		src, ok := read(0)
		if !ok {
			return quiet("unreadable")
		}
		c, err := keep(projectFile(p, src))
		c.manifest = []byte(src)
		return c, err
	case IsProjectFile(p) && !IsROSParamFile(p):
		src, ok := read(query.MaxProjectFileBytes)
		if !ok {
			return quiet("larger than a project file")
		}
		return keep(projectFile(p, src))
	case IsROSFile(p):
		src, err := in.read()
		if err != nil {
			return classified{}, err
		}
		return keep(rosFile(p, src))
	case IsROSLaunchFile(p):
		src, ok := read(0)
		if !ok {
			return quiet("unreadable")
		}
		return keep(launchFile(p, src), true)
	case IsROSParamFile(p):
		src, ok := read(maxParamFileBytes)
		if !ok {
			return quiet("larger than a parameter file")
		}
		return keep(paramFile(p, src))
	case query.IsSFCPath(p):
		src, ok := read(0)
		if !ok {
			return quiet("unreadable")
		}
		return keep(sfcFile(p, src, normVer))
	case IsRobotFile(p):
		src, ok := read(maxContentFileBytes)
		if !ok {
			return classified{skip: &SkippedFile{Path: p, Reason: "larger than a hand-written suite"}}, nil
		}
		return keep(robotFile(p, src), true)
	case IsContentFile(p):
		src, ok := read(maxContentFileBytes)
		if !ok {
			return quiet("larger than a content file")
		}
		return keep(contentFile(p, src))
	}
	return sourceOrText(in, repo, normVer, finish)
}

// sourceOrText parses a source file, or stores any other text.
func sourceOrText(in fileInput, repo string, normVer int, finish func(*ParsedFile)) (classified, error) {
	p := in.path
	ext := path.Ext(p)
	if _, known := RulesForExt(ext); !known && ext != "" {
		return textFile(in), nil
	}
	if why := skipSourceReason(p, in.size, nil); why != "" {
		return classified{skip: &SkippedFile{Path: p, Reason: why, Quiet: ext == ""}}, nil
	}
	src, err := in.read()
	if err != nil {
		return classified{}, err
	}
	if why := skipSourceReason(p, int64(len(src)), []byte(src)); why != "" {
		return classified{skip: &SkippedFile{Path: p, Reason: why, Quiet: ext == "" && !strings.HasPrefix(src, "#!")}}, nil
	}
	rules, ext, ok := RulesForSource(ext, []byte(src))
	if !ok {
		if ext != "" {
			return textFile(in), nil
		}
		// an extension-less file is a script when its shebang says so, else text
		line := src
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		if rules, ok = RulesForShebang(line); !ok {
			return textFile(fileInput{path: p, size: in.size, read: func() (string, error) { return src, nil }}), nil
		}
		ext = rules.Exts[0]
	}
	load := in.load
	job := func() (ParsedFile, bool, error) {
		root, spans, perr := parseFileRules([]byte(src), ext, rules)
		if perr != nil || root == nil {
			return ParsedFile{}, false, perr // nil root: no grammar bound for this extension
		}
		defer releaseAST(root)
		syms := Extract(root, rules, normVer)
		sealSymbols(repo, rules.Name, normVer, syms)
		pf := ParsedFile{
			Path: p, Lang: rules.Name, IsTest: IsTestPath(p),
			Symbols: syms, Spans: spans, Imports: Imports(root, rules),
			Types: CppTypeFacts(root, rules), Load: load,
		}
		if load == nil {
			pf.Content = src
		}
		if finish != nil {
			finish(&pf)
		}
		return pf, true, nil
	}
	return classified{job: job, lang: rules.Name}, nil
}

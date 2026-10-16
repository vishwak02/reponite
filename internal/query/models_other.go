// models_other.go recognizes the non-Python data models: TypeORM/MikroORM/
// sequelize-typescript entities (decorators), Prisma schema models, GORM
// structs, SQL DDL replayed across migration files, and protobuf messages.
package query

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// --- TypeScript entities ------------------------------------------------------

var (
	tsEntityDec = regexp.MustCompile(`@(Entity|Table)\s*\(`)
	tsClassDecl = regexp.MustCompile(`\bclass\s+(\w+)`)
	tsPropDecl  = regexp.MustCompile(`^\s*(?:(?:public|private|protected|readonly|declare|static|override)\s+)*(\w+)\s*[?!]?\s*:\s*([^;=]+?)\s*(?:=\s*([^;]+?))?\s*;?\s*$`)
	tsDecName   = regexp.MustCompile(`^@(\w+)`)
	tsArrowType = regexp.MustCompile(`\(\s*\)\s*=>\s*(\w+)`)
	tsInverse   = regexp.MustCompile(`=>\s*\w+\s*\.\s*(\w+)`)
	tsOptBool   = func(opts, key string) bool { return regexp.MustCompile(`\b` + key + `\s*:\s*true`).MatchString(opts) }
	tsOptValue  = func(opts, key string) string {
		if m := regexp.MustCompile(`\b` + key + `\s*:\s*(['"` + "`" + `]?)([^,}'"` + "`" + `]+)['"` + "`" + `]?`).FindStringSubmatch(opts); m != nil {
			return strings.TrimSpace(m[2])
		}
		return ""
	}
)

var tsColumnDecs = map[string]bool{"Column": true, "PrimaryGeneratedColumn": true, "PrimaryColumn": true, "CreateDateColumn": true,
	"UpdateDateColumn": true, "DeleteDateColumn": true, "VersionColumn": true, "ObjectIdColumn": true, "Property": true,
	"PrimaryKey": true, "Enum": true, "ForeignKey": true}

var tsRelDecs = map[string]string{"ManyToOne": "fk", "OneToMany": "has_many", "OneToOne": "o2o", "ManyToMany": "m2m",
	"BelongsTo": "fk", "HasMany": "has_many", "HasOne": "has_one", "BelongsToMany": "m2m"}

// blockEnd is the index just past the brace block that opens at or after i.
func blockEnd(src string, i int) (open, end int) {
	open = strings.IndexByte(src[i:], '{')
	if open < 0 {
		return -1, -1
	}
	open += i
	depth := 0
	var q byte
	for k := open; k < len(src); k++ {
		c := src[k]
		if q != 0 {
			if c == '\\' {
				k++
			} else if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			q = c
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return open, k + 1
			}
		}
	}
	return open, len(src)
}

func lineOf(src string, pos int) int { return 1 + strings.Count(src[:pos], "\n") }

func tsModels(repo string, files []File) []DataModel {
	var out []DataModel
	for _, f := range files {
		if codeExts[strings.ToLower(path.Ext(f.Path))] != "js" || !tsEntityDec.MatchString(f.Content) {
			continue
		}
		fw := "typeorm"
		switch {
		case strings.Contains(f.Content, "@mikro-orm"):
			fw = "mikro-orm"
		case strings.Contains(f.Content, "sequelize-typescript"):
			fw = "sequelize-typescript"
		}
		src := f.Content
		for _, m := range tsEntityDec.FindAllStringSubmatchIndex(src, -1) {
			args := callArgs(src, m[1])
			rest := src[m[1]+len(args):]
			cm := tsClassDecl.FindStringSubmatchIndex(rest)
			if cm == nil || strings.Contains(rest[:cm[0]], "@"+src[m[2]:m[3]]+"(") {
				continue
			}
			name := rest[cm[2]:cm[3]]
			at := m[1] + len(args) + cm[0]
			open, end := blockEnd(src, at)
			if open < 0 {
				continue
			}
			md := DataModel{Name: name, Kind: KindTable, Framework: fw, Repo: repo, Path: f.Path, Line: lineOf(src, at),
				EndLine: lineOf(src, end-1), Table: snakeCase(name), Test: IsTestPath(f.Path)}
			if lit, ok := jsStringLit(firstArg(args)); ok {
				md.Table = lit
			} else if v := tsOptValue(args, "(?:name|tableName)"); v != "" {
				md.Table = v
			}
			tsMembers(&md, src, open+1, end-1)
			out = append(out, md)
		}
	}
	return out
}

func jsStringLit(a string) (string, bool) {
	a = strings.TrimSpace(a)
	if len(a) >= 2 && strings.ContainsRune(`"'`+"`", rune(a[0])) && a[len(a)-1] == a[0] {
		return a[1 : len(a)-1], true
	}
	return "", false
}

// tsMembers reads decorated properties of an entity body.
func tsMembers(md *DataModel, src string, from, to int) {
	body := src[from:to]
	base := lineOf(src, from) - 1
	lines := strings.Split(body, "\n")
	var decs []string
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*") || t == "" {
			continue
		}
		for strings.HasPrefix(t, "@") {
			// A decorator, possibly multi-line, possibly followed by the
			// property on the same line.
			d := t
			for parenDelta(d) > 0 && i+1 < len(lines) {
				i++
				d += " " + strings.TrimSpace(lines[i])
			}
			dm := tsDecName.FindStringSubmatch(d)
			if dm == nil {
				break
			}
			cut := len(dm[0])
			if strings.HasPrefix(d[cut:], "(") {
				cut += len(callArgs(d, cut+1)) + 2
			}
			if cut > len(d) {
				cut = len(d)
			}
			decs = append(decs, d[:cut])
			t = strings.TrimSpace(d[cut:])
		}
		if t == "" {
			continue
		}
		pm := tsPropDecl.FindStringSubmatch(t)
		if pm == nil || strings.Contains(t, "(") && !strings.Contains(pm[2], "=>") {
			decs = nil
			continue
		}
		if len(decs) == 0 {
			continue
		}
		line := base + i + 1
		f := DataField{Name: pm[1], Type: strings.TrimSpace(pm[2]), Line: line, Default: strings.TrimSpace(pm[3])}
		isField, rel := false, DataRelation{}
		for _, d := range decs {
			name := tsDecName.FindStringSubmatch(d)[1]
			opts := ""
			if k := strings.Index(d, "("); k >= 0 {
				opts = d[k:]
			}
			switch {
			case tsColumnDecs[name]:
				isField = true
				if strings.HasPrefix(name, "Primary") {
					f.PK, f.Unique = true, true
				}
				if tsOptBool(opts, "nullable") {
					f.Null = true
				}
				if tsOptBool(opts, "unique") {
					f.Unique = true
				}
				if tsOptBool(opts, "primary") || tsOptBool(opts, "primaryKey") {
					f.PK = true
				}
				if v := tsOptValue(opts, "default"); v != "" && f.Default == "" {
					f.Default = v
				}
				if v := tsOptValue(opts, "(?:name|field)"); v != "" {
					f.Column = v
				}
				if lit, ok := jsStringLit(firstArg(strings.TrimPrefix(opts, "("))); ok {
					f.Type = lit
				} else if v := tsOptValue(opts, "type"); v != "" {
					f.Type = v
				}
				if name == "ForeignKey" {
					if tm := tsArrowType.FindStringSubmatch(opts); tm != nil {
						md.Relations = append(md.Relations, DataRelation{Field: f.Name, Kind: "fk", Raw: tm[1], Line: line})
					}
				}
			case name == "Index" || name == "Unique":
				f.Index = true
				if name == "Unique" {
					f.Unique = true
				}
			case name == "AllowNull":
				f.Null = true
			case tsRelDecs[name] != "":
				rel = DataRelation{Field: f.Name, Kind: tsRelDecs[name], Line: line}
				if tm := tsArrowType.FindStringSubmatch(opts); tm != nil {
					rel.Raw = tm[1]
				} else if lit, ok := jsStringLit(firstArg(strings.TrimPrefix(opts, "("))); ok {
					rel.Raw = lit
				}
				if im := tsInverse.FindStringSubmatch(opts); im != nil {
					rel.Reverse = im[1]
				}
				if tsOptBool(opts, "nullable") {
					f.Null = true
				}
			}
		}
		decs = nil
		if rel.Kind != "" {
			if rel.Raw == "" {
				rel.Raw = strings.TrimSuffix(strings.TrimSpace(f.Type), "[]")
			}
			md.Relations = append(md.Relations, rel)
			continue
		}
		if isField {
			md.Fields = append(md.Fields, f)
		}
	}
}

// --- Prisma ---------------------------------------------------------------------

var (
	prismaBlock = regexp.MustCompile(`(?m)^\s*(model|enum|type|view)\s+(\w+)\s*\{`)
	prismaField = regexp.MustCompile(`^(\w+)\s+([\w.]+)(\[\])?(\?)?\s*(.*)$`)
	prismaAttr  = regexp.MustCompile(`@([\w.]+)(\((?:[^()]|\([^()]*\))*\))?`)
)

func prismaModels(repo string, files []File) []DataModel {
	var out []DataModel
	for _, f := range files {
		if path.Ext(f.Path) != ".prisma" {
			continue
		}
		src := f.Content
		blocks := prismaBlock.FindAllStringSubmatchIndex(src, -1)
		models := map[string]bool{}
		for _, b := range blocks {
			if k := src[b[2]:b[3]]; k == "model" || k == "type" || k == "view" {
				models[src[b[4]:b[5]]] = true
			}
		}
		for _, b := range blocks {
			kind := src[b[2]:b[3]]
			if kind == "enum" {
				continue
			}
			name := src[b[4]:b[5]]
			open, end := blockEnd(src, b[0])
			md := DataModel{Name: name, Kind: KindTable, Framework: "prisma", Table: name, Repo: repo, Path: f.Path,
				Line: lineOf(src, b[0]), EndLine: lineOf(src, end-1), Test: IsTestPath(f.Path)}
			switch kind {
			case "view":
				md.Kind = KindView
			case "type":
				md.Kind, md.Table = KindSchema, ""
			}
			base := lineOf(src, open+1) - 1
			for i, ln := range strings.Split(src[open+1:end-1], "\n") {
				t := strings.TrimSpace(ln)
				if c := strings.Index(t, "//"); c >= 0 {
					t = strings.TrimSpace(t[:c])
				}
				if strings.HasPrefix(t, "@@") {
					switch {
					case strings.HasPrefix(t, "@@map("):
						if v, ok := jsStringLit(strings.TrimSuffix(strings.TrimPrefix(t, "@@map("), ")")); ok {
							md.Table = v
						}
					case strings.HasPrefix(t, "@@index(") || strings.HasPrefix(t, "@@unique(") || strings.HasPrefix(t, "@@id("):
						for _, fld := range regexp.MustCompile(`\[\s*(\w+)`).FindAllStringSubmatch(t, 1) {
							for j := range md.Fields {
								if md.Fields[j].Name == fld[1] {
									md.Fields[j].Index = true
									md.Fields[j].PK = md.Fields[j].PK || strings.HasPrefix(t, "@@id(")
								}
							}
						}
					}
					continue
				}
				fm := prismaField.FindStringSubmatch(t)
				if fm == nil {
					continue
				}
				line := base + i + 1
				fd := DataField{Name: fm[1], Type: fm[2] + fm[3], Null: fm[4] == "?", Line: line}
				var relArgs string
				hasRel := false
				for _, a := range prismaAttr.FindAllStringSubmatch(fm[5], -1) {
					switch a[1] {
					case "id":
						fd.PK, fd.Unique = true, true
					case "unique":
						fd.Unique = true
					case "default":
						fd.Default = strings.TrimSuffix(strings.TrimPrefix(a[2], "("), ")")
					case "map":
						if v, ok := jsStringLit(strings.TrimSuffix(strings.TrimPrefix(a[2], "("), ")")); ok {
							fd.Column = v
						}
					case "relation":
						hasRel, relArgs = true, a[2]
					}
				}
				if models[fm[2]] {
					rel := DataRelation{Field: fm[1], Raw: fm[2], Target: fm[2], Line: line}
					switch {
					case fm[3] == "[]":
						rel.Kind = "has_many"
					case strings.Contains(relArgs, "fields"):
						rel.Kind = "fk"
						if fk := regexp.MustCompile(`fields\s*:\s*\[\s*(\w+)`).FindStringSubmatch(relArgs); fk != nil {
							rel.Field = fk[1]
						}
					default:
						rel.Kind = "has_one"
					}
					_ = hasRel
					md.Relations = append(md.Relations, rel)
					continue
				}
				md.Fields = append(md.Fields, fd)
			}
			out = append(out, md)
		}
	}
	return out
}

// --- GORM -------------------------------------------------------------------------

var (
	goStructDecl = regexp.MustCompile(`(?m)^type\s+(\w+)\s+struct\s*\{`)
	goFieldLine  = regexp.MustCompile("^([A-Z]\\w*(?:\\s*,\\s*[A-Z]\\w*)*)\\s+([\\w.*\\[\\]]+)\\s*(`[^`]*`)?")
	goEmbedLine  = regexp.MustCompile("^(\\*?[\\w.]+)\\s*(`[^`]*`)?\\s*$")
	goTableName  = regexp.MustCompile(`func\s*\(\s*(?:\w+\s+)?\*?(\w+)\s*\)\s*TableName\(\)\s*string\s*\{\s*return\s*"([^"]+)"`)
	gormTagRe    = regexp.MustCompile(`gorm:"([^"]*)"`)
)

type goStruct struct {
	name, path string
	line, end  int
	fields     []string // raw field lines with their line numbers
	lines      []int
	gorm       bool
	file       *File
}

func gormModels(repo string, files []File) []DataModel {
	structs := map[string]*goStruct{}
	var order []*goStruct
	tables := map[string]string{}
	for i := range files {
		f := &files[i]
		if path.Ext(f.Path) != ".go" || !strings.Contains(f.Content, "struct") {
			continue
		}
		importsGorm := strings.Contains(f.Content, `gorm.io/gorm"`) || strings.Contains(f.Content, `jinzhu/gorm"`)
		for _, m := range goTableName.FindAllStringSubmatch(f.Content, -1) {
			tables[m[1]] = m[2]
		}
		for _, m := range goStructDecl.FindAllStringSubmatchIndex(f.Content, -1) {
			open, end := blockEnd(f.Content, m[0])
			st := &goStruct{name: f.Content[m[2]:m[3]], path: f.Path, line: lineOf(f.Content, m[0]), end: lineOf(f.Content, end-1), file: f}
			base := lineOf(f.Content, open+1) - 1
			for k, ln := range strings.Split(f.Content[open+1:end-1], "\n") {
				t := strings.TrimSpace(ln)
				if t == "" || strings.HasPrefix(t, "//") {
					continue
				}
				st.fields = append(st.fields, t)
				st.lines = append(st.lines, base+k+1)
				if strings.Contains(t, `gorm:"`) || importsGorm && (t == "gorm.Model" || strings.HasPrefix(t, "gorm.Model ")) {
					st.gorm = true
				}
			}
			structs[st.name] = st
			order = append(order, st)
		}
		for _, m := range goTableName.FindAllStringSubmatch(f.Content, -1) {
			if st := structs[m[1]]; st != nil && importsGorm {
				st.gorm = true
			}
		}
	}
	// A struct embedding a GORM model is one too.
	for changed := true; changed; {
		changed = false
		for _, st := range order {
			if st.gorm {
				continue
			}
			for _, t := range st.fields {
				if em := goEmbedLine.FindStringSubmatch(t); em != nil {
					if b := structs[lastDot(strings.TrimPrefix(em[1], "*"))]; b != nil && b.gorm {
						st.gorm, changed = true, true
					}
				}
			}
		}
	}
	var out []DataModel
	for _, st := range order {
		if !st.gorm {
			continue
		}
		md := DataModel{Name: st.name, Kind: KindTable, Framework: "gorm", Repo: repo, Path: st.path, Line: st.line, EndLine: st.end,
			Table: pluralize(snakeCase(st.name)), Test: IsTestPath(st.path)}
		if t, ok := tables[st.name]; ok {
			md.Table = t
		}
		gormFields(&md, st, structs, map[string]bool{})
		out = append(out, md)
	}
	return out
}

func gormFields(md *DataModel, st *goStruct, structs map[string]*goStruct, seen map[string]bool) {
	if seen[st.name] {
		return
	}
	seen[st.name] = true
	names := map[string]bool{}
	for _, t := range st.fields {
		if fm := goFieldLine.FindStringSubmatch(t); fm != nil {
			for _, n := range strings.Split(fm[1], ",") {
				names[strings.TrimSpace(n)] = true
			}
		}
	}
	for k, t := range st.fields {
		line := st.lines[k]
		if t == "gorm.Model" || strings.HasPrefix(t, "gorm.Model ") {
			for _, f := range []DataField{{Name: "ID", Type: "uint", PK: true, Unique: true}, {Name: "CreatedAt", Type: "time.Time"},
				{Name: "UpdatedAt", Type: "time.Time"}, {Name: "DeletedAt", Type: "gorm.DeletedAt", Index: true, Null: true}} {
				f.Line, f.Inherited, f.Column = line, "gorm.Model", snakeCase(f.Name)
				md.Fields = append(md.Fields, f)
			}
			continue
		}
		if em := goEmbedLine.FindStringSubmatch(t); em != nil && !strings.Contains(t, " ") || em != nil && strings.HasPrefix(em[2], "`") && !strings.Contains(strings.TrimSpace(strings.TrimSuffix(t, em[2])), " ") {
			if b := structs[lastDot(strings.TrimPrefix(em[1], "*"))]; b != nil {
				before := len(md.Fields)
				gormFields(md, b, structs, seen)
				for i := before; i < len(md.Fields); i++ {
					if md.Fields[i].Inherited == "" {
						md.Fields[i].Inherited = b.name
					}
				}
			}
			continue
		}
		fm := goFieldLine.FindStringSubmatch(t)
		if fm == nil {
			continue
		}
		tag := ""
		if g := gormTagRe.FindStringSubmatch(fm[3]); g != nil {
			tag = g[1]
		}
		if tag == "-" || strings.HasPrefix(tag, "-;") {
			continue
		}
		typ := fm[2]
		elem := strings.TrimLeft(typ, "*[]")
		for _, n := range strings.Split(fm[1], ",") {
			n = strings.TrimSpace(n)
			if target := structs[lastDot(elem)]; target != nil && target.gorm || strings.Contains(tag, "foreignKey") || strings.Contains(tag, "many2many") {
				rel := DataRelation{Field: n, Raw: lastDot(elem), Line: line}
				switch {
				case strings.Contains(tag, "many2many"):
					rel.Kind = "m2m"
				case strings.HasPrefix(typ, "[]") || strings.HasPrefix(typ, "*[]"):
					rel.Kind = "has_many"
				case names[n+"ID"]:
					rel.Kind = "fk"
				default:
					rel.Kind = "has_one"
				}
				md.Relations = append(md.Relations, rel)
				continue
			}
			f := DataField{Name: n, Type: typ, Line: line, Column: snakeCase(n), Null: strings.HasPrefix(typ, "*") || strings.HasPrefix(typ, "sql.Null")}
			for _, part := range strings.Split(tag, ";") {
				k, v, _ := strings.Cut(strings.TrimSpace(part), ":")
				switch strings.ToLower(k) {
				case "primarykey", "primary_key":
					f.PK, f.Unique = true, true
				case "unique":
					f.Unique = true
				case "uniqueindex":
					f.Unique, f.Index = true, true
				case "index":
					f.Index = true
				case "not null":
					f.Null = false
				case "default":
					f.Default = v
				case "column":
					f.Column = v
				case "type":
					f.Type = v
				}
			}
			if n == "ID" && !strings.Contains(tag, "primaryKey") {
				f.PK, f.Unique = true, true
			}
			if f.Column == snakeCase(n) {
				f.Column = ""
			}
			md.Fields = append(md.Fields, f)
		}
	}
}

// --- SQL DDL ------------------------------------------------------------------------

var (
	sqlCreateTable = regexp.MustCompile(`(?is)^create\s+(?:or\s+replace\s+)?(?:(?:global\s+|local\s+)?(?:temporary|temp|unlogged)\s+)?table\s+(?:if\s+not\s+exists\s+)?([\w."` + "`" + `\[\]]+)\s*\(`)
	sqlCreateView  = regexp.MustCompile(`(?is)^create\s+(?:or\s+replace\s+)?(?:materialized\s+)?view\s+(?:if\s+not\s+exists\s+)?([\w."` + "`" + `\[\]]+)`)
	sqlCreateIndex = regexp.MustCompile(`(?is)^create\s+(unique\s+)?index\s+(?:concurrently\s+)?(?:if\s+not\s+exists\s+)?(?:[\w."` + "`" + `]+\s+)?on\s+(?:only\s+)?([\w."` + "`" + `\[\]]+)\s*(?:using\s+\w+\s*)?\(([^)]*)\)`)
	sqlAlterTable  = regexp.MustCompile(`(?is)^alter\s+table\s+(?:if\s+exists\s+)?(?:only\s+)?([\w."` + "`" + `\[\]]+)\s+(.*)$`)
	sqlDropTable   = regexp.MustCompile(`(?is)^drop\s+table\s+(?:if\s+exists\s+)?([\w."` + "`" + `\[\], ]+?)(?:\s+cascade|\s+restrict)?$`)
	sqlRefRe       = regexp.MustCompile(`(?i)\breferences\s+([\w."` + "`" + `\[\]]+)\s*(?:\(\s*([\w"` + "`" + `]+)\s*\))?`)
	sqlDefaultRe   = regexp.MustCompile(`(?i)\bdefault\s+('(?:[^']|'')*'|\([^)]*\)|[\w.:]+(?:\([^)]*\))?)`)
	sqlFKRe        = regexp.MustCompile(`(?i)foreign\s+key\s*\(\s*([\w"` + "`" + `]+)`)
	sqlColsRe      = regexp.MustCompile(`\(([^)]*)\)`)
	sqlTypeStop    = map[string]bool{"not": true, "null": true, "primary": true, "unique": true, "default": true, "references": true,
		"check": true, "constraint": true, "generated": true, "auto_increment": true, "autoincrement": true, "collate": true, "identity": true, "comment": true}
)

func sqlIdent(s string) string { return strings.Trim(strings.TrimSpace(s), "\"`[]") }

// sqlTable: schema-qualified name → (bare name, name as written).
func sqlTable(s string) (string, string) {
	parts := strings.Split(s, ".")
	for i := range parts {
		parts[i] = sqlIdent(parts[i])
	}
	return parts[len(parts)-1], strings.Join(parts, ".")
}

// sqlStatements splits SQL text into statements with their start lines,
// comments removed and quoted text and $$ bodies kept intact.
func sqlStatements(src string) []pyStmt {
	var out []pyStmt
	var b strings.Builder
	line, start := 1, 1
	inDollar := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\n' {
			line++
		}
		if !inDollar && c == '-' && i+1 < len(src) && src[i+1] == '-' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			line++
			b.WriteByte('\n')
			continue
		}
		if !inDollar && c == '/' && i+1 < len(src) && src[i+1] == '*' {
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				break
			}
			line += strings.Count(src[i:i+2+j], "\n")
			i += j + 3
			continue
		}
		if c == '$' && i+1 < len(src) && src[i+1] == '$' {
			inDollar = !inDollar
		}
		if c == '\'' && !inDollar {
			j := i + 1
			for j < len(src) && (src[j] != '\'' || j+1 < len(src) && src[j+1] == '\'' && func() bool { j++; return true }()) {
				j++
			}
			if j >= len(src) {
				j = len(src) - 1
			}
			line += strings.Count(src[i:j+1], "\n")
			b.WriteString(src[i : j+1])
			i = j
			continue
		}
		if c == ';' && !inDollar {
			if t := strings.TrimSpace(b.String()); t != "" {
				out = append(out, pyStmt{text: t, line: start})
			}
			b.Reset()
			continue
		}
		if b.Len() == 0 || strings.TrimSpace(b.String()) == "" {
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				b.Reset()
				start = line
				if c == '\n' {
					start = line
				}
				continue
			}
			start = line
		}
		b.WriteByte(c)
	}
	if t := strings.TrimSpace(b.String()); t != "" {
		out = append(out, pyStmt{text: t, line: start})
	}
	return out
}

// sqlDownMigration: a rollback file would undo the schema it follows.
func sqlDownMigration(p string) bool {
	l := strings.ToLower(p)
	return strings.Contains(l, ".down.") || strings.Contains(l, "_down.") || strings.Contains(l, "/down/") ||
		strings.Contains(l, "rollback") || strings.HasSuffix(l, "down.sql")
}

// sqlModels replays the DDL of every .sql file (in path order — the order
// migration tools apply them) into tables with their final columns.
func sqlModels(repo string, files []File) []DataModel {
	var sqlFiles []File
	for _, f := range files {
		if strings.ToLower(path.Ext(f.Path)) == ".sql" && !sqlDownMigration(f.Path) {
			sqlFiles = append(sqlFiles, f)
		}
	}
	sort.Slice(sqlFiles, func(i, j int) bool { return sqlFiles[i].Path < sqlFiles[j].Path })
	tables := map[string]*DataModel{}
	var order []string
	get := func(name string) *DataModel { return tables[strings.ToLower(name)] }
	for _, f := range sqlFiles {
		for _, st := range sqlStatements(f.Content) {
			text := st.text
			if m := sqlCreateTable.FindStringSubmatchIndex(text); m != nil {
				name, full := sqlTable(text[m[2]:m[3]])
				md := &DataModel{Name: name, Kind: KindTable, Framework: "sql", Table: full, Repo: repo, Path: f.Path, Line: st.line,
					EndLine: st.line + strings.Count(text, "\n"), Test: IsTestPath(f.Path)}
				body := callArgs(text, m[1])
				for _, item := range splitArgs(body) {
					sqlColumnOrConstraint(md, strings.TrimSpace(item), st.line+strings.Count(text[:strings.Index(text, item)+1], "\n"))
				}
				key := strings.ToLower(name)
				if _, ok := tables[key]; !ok {
					order = append(order, key)
				}
				tables[key] = md
				continue
			}
			if m := sqlCreateView.FindStringSubmatch(text); m != nil {
				name, full := sqlTable(m[1])
				key := strings.ToLower(name)
				if _, ok := tables[key]; !ok {
					order = append(order, key)
				}
				tables[key] = &DataModel{Name: name, Kind: KindView, Framework: "sql", Table: full, Repo: repo, Path: f.Path, Line: st.line,
					EndLine: st.line + strings.Count(text, "\n"), Test: IsTestPath(f.Path)}
				continue
			}
			if m := sqlCreateIndex.FindStringSubmatch(text); m != nil {
				name, _ := sqlTable(m[2])
				if md := get(name); md != nil {
					cols := strings.Split(m[3], ",")
					for i := range md.Fields {
						if strings.EqualFold(md.Fields[i].Name, sqlIdent(strings.Fields(cols[0] + " ")[0])) {
							md.Fields[i].Index = true
							if m[1] != "" && len(cols) == 1 {
								md.Fields[i].Unique = true
							}
						}
					}
					sqlNoteSource(md, f.Path)
				}
				continue
			}
			if m := sqlAlterTable.FindStringSubmatch(text); m != nil {
				name, _ := sqlTable(m[1])
				md := get(name)
				if md == nil {
					continue
				}
				sqlNoteSource(md, f.Path)
				for _, action := range splitArgs(m[2]) {
					sqlAlter(md, strings.TrimSpace(action), st.line, tables, &order)
				}
				continue
			}
			if m := sqlDropTable.FindStringSubmatch(text); m != nil {
				for _, t := range strings.Split(m[1], ",") {
					name, _ := sqlTable(t)
					delete(tables, strings.ToLower(name))
				}
			}
		}
	}
	var out []DataModel
	for _, k := range order {
		if md, ok := tables[k]; ok {
			out = append(out, *md)
			delete(tables, k) // a table renamed onto an existing key is emitted once
		}
	}
	return out
}

func sqlNoteSource(md *DataModel, p string) {
	if p != md.Path && !containsStr(md.Sources, p) {
		md.Sources = append(md.Sources, p)
	}
}

func sqlColumnOrConstraint(md *DataModel, item string, line int) {
	if item == "" {
		return
	}
	words := strings.Fields(item)
	head := strings.ToLower(words[0])
	switch head {
	case "constraint", "primary", "foreign", "unique", "check", "key", "index", "exclude", "fulltext", "spatial":
		low := strings.ToLower(item)
		cols := []string{}
		if cm := sqlColsRe.FindStringSubmatch(item); cm != nil {
			for _, c := range strings.Split(cm[1], ",") {
				cols = append(cols, sqlIdent(strings.Fields(c + " ")[0]))
			}
		}
		set := func(fn func(*DataField)) {
			for i := range md.Fields {
				for _, c := range cols {
					if strings.EqualFold(md.Fields[i].Name, c) {
						fn(&md.Fields[i])
					}
				}
			}
		}
		switch {
		case strings.Contains(low, "foreign key"):
			if fk := sqlFKRe.FindStringSubmatch(item); fk != nil {
				if ref := sqlRefRe.FindStringSubmatch(item); ref != nil {
					_, t := sqlTable(ref[1])
					md.Relations = append(md.Relations, DataRelation{Field: sqlIdent(fk[1]), Kind: "fk", Raw: t, Line: line})
				}
			}
		case strings.Contains(low, "primary key"):
			set(func(f *DataField) { f.PK, f.Unique = true, len(cols) == 1; f.Null = false })
		case strings.Contains(low, "unique"):
			if len(cols) == 1 {
				set(func(f *DataField) { f.Unique = true })
			}
		case head == "key" || head == "index":
			set(func(f *DataField) { f.Index = true })
		}
		return
	case "like":
		return
	}
	f := DataField{Name: sqlIdent(words[0]), Line: line, Null: true}
	var typ []string
	i := 1
	for ; i < len(words); i++ {
		if sqlTypeStop[strings.ToLower(words[i])] {
			break
		}
		typ = append(typ, words[i])
	}
	f.Type = strings.Join(typ, " ")
	rest := strings.ToLower(strings.Join(words[i:], " "))
	if strings.Contains(rest, "not null") {
		f.Null = false
	}
	if strings.Contains(rest, "primary key") {
		f.PK, f.Unique, f.Null = true, true, false
	}
	if regexp.MustCompile(`\bunique\b`).MatchString(rest) {
		f.Unique = true
	}
	if strings.HasPrefix(strings.ToLower(f.Type), "serial") || strings.HasPrefix(strings.ToLower(f.Type), "bigserial") {
		f.Null = false
	}
	if d := sqlDefaultRe.FindStringSubmatch(item); d != nil {
		f.Default = d[1]
	}
	if ref := sqlRefRe.FindStringSubmatch(item); ref != nil {
		_, t := sqlTable(ref[1])
		md.Relations = append(md.Relations, DataRelation{Field: f.Name, Kind: "fk", Raw: t, Line: line})
	}
	md.Fields = append(md.Fields, f)
}

var (
	sqlAddCol    = regexp.MustCompile(`(?is)^add\s+(?:column\s+)?(?:if\s+not\s+exists\s+)?(.*)$`)
	sqlDropCol   = regexp.MustCompile(`(?is)^drop\s+(?:column\s+)?(?:if\s+exists\s+)?([\w"` + "`" + `]+)`)
	sqlRenameCol = regexp.MustCompile(`(?is)^rename\s+(?:column\s+)?([\w"` + "`" + `]+)\s+to\s+([\w"` + "`" + `]+)`)
	sqlRenameTo  = regexp.MustCompile(`(?is)^rename\s+to\s+([\w."` + "`" + `]+)`)
	sqlAlterCol  = regexp.MustCompile(`(?is)^(?:alter|modify)\s+(?:column\s+)?([\w"` + "`" + `]+)\s+(.*)$`)
)

func sqlAlter(md *DataModel, a string, line int, tables map[string]*DataModel, order *[]string) {
	low := strings.ToLower(a)
	switch {
	case sqlRenameTo.MatchString(a):
		name, full := sqlTable(sqlRenameTo.FindStringSubmatch(a)[1])
		delete(tables, strings.ToLower(md.Name))
		md.Name, md.Table = name, full
		tables[strings.ToLower(name)] = md
		*order = append(*order, strings.ToLower(name))
	case sqlRenameCol.MatchString(a):
		m := sqlRenameCol.FindStringSubmatch(a)
		for i := range md.Fields {
			if strings.EqualFold(md.Fields[i].Name, sqlIdent(m[1])) {
				md.Fields[i].Name = sqlIdent(m[2])
			}
		}
	case strings.HasPrefix(low, "add constraint") || strings.HasPrefix(low, "add primary") || strings.HasPrefix(low, "add foreign") || strings.HasPrefix(low, "add unique"):
		sqlColumnOrConstraint(md, strings.TrimSpace(a[3:]), line)
	case sqlAddCol.MatchString(a):
		sqlColumnOrConstraint(md, sqlAddCol.FindStringSubmatch(a)[1], line)
	case strings.HasPrefix(low, "drop constraint"):
	case sqlDropCol.MatchString(a):
		name := sqlIdent(sqlDropCol.FindStringSubmatch(a)[1])
		var keep []DataField
		for _, f := range md.Fields {
			if !strings.EqualFold(f.Name, name) {
				keep = append(keep, f)
			}
		}
		md.Fields = keep
	case sqlAlterCol.MatchString(a):
		m := sqlAlterCol.FindStringSubmatch(a)
		rest := strings.ToLower(m[2])
		for i := range md.Fields {
			f := &md.Fields[i]
			if !strings.EqualFold(f.Name, sqlIdent(m[1])) {
				continue
			}
			switch {
			case strings.Contains(rest, "set not null"):
				f.Null = false
			case strings.Contains(rest, "drop not null"):
				f.Null = true
			case strings.HasPrefix(rest, "set default"):
				f.Default = strings.TrimSpace(m[2][len("set default"):])
			case strings.HasPrefix(rest, "drop default"):
				f.Default = ""
			case strings.HasPrefix(rest, "type ") || strings.HasPrefix(rest, "set data type "):
				f.Type = strings.TrimSpace(m[2][strings.Index(rest, "type ")+5:])
				if k := strings.Index(strings.ToLower(f.Type), " using "); k >= 0 {
					f.Type = f.Type[:k]
				}
			}
		}
	}
}

// --- protobuf ---------------------------------------------------------------------

var (
	protoPkg   = regexp.MustCompile(`(?m)^\s*package\s+([\w.]+)\s*;`)
	protoOpen  = regexp.MustCompile(`^\s*(message|enum|oneof|service|extend)\s+(\w+)\s*\{`)
	protoField = regexp.MustCompile(`^\s*(?:(optional|required|repeated)\s+)?(map\s*<\s*[\w.]+\s*,\s*[\w.]+\s*>|[\w.]+)\s+(\w+)\s*=\s*(\d+)\s*(\[[^\]]*\])?\s*;`)
	protoMapV  = regexp.MustCompile(`map\s*<\s*[\w.]+\s*,\s*([\w.]+)\s*>`)
)

var protoScalar = map[string]bool{"double": true, "float": true, "int32": true, "int64": true, "uint32": true, "uint64": true,
	"sint32": true, "sint64": true, "fixed32": true, "fixed64": true, "sfixed32": true, "sfixed64": true, "bool": true,
	"string": true, "bytes": true}

func protoModels(repo string, files []File) []DataModel {
	var out []DataModel
	for _, f := range files {
		if path.Ext(f.Path) != ".proto" {
			continue
		}
		pkg := ""
		if m := protoPkg.FindStringSubmatch(f.Content); m != nil {
			pkg = m[1]
		}
		type frame struct {
			kind string
			idx  int // index into out for a message, -1 otherwise
			name string
		}
		var stack []frame
		for i, ln := range strings.Split(f.Content, "\n") {
			if c := strings.Index(ln, "//"); c >= 0 {
				ln = ln[:c]
			}
			if m := protoOpen.FindStringSubmatch(ln); m != nil {
				fr := frame{kind: m[1], idx: -1, name: m[2]}
				if m[1] == "message" {
					name := m[2]
					for k := len(stack) - 1; k >= 0; k-- {
						if stack[k].kind == "message" {
							name = stack[k].name + "." + name
							break
						}
					}
					fr.name = name
					out = append(out, DataModel{Name: name, Kind: KindWire, Framework: "protobuf", Package: pkg, Repo: repo, Path: f.Path,
						Line: i + 1, EndLine: i + 1, Test: IsTestPath(f.Path)})
					fr.idx = len(out) - 1
				}
				stack = append(stack, fr)
				if strings.Contains(ln[strings.Index(ln, "{"):], "}") {
					stack = stack[:len(stack)-1]
				}
				continue
			}
			// The innermost message (a oneof's fields belong to its message).
			owner := -1
			for k := len(stack) - 1; k >= 0; k-- {
				if stack[k].kind == "message" {
					owner = stack[k].idx
					break
				}
				if stack[k].kind == "enum" || stack[k].kind == "service" {
					break
				}
			}
			if fm := protoField.FindStringSubmatch(ln); fm != nil && owner >= 0 {
				md := &out[owner]
				typ := strings.ReplaceAll(fm[2], " ", "")
				fd := DataField{Name: fm[3], Type: typ, Line: i + 1, Null: fm[1] == "optional"}
				if fm[1] == "repeated" {
					fd.Type = "repeated " + typ
				}
				md.Fields = append(md.Fields, fd)
				ref := typ
				if mv := protoMapV.FindStringSubmatch(typ); mv != nil {
					ref = mv[1]
				}
				if !protoScalar[ref] && !strings.HasPrefix(ref, "google.protobuf.") {
					kind := "ref"
					if fm[1] == "repeated" || ref != typ {
						kind = "ref_many"
					}
					md.Relations = append(md.Relations, DataRelation{Field: fm[3], Kind: kind, Raw: strings.TrimPrefix(ref, "."), Line: i + 1})
				}
			}
			for n := strings.Count(ln, "}"); n > 0 && len(stack) > 0; n-- {
				top := stack[len(stack)-1]
				if top.idx >= 0 {
					out[top.idx].EndLine = i + 1
				}
				stack = stack[:len(stack)-1]
			}
		}
	}
	// Proto references are by (possibly nested/qualified) message name.
	names := map[string]string{}
	for _, m := range out {
		names[m.Name] = m.Name
		names[lastDot(m.Name)] = m.Name
		if m.Package != "" {
			names[m.Package+"."+m.Name] = m.Name
		}
	}
	for i := range out {
		for j := range out[i].Relations {
			r := &out[i].Relations[j]
			if n, ok := names[r.Raw]; ok {
				r.Target = n
			} else if n, ok := names[out[i].Name+"."+r.Raw]; ok {
				r.Target = n
			} else if n, ok := names[lastDot(r.Raw)]; ok {
				r.Target = n
			}
		}
	}
	return out
}

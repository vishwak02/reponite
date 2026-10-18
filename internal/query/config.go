// config.go is a codebase's configuration surface: every environment
// variable, CLI flag, settings key, feature flag and ROS parameter the code
// READS (with the default it falls back to), joined to every place the repo
// SETS it (.env files, docker-compose environment, Kubernetes/Helm env,
// Dockerfile ENV/ARG, CI workflow env, launch files and their rosparam YAML,
// settings modules, .properties, shell exports, code that writes the
// environment). A key read but never set — and one set but never read — is
// flagged, because that is where "the config change did not take effect"
// lives. Pure over Store.Files (ADR-018); idiom-based, so medium confidence.
package query

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/vishwak02/reponite/internal/roslaunch"
)

// DefaultConfigBudget is the token budget when the caller passes <= 0.
const DefaultConfigBudget = 4000

// Config key kinds.
const (
	CfgEnv      = "env"
	CfgFlag     = "flag"
	CfgSetting  = "setting"
	CfgFeature  = "feature"
	CfgROSParam = "rosparam"
)

var cfgKindRank = map[string]int{CfgEnv: 0, CfgSetting: 1, CfgFlag: 2, CfgFeature: 3, CfgROSParam: 4}

// ConfigRead is one place the code reads a key.
type ConfigRead struct {
	Kind, Key  string
	Repo, Path string
	Line       int
	In, Handle string
	Default    string
	HasDefault bool
	Via        string // os.getenv, process.env, argparse, settings, pydantic-settings, viper, …
	Text       string
}

// ConfigSet is one place the repo sets a key.
type ConfigSet struct {
	Kind, Key  string
	Value      string
	Repo, Path string
	Line       int
	Source     string // dotenv, compose, k8s, dockerfile, ci, launch, settings, properties, shell, code, cli
}

// ConfigKey is one key with its reads and sets.
type ConfigKey struct {
	Kind, Key string
	Reads     []ConfigRead
	Sets      []ConfigSet
	NReads    int
	NSets     int
	Defaults  []string
	Status    string // "", read_never_set, set_never_read
	Note      string
}

// ConfigResult is the configuration surface (or one key's slice of it).
type ConfigResult struct {
	Query        string
	Keys         []ConfigKey
	ReadNeverSet []ConfigKey // reads only (sites trimmed to the first)
	SetNeverRead []ConfigKey // sets only (sites trimmed to the first)
	Counts       map[string]int
	Total        int
	Truncated    bool
	Omitted      int
	Next         string
	Note         string
	Meta         Meta
}

// ConfigOptions bound a config query.
type ConfigOptions struct {
	Budget int
	Kind   string // keep one kind; "" = all
}

// Config maps the configuration surface across repo (FleetRepo = all) at ref.
func Config(s Store, repo, ref, key string, opt ConfigOptions) ConfigResult {
	if opt.Budget <= 0 {
		opt.Budget = DefaultConfigBudget
	}
	res := ConfigResult{Query: key, Counts: map[string]int{}, Meta: NewMeta(s, repo, ref)}
	keys := collectConfig(s, repo, ref)
	for _, k := range keys {
		res.Counts[k.Kind]++
	}
	res.Total = len(keys)
	if key != "" {
		configDetail(&res, keys, key, opt)
		return res
	}
	configList(&res, keys, opt)
	return res
}

// collectConfig scans every repo in scope and returns each key with all of
// its reads and sets, classified.
func collectConfig(s Store, repo, ref string) []ConfigKey {
	var reads []ConfigRead
	var sets []ConfigSet
	repos := reposFor(s, repo)
	all := map[string][]File{}
	for _, rp := range repos {
		files := s.Files(rp, ref)
		all[rp] = files
		syms := s.SymbolsAt(rp, ref)
		r, st := scanConfig(rp, files, syms)
		reads = append(reads, r...)
		sets = append(sets, st...)
	}
	sets = append(sets, rosParamSets(all)...)
	keys := groupConfig(reads, sets)
	// A key only ever set gets one more look: a shell/compose/Dockerfile
	// expansion ($KEY, ${KEY}) reads it without any language API.
	expansionReads(keys, all)
	for i := range keys {
		classifyKey(&keys[i])
	}
	return keys
}

func configList(res *ConfigResult, keys []ConfigKey, opt ConfigOptions) {
	var keep []ConfigKey
	for _, k := range keys {
		if opt.Kind == "" || k.Kind == opt.Kind {
			keep = append(keep, k)
		}
	}
	sort.SliceStable(keep, func(i, j int) bool {
		a, b := keep[i], keep[j]
		if cfgKindRank[a.Kind] != cfgKindRank[b.Kind] {
			return cfgKindRank[a.Kind] < cfgKindRank[b.Kind]
		}
		if a.NReads != b.NReads {
			return a.NReads > b.NReads
		}
		return a.Key < b.Key
	})
	spent := 80
	brief := func(k ConfigKey) ConfigKey {
		b := k
		if len(b.Reads) > 1 {
			b.Reads = b.Reads[:1]
		}
		if len(b.Sets) > 1 {
			b.Sets = b.Sets[:1]
		}
		return b
	}
	// The flagged keys come first in the budget: they are the answer to
	// "why did my setting not take effect". They take at most 60% of it, so
	// the key list itself is never crowded out by a long flagged list.
	flagged := opt.Budget * 3 / 5
	for _, k := range keep {
		if k.Status == "" {
			continue
		}
		c := cfgKeyCost(brief(k))
		if spent+c > flagged {
			res.Omitted++
			continue
		}
		spent += c
		if k.Status == "read_never_set" {
			res.ReadNeverSet = append(res.ReadNeverSet, brief(k))
		} else {
			res.SetNeverRead = append(res.SetNeverRead, brief(k))
		}
	}
	for _, k := range keep {
		b := brief(k)
		b.Reads, b.Sets = nil, nil
		c := estTokens(b.Kind+b.Key+strings.Join(b.Defaults, ",")) + 10
		if spent+c > opt.Budget {
			res.Omitted++
			continue
		}
		spent += c
		res.Keys = append(res.Keys, b)
	}
	if res.Omitted > 0 {
		res.Truncated = true
		res.Next = fmt.Sprintf("%d entr(ies) omitted for budget — rerun with --budget %d, filter with --kind env|setting|flag|feature|rosparam, or name one key: config <KEY>", res.Omitted, opt.Budget*2)
	}
	if len(keys) == 0 {
		res.Note = "no configuration reads or sets recognized at this ref"
		return
	}
	res.Note = "reads: os.getenv/os.environ, django-environ/decouple, pydantic BaseSettings, Django settings, argparse/click, process.env/import.meta.env, commander, os.Getenv/viper/env tags/flag/cobra, getenv, std::env, System.getenv/@Value, ${VAR:-default}, ROS params, feature-flag calls; sets: .env, compose/k8s/CI env, Dockerfile ENV/ARG, launch <env>/<param>/<rosparam>, settings modules, .properties, export, code writing the environment, --flags passed in scripts — a key consumed by a library, a platform or a secret store outside the repo reads as never read/set here; name a key for every site"
}

func cfgKeyCost(k ConfigKey) int {
	c := estTokens(k.Kind+k.Key+strings.Join(k.Defaults, ",")) + 12
	for _, r := range k.Reads {
		c += estTokens(r.Path+r.In+r.Handle+r.Via+r.Text) + 8
	}
	for _, s := range k.Sets {
		c += estTokens(s.Path+s.Value+s.Source) + 8
	}
	return c
}

func configDetail(res *ConfigResult, keys []ConfigKey, q string, opt ConfigOptions) {
	var hit []ConfigKey
	for _, try := range []func(ConfigKey) bool{
		func(k ConfigKey) bool { return k.Key == q },
		func(k ConfigKey) bool { return canonCfg(k.Kind, k.Key) == canonCfg(k.Kind, q) },
		func(k ConfigKey) bool {
			return k.Kind == CfgROSParam && strings.HasSuffix(k.Key, "/"+roslaunch.NormParam(q))
		},
	} {
		for _, k := range keys {
			if (opt.Kind == "" || k.Kind == opt.Kind) && try(k) {
				hit = append(hit, k)
			}
		}
		if len(hit) > 0 {
			break
		}
	}
	if len(hit) == 0 {
		lq := strings.ToLower(q)
		var near []string
		for _, k := range keys {
			if strings.Contains(strings.ToLower(k.Key), lq) {
				near = append(near, k.Kind+":"+k.Key)
			}
		}
		sort.Strings(near)
		if len(near) > 10 {
			near = near[:10]
		}
		res.Note = fmt.Sprintf("no configuration key %q read or set at this ref", q)
		if len(near) > 0 {
			res.Note += "; near: " + strings.Join(near, ", ")
		}
		return
	}
	spent := 60
	for _, k := range hit {
		out := k
		out.Reads, out.Sets = nil, nil
		spent += estTokens(k.Kind+k.Key+strings.Join(k.Defaults, ",")) + 12
		for _, r := range k.Reads {
			c := estTokens(r.Path+r.In+r.Handle+r.Via+r.Text) + 10
			if spent+c > opt.Budget {
				res.Omitted++
				continue
			}
			spent += c
			out.Reads = append(out.Reads, r)
		}
		for _, s := range k.Sets {
			c := estTokens(s.Path+s.Value+s.Source) + 10
			if spent+c > opt.Budget {
				res.Omitted++
				continue
			}
			spent += c
			out.Sets = append(out.Sets, s)
		}
		res.Keys = append(res.Keys, out)
	}
	if res.Omitted > 0 {
		res.Truncated = true
		res.Next = fmt.Sprintf("%d site(s) omitted for budget — rerun with --budget %d", res.Omitted, opt.Budget*2)
	}
	res.Note = "every read (with its default) and every set of the key; a value set by a platform, secret store or deploy tool outside the indexed repos is not seen"
}

// canonCfg is the identity two spellings of a key share.
func canonCfg(kind, key string) string {
	switch kind {
	case CfgEnv:
		return strings.ToUpper(key)
	case CfgFlag:
		return strings.ReplaceAll(strings.ToLower(strings.TrimLeft(key, "-")), "-", "_")
	case CfgROSParam:
		return roslaunch.NormParam(key)
	}
	return strings.ToLower(key)
}

// groupConfig joins reads and sets by (kind, canonical key). A ROS parameter
// set under a namespace (node/x) joins the read of x.
func groupConfig(reads []ConfigRead, sets []ConfigSet) []ConfigKey {
	type id struct{ kind, key string }
	by := map[id]*ConfigKey{}
	var order []id
	get := func(kind, key string) *ConfigKey {
		k := id{kind, canonCfg(kind, key)}
		if c, ok := by[k]; ok {
			return c
		}
		c := &ConfigKey{Kind: kind, Key: key}
		by[k] = c
		order = append(order, k)
		return c
	}
	for _, r := range reads {
		c := get(r.Kind, r.Key)
		c.Reads = append(c.Reads, r)
		if r.HasDefault && !containsStr(c.Defaults, r.Default) {
			c.Defaults = append(c.Defaults, r.Default)
		}
	}
	readParams := map[string]bool{}
	for k := range by {
		if k.kind == CfgROSParam {
			readParams[k.key] = true
		}
	}
	for _, s := range sets {
		if s.Kind == CfgROSParam {
			key := canonCfg(CfgROSParam, s.Key)
			best := ""
			for rp := range readParams {
				if (key == rp || strings.HasSuffix(key, "/"+rp)) && len(rp) > len(best) {
					best = rp
				}
			}
			if best != "" {
				c := by[id{CfgROSParam, best}]
				c.Sets = append(c.Sets, s)
				continue
			}
		}
		c := get(s.Kind, s.Key)
		c.Sets = append(c.Sets, s)
	}
	out := make([]ConfigKey, 0, len(order))
	for _, k := range order {
		c := by[k]
		sortReads(c.Reads)
		sort.SliceStable(c.Sets, func(i, j int) bool {
			if c.Sets[i].Repo != c.Sets[j].Repo {
				return c.Sets[i].Repo < c.Sets[j].Repo
			}
			if c.Sets[i].Path != c.Sets[j].Path {
				return c.Sets[i].Path < c.Sets[j].Path
			}
			return c.Sets[i].Line < c.Sets[j].Line
		})
		out = append(out, *c)
	}
	return out
}

func sortReads(rs []ConfigRead) {
	sort.SliceStable(rs, func(i, j int) bool {
		if ti, tj := IsTestPath(rs[i].Path), IsTestPath(rs[j].Path); ti != tj {
			return !ti
		}
		if rs[i].Repo != rs[j].Repo {
			return rs[i].Repo < rs[j].Repo
		}
		if rs[i].Path != rs[j].Path {
			return rs[i].Path < rs[j].Path
		}
		return rs[i].Line < rs[j].Line
	})
}

// Settings a framework or a common library consumes itself: set in a
// settings module and never read by the project's own code, by design.
var frameworkSettings = map[string]bool{"SECRET_KEY": true, "DEBUG": true, "ALLOWED_HOSTS": true, "INSTALLED_APPS": true,
	"MIDDLEWARE": true, "ROOT_URLCONF": true, "TEMPLATES": true, "WSGI_APPLICATION": true, "ASGI_APPLICATION": true,
	"DATABASES": true, "AUTH_PASSWORD_VALIDATORS": true, "LANGUAGE_CODE": true, "TIME_ZONE": true, "USE_I18N": true,
	"USE_L10N": true, "USE_TZ": true, "STATIC_URL": true, "STATIC_ROOT": true, "STATICFILES_DIRS": true, "STATICFILES_STORAGE": true,
	"STATICFILES_FINDERS": true, "MEDIA_URL": true, "MEDIA_ROOT": true, "DEFAULT_AUTO_FIELD": true, "LOGGING": true, "CACHES": true,
	"AUTH_USER_MODEL": true, "AUTHENTICATION_BACKENDS": true, "LOGIN_URL": true, "LOGIN_REDIRECT_URL": true, "LOGOUT_REDIRECT_URL": true,
	"DEFAULT_FILE_STORAGE": true, "STORAGES": true, "SITE_ID": true, "X_FRAME_OPTIONS": true, "REST_FRAMEWORK": true,
	"SIMPLE_JWT": true, "SPECTACULAR_SETTINGS": true, "CHANNEL_LAYERS": true, "INTERNAL_IPS": true, "ADMINS": true, "MANAGERS": true,
	"APPEND_SLASH": true, "LOCALE_PATHS": true, "LANGUAGES": true, "FIXTURE_DIRS": true, "TEST_RUNNER": true, "DATABASE_ROUTERS": true,
	"PASSWORD_HASHERS": true, "MESSAGE_STORAGE": true, "FORM_RENDERER": true, "SILENCED_SYSTEM_CHECKS": true, "USE_X_FORWARDED_HOST": true}

var frameworkSettingPrefix = []string{"CORS_", "CELERY_", "EMAIL_", "SESSION_", "CSRF_", "SECURE_", "AWS_", "DEBUG_TOOLBAR", "SOCIAL_AUTH_",
	"ACCOUNT_", "OAUTH2_", "SENTRY_", "FILE_UPLOAD_", "DATA_UPLOAD_", "CRISPY_", "GRAPHENE", "OTEL_", "DJANGO_", "SWAGGER_", "REDOC_"}

// Environment variables the runtime or tooling consumes.
var runtimeEnv = map[string]bool{"PATH": true, "HOME": true, "PYTHONPATH": true, "PYTHONUNBUFFERED": true, "PYTHONDONTWRITEBYTECODE": true,
	"NODE_ENV": true, "TZ": true, "LANG": true, "LC_ALL": true, "DEBIAN_FRONTEND": true, "DJANGO_SETTINGS_MODULE": true,
	"GOPATH": true, "GOFLAGS": true, "CGO_ENABLED": true, "GOOS": true, "GOARCH": true, "NODE_OPTIONS": true, "PIP_NO_CACHE_DIR": true,
	"POETRY_VIRTUALENVS_CREATE": true, "VIRTUAL_ENV": true, "TERM": true, "USER": true, "SHELL": true, "ROS_MASTER_URI": true,
	"ROS_IP": true, "ROS_HOSTNAME": true, "ROS_DOMAIN_ID": true, "ROS_DISTRO": true, "LD_LIBRARY_PATH": true, "CI": true,
	"GITHUB_TOKEN": true, "GITHUB_REF": true, "GITHUB_SHA": true, "RUST_LOG": true, "RUST_BACKTRACE": true, "JAVA_OPTS": true,
	"POSTGRES_USER": true, "POSTGRES_PASSWORD": true, "POSTGRES_DB": true, "MYSQL_ROOT_PASSWORD": true, "MYSQL_DATABASE": true,
	"MYSQL_USER": true, "MYSQL_PASSWORD": true, "PGDATA": true, "DOCKER_BUILDKIT": true, "COMPOSE_PROJECT_NAME": true}

func classifyKey(k *ConfigKey) {
	k.NReads, k.NSets = len(k.Reads), len(k.Sets)
	switch k.Kind {
	case CfgFeature:
		if k.NSets == 0 {
			k.Note = "feature flags are usually set in a flag service, outside the repo"
		}
		return
	case CfgFlag:
		return // a flag is defined where it is read; being passed is optional
	}
	if strings.Contains(k.Key, "*") {
		k.Note = "name built at runtime (* varies)"
		return
	}
	switch {
	case k.NReads > 0 && k.NSets == 0:
		if k.Kind == CfgSetting && frameworkSettings[k.Key] {
			return
		}
		k.Status = "read_never_set"
		allDefault := true
		for _, r := range k.Reads {
			allDefault = allDefault && r.HasDefault
		}
		if allDefault {
			k.Note = "every read has a default"
		}
	case k.NSets > 0 && k.NReads == 0:
		if k.Kind == CfgEnv && runtimeEnv[strings.ToUpper(k.Key)] {
			k.Note = "consumed by the runtime/tooling"
			return
		}
		if k.Kind == CfgSetting {
			if frameworkSettings[k.Key] {
				return
			}
			for _, p := range frameworkSettingPrefix {
				if strings.HasPrefix(k.Key, p) {
					return
				}
			}
		}
		k.Status = "set_never_read"
		if k.Kind == CfgROSParam {
			k.Note = "may be read by a library node or a plugin under a name built at runtime"
		}
	}
}

// --- reads ---------------------------------------------------------------------

var (
	pyEnvCall    = regexp.MustCompile(`\b(os\.getenv|getenv|os\.environ\.get|environ\.get|os\.environ\.setdefault|environ\.setdefault)\s*\(`)
	pyEnvIndex   = regexp.MustCompile(`\b(?:os\.)?environ\s*\[\s*["']([\w.-]+)["']\s*\](\s*=[^=])?`)
	pyEnvironEnv = regexp.MustCompile(`\benv(?:\.(?:str|bool|int|float|list|tuple|dict|json|url|db_url|db|cache_url|cache|email_url|search_url|path|decimal|parse_value))?\s*\(`)
	pyDecouple   = regexp.MustCompile(`\bconfig\s*\(`)
	pyArgparse   = regexp.MustCompile(`\.add_argument\s*\(`)
	pyClick      = regexp.MustCompile(`@(?:click|cloup)\.(option|argument)\s*\(|\btyper\.(Option|Argument)\s*\(`)
	pySettingAt  = regexp.MustCompile(`\bsettings\.([A-Z][A-Z0-9_]+)\b`)
	pyGetattrSet = regexp.MustCompile(`\bgetattr\s*\(\s*settings\s*,\s*`)
	pySettingSet = regexp.MustCompile(`^([A-Z][A-Z0-9_]+)\s*(?::[^=]+)?\+?=\s*(.*)$`)
	pyEnvWrite   = regexp.MustCompile(`\b(?:os\.)?environ\s*\[\s*["']([\w.-]+)["']\s*\]\s*=\s*([^=\n].*)$|\bos\.putenv\(\s*["']([\w.-]+)["']\s*,\s*(.+)\)`)

	jsEnvDot      = regexp.MustCompile(`\b(process\.env|import\.meta\.env|Deno\.env\.get\()\s*(?:\.\s*([A-Za-z_]\w*)|\[\s*['"]([\w.-]+)['"]\s*\]|['"]([\w.-]+)['"]\s*\))`)
	jsEnvDefault  = regexp.MustCompile(`^\s*(\|\||\?\?)\s*(['"` + "`" + `][^'"` + "`" + `]*['"` + "`" + `]|-?[\d.]+|true|false)`)
	jsEnvAssign   = regexp.MustCompile(`^\s*=[^=]`)
	jsEnvDestruct = regexp.MustCompile(`\{([^}]*)\}\s*=\s*process\.env\b`)
	jsCommander   = regexp.MustCompile(`\.(?:option|requiredOption)\s*\(\s*['"]([^'"]*--[^'"]*)['"]`)

	goEnvCall = regexp.MustCompile(`\bos\.(Getenv|LookupEnv)\(\s*"([\w.-]+)"\s*\)`)
	// os.Getenv(name) with an identifier: resolved through the package's string constants.
	goEnvIdent = regexp.MustCompile(`\bos\.(Getenv|LookupEnv)\(\s*([A-Za-z_]\w*)\s*\)`)
	// `const name = "value"` and `name = "value"` inside a const block (typed or not).
	goStrConst  = regexp.MustCompile(`(?m)^\s*(?:const\s+)?([A-Za-z_]\w*)(?:\s+string)?\s*=\s*"([A-Za-z_][\w.-]*)"\s*(?://.*)?$`)
	goTagEnv    = regexp.MustCompile("`[^`]*\\b(env|envconfig)\\s*:\\s*\"([\\w.-]+)[^\"]*\"[^`]*`")
	goTagDef    = regexp.MustCompile(`\b(?:envDefault|default)\s*:\s*"([^"]*)"`)
	goViperGet  = regexp.MustCompile(`\bviper\.(Get\w*|IsSet)\(\s*"([\w.-]+)"\s*\)`)
	goViperDef  = regexp.MustCompile(`\bviper\.SetDefault\(\s*"([\w.-]+)"\s*,\s*(.+)\)`)
	goViperBind = regexp.MustCompile(`\bviper\.BindEnv\(\s*"([\w.-]+)"\s*(?:,\s*"([\w.-]+)")?`)
	goFlagCall  = regexp.MustCompile(`(?:\bflag|\bpflag|\.Flags\(\)|\.PersistentFlags\(\)|\bfs|\bflags|\b\w*[Ff]lagSet)\.(String|Bool|Int|Int64|Int32|Uint|Uint64|Float64|Float32|Duration|StringSlice|StringArray|IntSlice|Count|Func|BoolFunc|TextVar|Var)(Var)?(P)?\s*\(`)

	cEnvCall    = regexp.MustCompile(`\b(?:std::)?(?:secure_)?getenv\s*\(\s*"([\w.-]+)"|\bqgetenv\(\s*"([\w.-]+)"|\bqEnvironmentVariable\(\s*"([\w.-]+)"`)
	cEnvSet     = regexp.MustCompile(`\bsetenv\s*\(\s*"([\w.-]+)"\s*,\s*("(?:[^"\\]|\\.)*"|[^,]+)|\bputenv\s*\(\s*"([\w.-]+)=([^"]*)"`)
	rustEnv     = regexp.MustCompile(`\benv::var(?:_os)?\(\s*"([\w.-]+)"\s*\)|\b(?:option_)?env!\(\s*"([\w.-]+)"`)
	rustUnwrap  = regexp.MustCompile(`^\s*\.unwrap_or(?:_else)?\(\s*(?:\|\|\s*)?(?:String::from\()?("(?:[^"\\]|\\.)*")`)
	rustClapEnv = regexp.MustCompile(`#\[(?:arg|clap)\(([^\]]*)\)\]`)
	javaEnv     = regexp.MustCompile(`\bSystem\.getenv\(\s*"([\w.-]+)"\s*\)`)
	javaProp    = regexp.MustCompile(`\bSystem\.getProperty\(\s*"([\w.-]+)"\s*(?:,\s*("(?:[^"\\]|\\.)*"))?`)
	javaValue   = regexp.MustCompile(`@Value\(\s*"\$\{([\w.\-\[\]]+)(?::([^}]*))?\}"\s*\)`)
	shExpand    = regexp.MustCompile(`\$\{([A-Za-z_]\w*)(:?[-=?+])([^}]*)\}`)
	shExport    = regexp.MustCompile(`^\s*export\s+([A-Za-z_]\w*)=(.*)$`)

	featureCall = regexp.MustCompile(`\b(isFeatureEnabled|is_feature_enabled|isEnabled|is_enabled|flag_is_active|switch_is_active|sample_is_active|feature_enabled|isOn|getFeatureValue|useFlag|useFeatureFlag\w*|useFeatureIsOn|useFeatureValue|useVariable|boolVariation|stringVariation|numberVariation|jsonVariation|variation|isFeatureOn)\(\s*(?:[\w.]+\s*,\s*)?["'` + "`" + `]([\w.:\-]+)["'` + "`" + `]`)

	launchEnvRead = regexp.MustCompile(`\$\((env|optenv)\s+([A-Za-z_]\w*)(?:\s+([^)]*))?\)`)
	launchEnvSet  = regexp.MustCompile(`<env\s+name\s*=\s*"([\w.-]+)"\s+value\s*=\s*"([^"]*)"`)
	ciExprEnv     = regexp.MustCompile(`\$\{\{\s*env\.([A-Za-z_]\w*)\s*\}\}`)
	dynKeyPart    = regexp.MustCompile(`\$?\{[^}]*\}`)
)

type cfgScan struct {
	repo  string
	syms  map[string]SymbolRef
	reads []ConfigRead
	sets  []ConfigSet
	// goConsts maps a Go package directory to its string constants, so a key
	// read through a named constant (os.Getenv(storeDirEnv)) resolves to the
	// constant's value instead of vanishing.
	goConsts map[string]map[string]string
}

func (c *cfgScan) read(f File, pos int, kind, key, def string, hasDef bool, via string) {
	if key == "" {
		return
	}
	// A name built at runtime (f"TOKEN_{i}", "${prefix}_URL"): keep its
	// shape, the fixed parts, with * for what varies.
	key = dynKeyPart.ReplaceAllString(key, "*")
	line := dataLineOf(f.Content, pos)
	in, h := enclosingHandle(f, line, c.syms)
	c.reads = append(c.reads, ConfigRead{Kind: kind, Key: key, Repo: c.repo, Path: f.Path, Line: line, In: in, Handle: h,
		Default: dataClip(def), HasDefault: hasDef, Via: via, Text: dataClip(dataLineAt(f.Content, pos))})
}

func (c *cfgScan) set(f File, line int, kind, key, value, source string) {
	if key == "" {
		return
	}
	c.sets = append(c.sets, ConfigSet{Kind: kind, Key: key, Value: dataClip(unquoteVal(value)), Repo: c.repo, Path: f.Path, Line: line, Source: source})
}

func dataLineAt(s string, pos int) string {
	ls := strings.LastIndex(s[:pos], "\n") + 1
	le := strings.IndexByte(s[pos:], '\n')
	if le < 0 {
		return s[ls:]
	}
	return s[ls : pos+le]
}

func unquoteVal(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// scanConfig reads one repo's files for configuration reads and sets.
func scanConfig(repo string, files []File, syms map[string]SymbolRef) ([]ConfigRead, []ConfigSet) {
	c := &cfgScan{repo: repo, syms: syms, goConsts: map[string]map[string]string{}}
	for _, f := range files {
		if strings.HasSuffix(f.Path, ".go") {
			dir := path.Dir(f.Path)
			for _, m := range goStrConst.FindAllStringSubmatch(f.Content, -1) {
				if c.goConsts[dir] == nil {
					c.goConsts[dir] = map[string]string{}
				}
				c.goConsts[dir][m[1]] = m[2]
			}
		}
	}
	for _, f := range files {
		base := strings.ToLower(path.Base(f.Path))
		ext := strings.ToLower(path.Ext(f.Path))
		switch {
		case ext == ".py":
			c.python(f)
		case ext == ".go":
			c.golang(f)
		case ext == ".rs":
			c.rust(f)
		case ext == ".java" || ext == ".kt":
			c.java(f)
		case codeExts[ext] == "c":
			c.cpp(f)
		case codeExts[ext] == "js":
			c.js(f)
		case codeExts[ext] == "sh":
			c.shell(f)
		case ext == ".yaml" || ext == ".yml":
			c.yaml(f)
		case ext == ".launch" || ext == ".test" || strings.HasSuffix(base, ".launch.xml"):
			c.launch(f)
		case ext == ".properties":
			c.properties(f)
		case strings.HasPrefix(base, ".env") || ext == ".env":
			c.dotenv(f)
		case base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || ext == ".dockerfile" || strings.HasPrefix(base, "containerfile"):
			c.dockerfile(f)
		}
		if codeExts[ext] != "" {
			for _, m := range featureCall.FindAllStringSubmatchIndex(f.Content, -1) {
				c.read(f, m[0], CfgFeature, f.Content[m[4]:m[5]], "", false, f.Content[m[2]:m[3]])
			}
		}
	}
	c.pydanticSettings(files)
	return c.reads, c.sets
}

// callArgsAt splits the call whose "(" ends the match at pos.
func callArgsAt(src string, pos int) []string {
	var out []string
	for _, a := range splitArgs(callArgs(src, pos)) {
		out = append(out, strings.TrimSpace(a))
	}
	return out
}

func kwArg(args []string, name string) (string, bool) {
	for _, a := range args {
		if k, v, ok := strings.Cut(a, "="); ok && strings.TrimSpace(k) == name {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

func isSettingsModule(p string) bool {
	b := strings.ToLower(path.Base(p))
	return strings.Contains(b, "settings") || strings.Contains("/"+path.Dir(p)+"/", "/settings/")
}

func (c *cfgScan) python(f File) {
	src := stripPyComments(f.Content)
	if strings.Contains(src, "environ") || strings.Contains(src, "getenv") {
		for _, m := range pyEnvCall.FindAllStringSubmatchIndex(src, -1) {
			if m[0] > 0 && (src[m[0]-1] == '.' || src[m[0]-1] == '_') && !strings.HasPrefix(src[m[2]:m[3]], "os.") {
				continue
			}
			args := callArgsAt(src, m[1])
			if len(args) == 0 {
				continue
			}
			key, ok := pyStringLit(args[0])
			if !ok {
				continue
			}
			def, has := "", len(args) > 1
			if has {
				def = args[1]
			}
			fn := src[m[2]:m[3]]
			c.read(f, m[0], CfgEnv, key, def, has, fn)
			if strings.HasSuffix(fn, "setdefault") && has {
				c.set(f, dataLineOf(src, m[0]), CfgEnv, key, def, "code")
			}
		}
		for _, m := range pyEnvIndex.FindAllStringSubmatchIndex(src, -1) {
			key := src[m[2]:m[3]]
			if m[4] >= 0 {
				continue // a write: handled below
			}
			c.read(f, m[0], CfgEnv, key, "", false, "os.environ[]")
		}
		for _, m := range pyEnvWrite.FindAllStringSubmatchIndex(src, -1) {
			if m[2] >= 0 {
				c.set(f, dataLineOf(src, m[0]), CfgEnv, src[m[2]:m[3]], src[m[4]:m[5]], "code")
			} else {
				c.set(f, dataLineOf(src, m[0]), CfgEnv, src[m[6]:m[7]], src[m[8]:m[9]], "code")
			}
		}
	}
	if strings.Contains(src, "environ.Env") || strings.Contains(src, "import environ") {
		for _, m := range pyEnvironEnv.FindAllStringSubmatchIndex(src, -1) {
			if m[0] > 0 && (src[m[0]-1] == '.' || src[m[0]-1] == '_') {
				continue
			}
			c.pyKeyCall(f, src, m, "django-environ")
		}
	}
	if strings.Contains(src, "decouple") {
		for _, m := range pyDecouple.FindAllStringSubmatchIndex(src, -1) {
			if m[0] > 0 && (src[m[0]-1] == '.' || src[m[0]-1] == '_') {
				continue
			}
			c.pyKeyCall(f, src, m, "decouple")
		}
	}
	if strings.Contains(src, "add_argument") {
		for _, m := range pyArgparse.FindAllStringSubmatchIndex(src, -1) {
			c.pyFlag(f, src, m[0], callArgsAt(src, m[1]), "argparse")
		}
	}
	if strings.Contains(src, "click") || strings.Contains(src, "typer") || strings.Contains(src, "cloup") {
		for _, m := range pyClick.FindAllStringSubmatchIndex(src, -1) {
			via := "click"
			if m[4] >= 0 {
				via = "typer"
			}
			args := callArgsAt(src, m[1])
			c.pyFlag(f, src, m[0], args, via)
			if ev, ok := kwArg(args, "envvar"); ok {
				def, has := kwArg(args, "default")
				for _, e := range regexp.MustCompile(`["']([\w.-]+)["']`).FindAllStringSubmatch(ev, -1) {
					c.read(f, m[0], CfgEnv, e[1], def, has, via+" envvar")
				}
			}
		}
	}
	if strings.Contains(src, "settings") && !isSettingsModule(f.Path) {
		for _, m := range pySettingAt.FindAllStringSubmatchIndex(src, -1) {
			if m[0] > 0 && (src[m[0]-1] == '.' || src[m[0]-1] == '_') {
				continue
			}
			c.read(f, m[0], CfgSetting, src[m[2]:m[3]], "", false, "settings")
		}
		for _, m := range pyGetattrSet.FindAllStringSubmatchIndex(src, -1) {
			args := splitArgs(callArgs(src, strings.LastIndex(src[:m[1]], "(")+1))
			if len(args) < 2 {
				continue
			}
			key, ok := pyStringLit(args[1])
			if !ok {
				continue
			}
			def, has := "", len(args) > 2
			if has {
				def = strings.TrimSpace(args[2])
			}
			c.read(f, m[0], CfgSetting, key, def, has, "getattr(settings)")
		}
	}
	if isSettingsModule(f.Path) {
		lines := strings.Split(src, "\n")
		defined := map[string]int{}
		for i, ln := range lines {
			if m := pySettingSet.FindStringSubmatch(ln); m != nil {
				c.set(f, i+1, CfgSetting, m[1], m[2], "settings")
				if _, ok := defined[m[1]]; !ok {
					defined[m[1]] = i
				}
			}
		}
		// A setting the settings module itself builds on (BASE_DIR, a
		// value reused in DATABASES) is read there by name.
		for name, at := range defined {
			re := regexp.MustCompile(`\b` + name + `\b`)
			for i, ln := range lines {
				if i == at || pySettingSet.MatchString(ln) && strings.HasPrefix(ln, name) {
					continue
				}
				if loc := re.FindStringIndex(ln); loc != nil {
					pos := 0
					for k := 0; k < i; k++ {
						pos += len(lines[k]) + 1
					}
					c.read(f, pos+loc[0], CfgSetting, name, "", false, "settings module")
					break
				}
			}
		}
	}
	for _, r := range scanParamReads(f.Path, f.Content) {
		c.rosRead(f, r)
	}
}

func (c *cfgScan) rosRead(f File, r paramRead) {
	in, h := enclosingHandle(f, r.line, c.syms)
	c.reads = append(c.reads, ConfigRead{Kind: CfgROSParam, Key: r.param, Repo: c.repo, Path: f.Path, Line: r.line, In: in, Handle: h,
		Default: r.def, HasDefault: r.hasDef, Via: "ros param", Text: dataClip(r.expression)})
}

func (c *cfgScan) pyKeyCall(f File, src string, m []int, via string) {
	args := callArgsAt(src, m[1])
	if len(args) == 0 {
		return
	}
	key, ok := pyStringLit(args[0])
	if !ok {
		return
	}
	def, has := kwArg(args, "default")
	if !has && len(args) > 1 && !strings.Contains(args[1], "=") {
		def, has = args[1], true
	}
	c.read(f, m[0], CfgEnv, key, def, has, via)
}

func (c *cfgScan) pyFlag(f File, src string, pos int, args []string, via string) {
	name := ""
	for _, a := range args {
		lit, ok := pyStringLit(a)
		if !ok {
			continue
		}
		if strings.HasPrefix(lit, "--") {
			name = lit
			break
		}
		if name == "" && !strings.HasPrefix(lit, "-") {
			name = lit // a positional argument
			break
		}
		if name == "" {
			name = lit
		}
	}
	if d, ok := kwArg(args, "dest"); ok && name == "" {
		name, _ = pyStringLit(d)
	}
	if name == "" {
		return
	}
	def, has := kwArg(args, "default")
	if !has && via == "typer" && len(args) > 0 && !strings.Contains(args[0], "=") {
		if _, lit := pyStringLit(args[0]); !lit || !strings.HasPrefix(strings.Trim(args[0], `"'`), "-") {
			def, has = args[0], args[0] != "..."
		}
	}
	c.read(f, pos, CfgFlag, name, def, has, via)
}

// pydanticSettings: a BaseSettings field is an environment variable named
// after it (with the class's env_prefix, or its alias).
func (c *cfgScan) pydanticSettings(files []File) {
	r := &pyRepo{classes: map[string][]*pyCls{}, cat: map[*pyCls]string{}, sqlBase: map[string]bool{}}
	var all []*pyCls
	for i := range files {
		f := &files[i]
		if path.Ext(f.Path) != ".py" || !strings.Contains(f.Content, "class ") {
			continue
		}
		for _, cl := range parsePyClasses(f) {
			r.classes[cl.name] = append(r.classes[cl.name], cl)
			all = append(all, cl)
		}
	}
	for _, cl := range all {
		if r.category(cl, 0) != catSettings {
			continue
		}
		prefix := unquoteLit(cl.meta["env_prefix"])
		if v := cl.meta["model_config"]; v != "" {
			if m := regexp.MustCompile(`env_prefix\s*=\s*["']([^"']*)["']`).FindStringSubmatch(v); m != nil {
				prefix = m[1]
			}
		}
		for _, s := range cl.body {
			var name, val string
			if a := pyAssignRe.FindStringSubmatch(s.text); a != nil && a[2] != "" {
				name, val = a[1], a[3]
			} else if a := pyAnnRe.FindStringSubmatch(s.text); a != nil && !strings.Contains(a[2], "=") {
				name = a[1]
			} else {
				continue
			}
			if strings.HasPrefix(name, "_") || name == "model_config" {
				continue
			}
			key := strings.ToUpper(prefix + name)
			def, has := val, val != ""
			if callee, pos, kw, ok := pyCall(val); ok && lastDot(callee) == "Field" {
				def, has = "", false
				if v, ok := kw["default"]; ok {
					def, has = v, true
				} else if len(pos) > 0 && pos[0] != "..." {
					def, has = pos[0], true
				}
				for _, k := range []string{"env", "validation_alias", "alias"} {
					if v, ok := kw[k]; ok {
						if lit, ok := pyStringLit(v); ok {
							key = lit
							break
						}
					}
				}
			}
			off := 0
			for k := 0; k < s.line-1; k++ {
				off += strings.IndexByte(cl.file.Content[off:], '\n') + 1
			}
			c.read(*cl.file, off, CfgEnv, key, def, has, "pydantic-settings "+cl.name+"."+name)
		}
	}
}

func (c *cfgScan) js(f File) {
	if minified(f) {
		return
	}
	src := f.Content
	if strings.Contains(src, "env") {
		for _, m := range jsEnvDot.FindAllStringSubmatchIndex(src, -1) {
			key := ""
			for _, g := range [][2]int{{4, 5}, {6, 7}, {8, 9}} {
				if m[g[0]] >= 0 {
					key = src[m[g[0]]:m[g[1]]]
				}
			}
			rest := src[m[1]:]
			if jsEnvAssign.MatchString(rest) {
				c.set(f, dataLineOf(src, m[0]), CfgEnv, key, strings.TrimSuffix(strings.TrimSpace(strings.SplitN(rest[strings.Index(rest, "=")+1:]+"\n", "\n", 2)[0]), ";"), "code")
				continue
			}
			def, has := "", false
			if d := jsEnvDefault.FindStringSubmatch(rest); d != nil {
				def, has = d[2], true
			}
			via := src[m[2]:m[3]]
			c.read(f, m[0], CfgEnv, key, def, has, strings.TrimSuffix(via, "("))
		}
		for _, m := range jsEnvDestruct.FindAllStringSubmatchIndex(src, -1) {
			for _, part := range strings.Split(src[m[2]:m[3]], ",") {
				name, def, has := strings.TrimSpace(part), "", false
				if k, v, ok := strings.Cut(name, "="); ok {
					name, def, has = strings.TrimSpace(k), strings.TrimSpace(v), true
				}
				if k, _, ok := strings.Cut(name, ":"); ok {
					name = strings.TrimSpace(k)
				}
				if pyIdent(name) {
					c.read(f, m[0], CfgEnv, name, def, has, "process.env")
				}
			}
		}
	}
	if strings.Contains(src, "option(") {
		for _, m := range jsCommander.FindAllStringSubmatchIndex(src, -1) {
			spec := src[m[2]:m[3]]
			lm := regexp.MustCompile(`--([\w-]+)`).FindStringSubmatch(spec)
			if lm == nil {
				continue
			}
			args := callArgsAt(src, strings.Index(src[m[0]:], "(")+m[0]+1)
			def, has := "", len(args) > 2
			if has {
				def = args[2]
			}
			c.read(f, m[0], CfgFlag, "--"+lm[1], def, has, "commander")
		}
	}
}

func (c *cfgScan) golang(f File) {
	src := f.Content
	for _, m := range goEnvCall.FindAllStringSubmatchIndex(src, -1) {
		c.read(f, m[0], CfgEnv, src[m[4]:m[5]], "", false, "os."+src[m[2]:m[3]])
	}
	consts := c.goConsts[path.Dir(f.Path)]
	for _, m := range goEnvIdent.FindAllStringSubmatchIndex(src, -1) {
		name := src[m[4]:m[5]]
		if v, ok := consts[name]; ok {
			c.read(f, m[0], CfgEnv, v, "", false, "os."+src[m[2]:m[3]]+" via const "+name)
		}
	}
	for _, m := range goTagEnv.FindAllStringSubmatchIndex(src, -1) {
		tag := src[m[0]:m[1]]
		def, has := "", false
		if d := goTagDef.FindStringSubmatch(tag); d != nil {
			def, has = d[1], true
		}
		c.read(f, m[0], CfgEnv, src[m[4]:m[5]], def, has, src[m[2]:m[3]]+" tag")
	}
	if strings.Contains(src, "viper.") {
		defs := map[string]string{}
		for _, m := range goViperDef.FindAllStringSubmatch(src, -1) {
			defs[m[1]] = strings.TrimSpace(m[2])
		}
		for _, m := range goViperGet.FindAllStringSubmatchIndex(src, -1) {
			key := src[m[4]:m[5]]
			d, has := defs[key]
			c.read(f, m[0], CfgSetting, key, d, has, "viper."+src[m[2]:m[3]])
		}
		for _, m := range goViperBind.FindAllStringSubmatchIndex(src, -1) {
			env := strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(src[m[2]:m[3]]))
			if m[4] >= 0 {
				env = src[m[4]:m[5]]
			}
			c.read(f, m[0], CfgEnv, env, "", false, "viper.BindEnv("+src[m[2]:m[3]]+")")
		}
	}
	if strings.Contains(src, "flag") || strings.Contains(src, "Flags()") {
		for _, m := range goFlagCall.FindAllStringSubmatchIndex(src, -1) {
			args := callArgsAt(src, m[1])
			i := 0
			if m[4] >= 0 || src[m[2]:m[3]] == "Var" || src[m[2]:m[3]] == "TextVar" {
				i++ // the pointer / Value
			}
			if i >= len(args) {
				continue
			}
			name, err := unquoteGo(args[i])
			if err {
				continue
			}
			i++
			if m[6] >= 0 {
				i++ // the shorthand
			}
			def, has := "", false
			if i < len(args) && src[m[2]:m[3]] != "Func" && src[m[2]:m[3]] != "BoolFunc" && src[m[2]:m[3]] != "Var" {
				def, has = args[i], true
			}
			via := "flag." + src[m[2]:m[3]]
			if m[4] >= 0 {
				via += "Var"
			}
			if m[6] >= 0 {
				via += "P"
			}
			c.read(f, m[0], CfgFlag, name, def, has, via)
		}
	}
	for _, r := range scanParamReads(f.Path, f.Content) {
		c.rosRead(f, r)
	}
}

func unquoteGo(a string) (string, bool) {
	a = strings.TrimSpace(a)
	if len(a) >= 2 && (a[0] == '"' || a[0] == '`') && a[len(a)-1] == a[0] {
		return a[1 : len(a)-1], false
	}
	return "", true
}

func (c *cfgScan) cpp(f File) {
	src := f.Content
	if strings.Contains(src, "env") {
		for _, m := range cEnvCall.FindAllStringSubmatchIndex(src, -1) {
			for g := 2; g < 8; g += 2 {
				if m[g] >= 0 {
					c.read(f, m[0], CfgEnv, src[m[g]:m[g+1]], "", false, "getenv")
				}
			}
		}
		for _, m := range cEnvSet.FindAllStringSubmatchIndex(src, -1) {
			if m[2] >= 0 {
				c.set(f, dataLineOf(src, m[0]), CfgEnv, src[m[2]:m[3]], src[m[4]:m[5]], "code")
			} else {
				c.set(f, dataLineOf(src, m[0]), CfgEnv, src[m[6]:m[7]], src[m[8]:m[9]], "code")
			}
		}
	}
	for _, r := range scanParamReads(f.Path, f.Content) {
		c.rosRead(f, r)
	}
}

func (c *cfgScan) rust(f File) {
	src := f.Content
	for _, m := range rustEnv.FindAllStringSubmatchIndex(src, -1) {
		key := ""
		if m[2] >= 0 {
			key = src[m[2]:m[3]]
		} else {
			key = src[m[4]:m[5]]
		}
		def, has := "", false
		if d := rustUnwrap.FindStringSubmatch(src[m[1]:]); d != nil {
			def, has = d[1], true
		}
		c.read(f, m[0], CfgEnv, key, def, has, "std::env")
	}
	for _, m := range rustClapEnv.FindAllStringSubmatchIndex(src, -1) {
		attr := src[m[2]:m[3]]
		def := regexp.MustCompile(`default_value(?:_t)?\s*=\s*("?[^,)]+"?)`).FindStringSubmatch(attr)
		if e := regexp.MustCompile(`\benv\s*=\s*"([\w.-]+)"`).FindStringSubmatch(attr); e != nil {
			if def != nil {
				c.read(f, m[0], CfgEnv, e[1], def[1], true, "clap env")
			} else {
				c.read(f, m[0], CfgEnv, e[1], "", false, "clap env")
			}
		}
		if strings.Contains(attr, "long") {
			rest := src[m[1]:]
			if fm := regexp.MustCompile(`^\s*(?:#\[[^\]]*\]\s*)*(?:pub\s+)?(\w+)\s*:`).FindStringSubmatch(rest); fm != nil {
				name := strings.ReplaceAll(fm[1], "_", "-")
				if l := regexp.MustCompile(`long\s*=\s*"([\w-]+)"`).FindStringSubmatch(attr); l != nil {
					name = l[1]
				}
				if def != nil {
					c.read(f, m[0], CfgFlag, "--"+name, def[1], true, "clap")
				} else {
					c.read(f, m[0], CfgFlag, "--"+name, "", false, "clap")
				}
			}
		}
	}
}

func (c *cfgScan) java(f File) {
	src := f.Content
	for _, m := range javaEnv.FindAllStringSubmatchIndex(src, -1) {
		c.read(f, m[0], CfgEnv, src[m[2]:m[3]], "", false, "System.getenv")
	}
	for _, m := range javaProp.FindAllStringSubmatchIndex(src, -1) {
		def, has := "", m[4] >= 0
		if has {
			def = src[m[4]:m[5]]
		}
		c.read(f, m[0], CfgSetting, src[m[2]:m[3]], def, has, "System.getProperty")
	}
	for _, m := range javaValue.FindAllStringSubmatchIndex(src, -1) {
		def, has := "", m[4] >= 0
		if has {
			def = src[m[4]:m[5]]
		}
		c.read(f, m[0], CfgSetting, src[m[2]:m[3]], def, has, "@Value")
	}
}

func (c *cfgScan) shell(f File) {
	for _, m := range shExpand.FindAllStringSubmatchIndex(f.Content, -1) {
		op := f.Content[m[4]:m[5]]
		def, has := "", strings.Contains(op, "-") || strings.Contains(op, "=")
		if has {
			def = f.Content[m[6]:m[7]]
		}
		c.read(f, m[0], CfgEnv, f.Content[m[2]:m[3]], def, has, "${…"+op+"}")
	}
	for i, ln := range strings.Split(f.Content, "\n") {
		if m := shExport.FindStringSubmatch(ln); m != nil {
			c.set(f, i+1, CfgEnv, m[1], m[2], "shell")
		}
	}
}

func (c *cfgScan) dotenv(f File) {
	re := regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][\w.]*)\s*=\s*(.*)$`)
	for i, ln := range strings.Split(f.Content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		if m := re.FindStringSubmatch(ln); m != nil {
			v := m[2]
			if k := strings.Index(v, " #"); k >= 0 && !strings.HasPrefix(strings.TrimSpace(v), `"`) {
				v = v[:k]
			}
			c.set(f, i+1, CfgEnv, m[1], v, "dotenv")
		}
	}
}

func (c *cfgScan) properties(f File) {
	re := regexp.MustCompile(`^\s*([\w.\-\[\]]+)\s*[=:]\s*(.*)$`)
	for i, ln := range strings.Split(f.Content, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "!") {
			continue
		}
		if m := re.FindStringSubmatch(ln); m != nil {
			c.set(f, i+1, CfgSetting, m[1], m[2], "properties")
		}
	}
	c.expansions(f, "properties")
}

func (c *cfgScan) dockerfile(f File) {
	envRe := regexp.MustCompile(`(?i)^\s*ENV\s+(.+)$`)
	argRe := regexp.MustCompile(`(?i)^\s*ARG\s+([A-Za-z_]\w*)(?:=(.*))?$`)
	kv := regexp.MustCompile(`([A-Za-z_]\w*)=("(?:[^"\\]|\\.)*"|'[^']*'|\S*)`)
	for i, ln := range strings.Split(f.Content, "\n") {
		if m := envRe.FindStringSubmatch(ln); m != nil {
			body := m[1]
			if pairs := kv.FindAllStringSubmatch(body, -1); len(pairs) > 0 && strings.Contains(strings.Fields(body)[0], "=") {
				for _, p := range pairs {
					c.set(f, i+1, CfgEnv, p[1], p[2], "dockerfile ENV")
				}
			} else if fs := strings.Fields(body); len(fs) >= 1 {
				c.set(f, i+1, CfgEnv, fs[0], strings.TrimSpace(strings.TrimPrefix(body, fs[0])), "dockerfile ENV")
			}
		} else if m := argRe.FindStringSubmatch(ln); m != nil {
			c.set(f, i+1, CfgEnv, m[1], m[2], "dockerfile ARG")
		}
	}
}

func (c *cfgScan) launch(f File) {
	for _, m := range launchEnvRead.FindAllStringSubmatchIndex(f.Content, -1) {
		def, has := "", m[6] >= 0
		if has {
			def = f.Content[m[6]:m[7]]
		}
		c.read(f, m[0], CfgEnv, f.Content[m[4]:m[5]], def, has, "$("+f.Content[m[2]:m[3]]+")")
	}
	for _, m := range launchEnvSet.FindAllStringSubmatchIndex(f.Content, -1) {
		c.set(f, dataLineOf(f.Content, m[0]), CfgEnv, f.Content[m[2]:m[3]], f.Content[m[4]:m[5]], "launch")
	}
}

// expansions: ${VAR:-default} interpolation in compose/CI/properties text.
func (c *cfgScan) expansions(f File, via string) {
	for _, m := range shExpand.FindAllStringSubmatchIndex(f.Content, -1) {
		op := f.Content[m[4]:m[5]]
		def, has := "", strings.Contains(op, "-") || strings.Contains(op, "=")
		if has {
			def = f.Content[m[6]:m[7]]
		}
		c.read(f, m[0], CfgEnv, f.Content[m[2]:m[3]], def, has, via+" interpolation")
	}
}

// yamlSource classifies a YAML file by what deploys it.
func yamlSource(f File) string {
	p := strings.ToLower(f.Path)
	base := path.Base(p)
	switch {
	case strings.HasPrefix(p, ".github/workflows/") || strings.HasPrefix(p, ".circleci/") || strings.HasPrefix(base, ".gitlab-ci") ||
		strings.Contains(p, "azure-pipelines") || base == "bitbucket-pipelines.yml" || base == "cloudbuild.yaml":
		return "ci"
	case strings.Contains(base, "compose"):
		return "compose"
	case strings.Contains(f.Content, "apiVersion:") && strings.Contains(f.Content, "kind:") || strings.Contains(p, "/templates/") ||
		strings.Contains(p, "helm") || strings.Contains(p, "k8s") || strings.Contains(p, "kubernetes") || strings.Contains(p, "charts/"):
		return "k8s"
	}
	return "yaml"
}

var (
	yamlEnvKey  = regexp.MustCompile(`^(\s*)(?:-\s+)?(environment|env|variables|envs|extraEnv|extraEnvVars|env_vars|envVars)\s*:\s*(.*)$`)
	yamlMapItem = regexp.MustCompile(`^([A-Za-z_][\w.-]*)\s*:\s*(.*)$`)
	yamlKeyName = regexp.MustCompile(`^[A-Za-z_][\w.-]*$`)
)

func (c *cfgScan) yaml(f File) {
	src := yamlSource(f)
	lines := strings.Split(f.Content, "\n")
	for i := 0; i < len(lines); i++ {
		m := yamlEnvKey.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		inline := strings.TrimSpace(m[3])
		if c := strings.Index(inline, " #"); c >= 0 {
			inline = strings.TrimSpace(inline[:c])
		}
		if strings.HasPrefix(inline, "[") {
			for _, it := range strings.Split(strings.Trim(inline, "[]"), ",") {
				c.yamlItem(f, i+1, strings.TrimSpace(it), src)
			}
			continue
		}
		if inline != "" && inline != "|" {
			continue // a scalar (env: production), not a block
		}
		keyInd := indentOf(lines[i])
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "- ") {
			keyInd += 2
		}
		child := -1
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			ind := indentOf(lines[j])
			if ind <= keyInd && !(ind == keyInd && strings.HasPrefix(t, "- ")) {
				break
			}
			if child < 0 {
				child = ind
			}
			if ind != child {
				continue
			}
			if strings.HasPrefix(t, "- ") {
				item := strings.TrimSpace(t[2:])
				if nm := regexp.MustCompile(`^name\s*:\s*(.+)$`).FindStringSubmatch(item); nm != nil {
					// k8s: - name: X / value: Y | valueFrom: …
					val := ""
					for k := j + 1; k < len(lines); k++ {
						tk := strings.TrimSpace(lines[k])
						if tk == "" {
							continue
						}
						if indentOf(lines[k]) <= child {
							break
						}
						if v := regexp.MustCompile(`^value\s*:\s*(.*)$`).FindStringSubmatch(tk); v != nil {
							val = v[1]
						} else if strings.HasPrefix(tk, "valueFrom") && val == "" {
							val = "(valueFrom)"
						} else if (strings.HasPrefix(tk, "secretKeyRef") || strings.HasPrefix(tk, "configMapKeyRef") || strings.HasPrefix(tk, "fieldRef")) && val == "(valueFrom)" {
							val = "(from " + strings.TrimSuffix(tk, ":") + ")"
						}
					}
					c.set(f, j+1, CfgEnv, unquoteVal(nm[1]), val, src)
					continue
				}
				c.yamlItem(f, j+1, item, src)
				continue
			}
			if mm := yamlMapItem.FindStringSubmatch(t); mm != nil {
				val := mm[2]
				if strings.TrimSpace(val) == "" {
					// gitlab: KEY: {value: x} written as a nested block
					if j+1 < len(lines) {
						if v := regexp.MustCompile(`^\s*value\s*:\s*(.*)$`).FindStringSubmatch(lines[j+1]); v != nil {
							val = v[1]
						}
					}
				}
				c.set(f, j+1, CfgEnv, mm[1], val, src)
			}
		}
	}
	c.expansions(f, src)
	if src == "ci" {
		for _, m := range ciExprEnv.FindAllStringSubmatchIndex(f.Content, -1) {
			c.read(f, m[0], CfgEnv, f.Content[m[2]:m[3]], "", false, "ci expression")
		}
	}
}

func (c *cfgScan) yamlItem(f File, line int, item, src string) {
	item = unquoteVal(item)
	if item == "" {
		return
	}
	if k, v, ok := strings.Cut(item, "="); ok {
		if yamlKeyName.MatchString(k) {
			c.set(f, line, CfgEnv, k, v, src)
		}
		return
	}
	if k, v, ok := strings.Cut(item, ":"); ok && yamlKeyName.MatchString(strings.TrimSpace(k)) {
		c.set(f, line, CfgEnv, strings.TrimSpace(k), v, src)
		return
	}
	if yamlKeyName.MatchString(item) {
		c.set(f, line, CfgEnv, item, "(from the host environment)", src)
	}
}

// rosParamSets: every value a launch file gives a parameter (inline <param>,
// <rosparam> YAML it loads), fleet-wide.
func rosParamSets(all map[string][]File) []ConfigSet {
	var launch, yml []repoFile
	repos := make([]string, 0, len(all))
	for r := range all {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for _, r := range repos {
		for _, f := range all[r] {
			switch {
			case roslaunch.IsLaunchFile(f.Path):
				launch = append(launch, repoFile{r, f.Path, f.Content})
			case roslaunch.IsParamFile(f.Path):
				yml = append(yml, repoFile{r, f.Path, f.Content})
			}
		}
	}
	if len(launch) == 0 {
		return nil
	}
	idx := buildLaunchParams(launch, yml)
	var out []ConfigSet
	for name, vals := range idx.byName {
		for _, v := range vals {
			p, line := v.Via, 0
			if k := strings.Index(p, " via "); k >= 0 {
				p = p[:k]
			}
			if k := strings.LastIndex(p, ":"); k >= 0 {
				fmt.Sscanf(p[k+1:], "%d", &line)
				p = p[:k]
			}
			out = append(out, ConfigSet{Kind: CfgROSParam, Key: name, Value: dataClip(v.Value), Repo: v.repo, Path: p, Line: line, Source: "launch"})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Path+itoa3(out[i].Line) < out[j].Path+itoa3(out[j].Line)
	})
	return out
}

func itoa3(n int) string { return fmt.Sprintf("%08d", n) }

// expansionReads: for an env key only ever set, a $KEY/${KEY} in a shell
// script, Dockerfile, compose/CI/k8s YAML or launch file reads it; for a flag,
// a --flag in those files is where it is set.
func expansionReads(keys []ConfigKey, all map[string][]File) {
	var want []int
	var flags []int
	for i, k := range keys {
		if k.Kind == CfgEnv && len(k.Reads) == 0 && len(k.Sets) > 0 {
			want = append(want, i)
		}
		// A named option (--x, or a Go flag, which is passed as -x/--x); a
		// positional argument is never passed by name.
		if k.Kind == CfgFlag && len(k.Reads) > 0 && (strings.HasPrefix(k.Key, "-") || strings.HasPrefix(k.Reads[0].Via, "flag.")) {
			flags = append(flags, i)
		}
	}
	if len(want) == 0 && len(flags) == 0 {
		return
	}
	repos := make([]string, 0, len(all))
	for r := range all {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	isScript := func(p string) bool {
		ext := strings.ToLower(path.Ext(p))
		base := strings.ToLower(path.Base(p))
		return ext == ".sh" || ext == ".bash" || ext == ".yml" || ext == ".yaml" || ext == ".launch" || ext == ".service" ||
			base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || ext == ".dockerfile" || base == "makefile" || ext == ".env"
	}
	for _, i := range want {
		k := &keys[i]
		re := regexp.MustCompile(`\$\{?` + regexp.QuoteMeta(k.Key) + `\b`)
		for _, r := range repos {
			for _, f := range all[r] {
				if !isScript(f.Path) || !strings.Contains(f.Content, k.Key) {
					continue
				}
				if loc := re.FindStringIndex(f.Content); loc != nil {
					k.Reads = append(k.Reads, ConfigRead{Kind: CfgEnv, Key: k.Key, Repo: r, Path: f.Path, Line: dataLineOf(f.Content, loc[0]),
						Via: "shell expansion", Text: dataClip(dataLineAt(f.Content, loc[0]))})
				}
			}
		}
	}
	for _, i := range flags {
		k := &keys[i]
		name := strings.TrimLeft(k.Key, "-")
		if len(name) < 2 {
			continue
		}
		re := regexp.MustCompile(`(?:^|[\s"'\[,])--?` + regexp.QuoteMeta(name) + `(?:[= ]+("[^"]*"|'[^']*'|[^\s"',\]]+))?`)
		defined := map[string]bool{}
		for _, rd := range k.Reads {
			defined[rd.Repo+"\x00"+rd.Path] = true
		}
		for _, r := range repos {
			for _, f := range all[r] {
				if defined[r+"\x00"+f.Path] || !isScript(f.Path) && path.Ext(f.Path) != ".py" || !strings.Contains(f.Content, name) {
					continue
				}
				if path.Ext(f.Path) == ".py" && !strings.Contains(f.Content, "subprocess") {
					continue
				}
				for _, m := range re.FindAllStringSubmatchIndex(f.Content, -1) {
					v := ""
					if m[2] >= 0 {
						v = f.Content[m[2]:m[3]]
					}
					src := "cli"
					switch {
					case strings.HasSuffix(f.Path, ".launch"):
						src = "launch"
					case strings.Contains(strings.ToLower(path.Base(f.Path)), "compose"):
						src = "compose"
					case strings.Contains(strings.ToLower(path.Base(f.Path)), "dockerfile"):
						src = "dockerfile"
					}
					k.Sets = append(k.Sets, ConfigSet{Kind: CfgFlag, Key: k.Key, Value: dataClip(unquoteVal(v)), Repo: r, Path: f.Path, Line: dataLineOf(f.Content, m[0]), Source: src})
				}
			}
		}
	}
}

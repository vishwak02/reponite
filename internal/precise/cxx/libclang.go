//go:build cgo

// libclang.go parses one C/C++ translation unit with libclang and reports
// what the compiler bound: every function definition of the repo it saw, and
// for each call inside one, the declaration clang bound it to (by USR), or
// that it bound outside the repo, or that it could not bind it. libclang is
// loaded at run time (dlopen), so the binary needs no clang headers or
// development package to build, and runs without libclang at all — the
// driver then reports itself unavailable and indexing stays inferred.
package cxx

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef void *CXIndex;
typedef void *CXTranslationUnit;
typedef void *CXClientData;
typedef void *CXFile;
typedef void *CXDiagnostic;
typedef struct { const void *data; unsigned private_flags; } CXString;
typedef struct { int kind; int xdata; const void *data[3]; } CXCursor;
typedef struct { const void *ptr_data[2]; unsigned int_data; } CXSourceLocation;
typedef struct { int kind; void *data[2]; } CXType;
typedef struct { const void *ptr_data[2]; unsigned begin_int_data; unsigned end_int_data; } CXSourceRange;
typedef struct { unsigned int_data[4]; void *ptr_data; } CXToken;
typedef int (*CXCursorVisitor)(CXCursor, CXCursor, CXClientData);
typedef void (*CXInclusionVisitor)(CXFile, CXSourceLocation *, unsigned, CXClientData);

static struct {
	void *lib;
	CXIndex (*createIndex)(int, int);
	int (*parseTU2)(CXIndex, const char *, const char *const *, int, void *, unsigned, unsigned, CXTranslationUnit *);
	void (*disposeTU)(CXTranslationUnit);
	CXCursor (*tuCursor)(CXTranslationUnit);
	unsigned (*visitChildren)(CXCursor, CXCursorVisitor, CXClientData);
	CXCursor (*referenced)(CXCursor);
	CXCursor (*definition)(CXCursor);
	CXCursor (*canonical)(CXCursor);
	CXCursor (*semanticParent)(CXCursor);
	unsigned (*isDefinition)(CXCursor);
	int (*isNull)(CXCursor);
	unsigned (*isInvalidDecl)(CXCursor);
	CXString (*spelling)(CXCursor);
	CXString (*usr)(CXCursor);
	CXSourceLocation (*location)(CXCursor);
	void (*expansionLocation)(CXSourceLocation, CXFile *, unsigned *, unsigned *, unsigned *);
	CXString (*fileName)(CXFile);
	const char *(*cstring)(CXString);
	void (*disposeString)(CXString);
	int (*numArguments)(CXCursor);
	CXCursor (*argument)(CXCursor, unsigned);
	CXType (*cursorType)(CXCursor);
	CXType (*pointee)(CXType);
	CXCursor (*typeDecl)(CXType);
	void (*inclusions)(CXTranslationUnit, CXInclusionVisitor, CXClientData);
	unsigned (*numDiagnostics)(CXTranslationUnit);
	CXDiagnostic (*diagnostic)(CXTranslationUnit, unsigned);
	int (*diagnosticSeverity)(CXDiagnostic);
	void (*disposeDiagnostic)(CXDiagnostic);
	CXString (*version)(void);
	CXString (*formatDiagnostic)(CXDiagnostic, unsigned);
	CXSourceRange (*extent)(CXCursor);
	void (*tokenize)(CXTranslationUnit, CXSourceRange, CXToken **, unsigned *);
	CXString (*tokenSpelling)(CXTranslationUnit, CXToken);
	void (*disposeTokens)(CXTranslationUnit, CXToken *, unsigned);
	CXCursor (*specializedTemplate)(CXCursor);
	void (*spellingLocation)(CXSourceLocation, CXFile *, unsigned *, unsigned *, unsigned *);
	unsigned (*isVirtual)(CXCursor);
	CXString (*diagSpelling)(CXDiagnostic);
	CXSourceLocation (*diagLocation)(CXDiagnostic);
	CXIndex index;
} lc;

// rp_load opens the libclang at path and resolves every function used: 0 on
// success, 1 when it does not open, 2 when it lacks a function.
static int rp_load(const char *path) {
	void *lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (!lib) return 1;
	lc.lib = lib;
#define SYM(field, name) if (!(*(void **)&lc.field = dlsym(lc.lib, name))) { dlclose(lib); lc.lib = NULL; return 2; }
	SYM(createIndex, "clang_createIndex")
	SYM(parseTU2, "clang_parseTranslationUnit2")
	SYM(disposeTU, "clang_disposeTranslationUnit")
	SYM(tuCursor, "clang_getTranslationUnitCursor")
	SYM(visitChildren, "clang_visitChildren")
	SYM(referenced, "clang_getCursorReferenced")
	SYM(definition, "clang_getCursorDefinition")
	SYM(canonical, "clang_getCanonicalCursor")
	SYM(semanticParent, "clang_getCursorSemanticParent")
	SYM(isDefinition, "clang_isCursorDefinition")
	SYM(isNull, "clang_Cursor_isNull")
	SYM(isInvalidDecl, "clang_isInvalidDeclaration")
	SYM(spelling, "clang_getCursorSpelling")
	SYM(usr, "clang_getCursorUSR")
	SYM(location, "clang_getCursorLocation")
	SYM(expansionLocation, "clang_getExpansionLocation")
	SYM(fileName, "clang_getFileName")
	SYM(cstring, "clang_getCString")
	SYM(disposeString, "clang_disposeString")
	SYM(numArguments, "clang_Cursor_getNumArguments")
	SYM(argument, "clang_Cursor_getArgument")
	SYM(cursorType, "clang_getCursorType")
	SYM(pointee, "clang_getPointeeType")
	SYM(typeDecl, "clang_getTypeDeclaration")
	SYM(inclusions, "clang_getInclusions")
	SYM(numDiagnostics, "clang_getNumDiagnostics")
	SYM(diagnostic, "clang_getDiagnostic")
	SYM(diagnosticSeverity, "clang_getDiagnosticSeverity")
	SYM(disposeDiagnostic, "clang_disposeDiagnostic")
	SYM(version, "clang_getClangVersion")
	SYM(formatDiagnostic, "clang_formatDiagnostic")
	SYM(extent, "clang_getCursorExtent")
	SYM(tokenize, "clang_tokenize")
	SYM(tokenSpelling, "clang_getTokenSpelling")
	SYM(disposeTokens, "clang_disposeTokens")
	SYM(specializedTemplate, "clang_getSpecializedCursorTemplate")
	SYM(spellingLocation, "clang_getSpellingLocation")
	SYM(isVirtual, "clang_CXXMethod_isVirtual")
	SYM(diagSpelling, "clang_getDiagnosticSpelling")
	SYM(diagLocation, "clang_getDiagnosticLocation")
#undef SYM
	lc.index = lc.createIndex(0, 0);
	return 0;
}

static char *rp_version(void) {
	CXString s = lc.version();
	char *out = strdup(lc.cstring(s) ? lc.cstring(s) : "");
	lc.disposeString(s);
	return out;
}

// --- output buffer ---
typedef struct { char *p; size_t n, cap; } buf;
static void put(buf *b, const char *s) {
	size_t l = strlen(s);
	if (b->n + l + 1 > b->cap) {
		b->cap = (b->n + l + 1) * 2;
		b->p = realloc(b->p, b->cap);
	}
	memcpy(b->p + b->n, s, l);
	b->n += l;
	b->p[b->n] = 0;
}

// cursor kinds (clang-c/Index.h)
enum { K_STRUCT = 2, K_UNION = 3, K_CLASS = 4, K_FUNCTION = 8, K_METHOD = 21, K_CTOR = 24, K_DTOR = 25,
	K_CONVERSION = 26, K_FUNCTION_TEMPLATE = 30, K_OVERLOADREF = 49, K_DECLREF = 101, K_MEMBERREF = 102,
	K_CALL = 103, K_LAMBDA = 144 };

static int function_like(int k) {
	return k == K_FUNCTION || k == K_METHOD || k == K_CTOR || k == K_DTOR || k == K_CONVERSION || k == K_FUNCTION_TEMPLATE;
}

typedef struct {
	CXTranslationUnit tu;
	const char *root; size_t rootlen;
	buf out;
	// the innermost function definition being walked
	char owner_path[PATH_MAX]; unsigned owner_line; int has_owner; int owner_kind; CXCursor owner;
	// last file resolved (most cursors in a row share one)
	CXFile last_file; char last_path[PATH_MAX]; int last_in;
} walk;

// where_ resolves a location to its real path; 1 when inside the repo root.
static int where_(walk *w, CXSourceLocation loc, char *path, unsigned *line) {
	CXFile f = NULL; unsigned l = 0, c = 0, o = 0;
	lc.expansionLocation(loc, &f, &l, &c, &o);
	*line = l;
	if (!f) { path[0] = 0; return 0; }
	if (f != w->last_file) {
		CXString s = lc.fileName(f);
		const char *n = lc.cstring(s);
		char real[PATH_MAX];
		if (n && realpath(n, real)) {
			snprintf(w->last_path, sizeof w->last_path, "%s", real);
		} else {
			snprintf(w->last_path, sizeof w->last_path, "%s", n ? n : "");
		}
		lc.disposeString(s);
		w->last_file = f;
		w->last_in = strncmp(w->last_path, w->root, w->rootlen) == 0 && w->last_path[w->rootlen] == '/' &&
			!strpbrk(w->last_path, "\t\n"); // a path the report's format cannot carry is not the repo's
	}
	snprintf(path, PATH_MAX, "%s", w->last_path + (w->last_in ? w->rootlen + 1 : 0));
	return w->last_in;
}

static void clean_tabs(char *s) {
	for (; *s; s++) if (*s == '\t' || *s == '\n') *s = ' ';
}

// guessed: ref is one of several methods of its name and clang could not
// form one of them, or an argument's type — overload resolution was then
// error recovery's, not the language's.
typedef struct { const char *name; int count; int invalid; } sib;
static int sibling_visit(CXCursor c, CXCursor parent, CXClientData d) {
	sib *s = d;
	int k = c.kind;
	if (k == K_METHOD || k == K_FUNCTION_TEMPLATE || k == K_FUNCTION) {
		CXString n = lc.spelling(c);
		const char *cs = lc.cstring(n);
		if (cs && strcmp(cs, s->name) == 0) {
			s->count++;
			if (lc.isInvalidDecl(c)) s->invalid = 1;
		}
		lc.disposeString(n);
	}
	return 1; // continue
}
static int guessed(CXCursor call, CXCursor ref) {
	CXCursor parent = lc.semanticParent(ref);
	if (lc.isNull(parent)) return 0;
	CXString n = lc.spelling(ref);
	sib s = {lc.cstring(n), 0, 0};
	if (s.name) lc.visitChildren(parent, sibling_visit, &s);
	lc.disposeString(n);
	if (s.count < 2) return 0;
	if (s.invalid) return 1;
	int na = lc.numArguments(call);
	for (int i = 0; i < na; i++) {
		CXCursor a = lc.argument(call, i);
		if (lc.cursorType(a).kind == 0) return 1; // CXType_Invalid
		CXCursor r = lc.referenced(a);
		if (!lc.isNull(r) && lc.isInvalidDecl(r)) return 1;
	}
	return 0;
}

static int ident_char(char ch) {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9');
}

// callee_from_tokens writes the identifier before the first "(" of a call's
// tokens (a::send(m) -> send), "" when there is none.
static void callee_from_tokens(CXTranslationUnit tu, CXCursor call, char *out, size_t n) {
	out[0] = 0;
	CXToken *toks = NULL; unsigned nt = 0;
	lc.tokenize(tu, lc.extent(call), &toks, &nt);
	for (unsigned i = 0; i < nt; i++) {
		CXString ts = lc.tokenSpelling(tu, toks[i]);
		const char *t = lc.cstring(ts);
		int stop = t && strcmp(t, "(") == 0;
		if (!stop && t && t[0] && ident_char(t[0]) && !(t[0] >= '0' && t[0] <= '9')) {
			snprintf(out, n, "%s", t);
		}
		lc.disposeString(ts);
		if (stop) break;
	}
	if (toks) lc.disposeTokens(tu, toks, nt);
}

// dispatches: a virtual method's call may run an override — not when it
// names the method with a scope (Base::f(), obj.Base::f()), calls it on an
// object held by value (s.f() for a local, parameter or field that is not a
// reference), or calls it implicitly on this in a constructor or destructor.
static int first_child(CXCursor c, CXCursor parent, CXClientData d) {
	*(CXCursor *)d = c;
	return 0; // break
}
static CXCursor child_of(CXCursor c) {
	CXCursor out = c;
	out.kind = -1;
	lc.visitChildren(c, first_child, &out);
	if (out.kind == 100) { // an UnexposedExpr around it
		CXCursor inner = out;
		inner.kind = -1;
		lc.visitChildren(out, first_child, &inner);
		if (inner.kind != -1) out = inner;
	}
	return out;
}
enum { K_FIELD = 6, K_VAR = 9, K_PARM = 10, T_RECORD = 105, T_ELABORATED = 119 };
static int dispatches(walk *w, CXCursor call) {
	CXCursor callee = child_of(call);
	if (callee.kind == -1) return 1;
	CXToken *toks = NULL; unsigned nt = 0;
	lc.tokenize(w->tu, lc.extent(callee), &toks, &nt);
	char before[8] = "";
	if (nt >= 2) {
		CXString ts = lc.tokenSpelling(w->tu, toks[nt - 2]);
		snprintf(before, sizeof before, "%s", lc.cstring(ts) ? lc.cstring(ts) : "");
		lc.disposeString(ts);
	}
	if (toks) lc.disposeTokens(w->tu, toks, nt);
	if (strcmp(before, "::") == 0) return 0;
	if (nt == 1) return !(w->owner_kind == K_CTOR || w->owner_kind == K_DTOR); // f() on this
	if (strcmp(before, ".") == 0) {
		CXCursor base = child_of(callee);
		if (base.kind == K_DECLREF || base.kind == K_MEMBERREF) {
			CXCursor r = lc.referenced(base);
			if (!lc.isNull(r) && (r.kind == K_VAR || r.kind == K_PARM || r.kind == K_FIELD)) {
				int t = lc.cursorType(r).kind;
				if (t == T_RECORD || t == T_ELABORATED) return 0; // the object itself, not a reference
			}
		}
	}
	return 1;
}

// receiver writes where the class of the object a member call is made on is
// defined (path and line, repo-relative; empty when not the repo's): the
// overrides that may run are that class's and its subclasses'.
enum { T_POINTER = 101, T_LVREF = 103, T_RVREF = 104 };
static void receiver(walk *w, CXCursor call, char *path, unsigned *line) {
	path[0] = 0; *line = 0;
	CXCursor callee = child_of(call);
	if (callee.kind == -1) return;
	CXCursor cls;
	CXCursor base = child_of(callee);
	if (base.kind == -1) {
		cls = lc.semanticParent(w->owner); // f() on this: the caller's class
	} else {
		CXType t = lc.cursorType(base);
		if (t.kind == T_POINTER || t.kind == T_LVREF || t.kind == T_RVREF) t = lc.pointee(t);
		cls = lc.typeDecl(t);
	}
	if (lc.isNull(cls)) return;
	CXCursor def = lc.definition(cls);
	if (!lc.isNull(def)) cls = def;
	if (!where_(w, lc.location(cls), path, line)) { path[0] = 0; *line = 0; }
}

// body_spelled_in_repo: the definition's body is written in the repo — not
// a helper a framework macro defines at the macro's use (gtest's
// AddToRegistry at a TEST_P line), whose calls are the framework's.
typedef struct { walk *w; int found; int in; } bodyq;
static int body_visit(CXCursor c, CXCursor parent, CXClientData d) {
	bodyq *q = d;
	if (c.kind != 202) return 1; // CXCursor_CompoundStmt
	CXFile f = NULL; unsigned l = 0, col = 0, o = 0;
	lc.spellingLocation(lc.location(c), &f, &l, &col, &o);
	q->found = 1;
	if (f) {
		CXString s = lc.fileName(f);
		const char *n = lc.cstring(s);
		char real[PATH_MAX];
		q->in = n && realpath(n, real) && strncmp(real, q->w->root, q->w->rootlen) == 0 && real[q->w->rootlen] == '/';
		lc.disposeString(s);
	}
	return 0; // break
}
static int body_spelled_in_repo(walk *w, CXCursor c) {
	bodyq q = {w, 0, 1};
	lc.visitChildren(c, body_visit, &q);
	return !q.found || q.in;
}

static int visit(CXCursor c, CXCursor parent, CXClientData d) {
	walk *w = d;
	int k = c.kind;
	char path[PATH_MAX]; unsigned line;
	int in = where_(w, lc.location(c), path, &line);
	// outside any definition, a library header's declarations hold nothing
	// of the repo's
	if (!w->has_owner && !in && path[0]) return 1;
	if (function_like(k) && lc.isDefinition(c) && in && !body_spelled_in_repo(w, c)) {
		return 1; // a framework macro's own function: neither the repo's definition nor its calls
	}
	if (function_like(k) && lc.isDefinition(c) && in) {
		CXString u = lc.usr(c);
		char usr[4096]; snprintf(usr, sizeof usr, "%s", lc.cstring(u) ? lc.cstring(u) : ""); clean_tabs(usr);
		lc.disposeString(u);
		char dpath[PATH_MAX]; unsigned dline = 0;
		CXCursor can = lc.canonical(c);
		where_(w, lc.location(can), dpath, &dline);
		char line_s[PATH_MAX * 2 + 4200];
		snprintf(line_s, sizeof line_s, "D\t%s\t%s\t%u\t%s\t%u\n", usr, path, line, dpath, dline);
		put(&w->out, line_s);
		// walk its body as the owner (a nested lambda's calls stay with it)
		char saved_path[PATH_MAX]; unsigned saved_line = w->owner_line; int saved_has = w->has_owner;
		snprintf(saved_path, sizeof saved_path, "%s", w->owner_path);
		snprintf(w->owner_path, sizeof w->owner_path, "%s", path);
		int saved_kind = w->owner_kind; CXCursor saved_owner = w->owner;
		w->owner_line = line; w->has_owner = 1; w->owner_kind = k; w->owner = c;
		lc.visitChildren(c, visit, w);
		snprintf(w->owner_path, sizeof w->owner_path, "%s", saved_path);
		w->owner_line = saved_line; w->has_owner = saved_has; w->owner_kind = saved_kind; w->owner = saved_owner;
		return 1;
	}
	if (w->has_owner && (k == K_DECLREF || k == K_MEMBERREF || k == K_OVERLOADREF)) {
		// every reference to a function by name: a call clang's error recovery
		// dropped (an argument of a type from a missing header) leaves its
		// reference but no CallExpr, and the count of the two tells
		CXCursor r = lc.referenced(c);
		if (k == K_OVERLOADREF || lc.isNull(r) || function_like(r.kind)) {
			CXString sp = lc.spelling(c);
			char name[1024]; snprintf(name, sizeof name, "%s", lc.cstring(sp) ? lc.cstring(sp) : ""); clean_tabs(name);
			lc.disposeString(sp);
			if (name[0]) {
				char line_s[PATH_MAX + 1100];
				snprintf(line_s, sizeof line_s, "R\t%s\t%u\t%s\n", w->owner_path, w->owner_line, name);
				put(&w->out, line_s);
			}
		}
	}
	if (k == K_CALL && w->has_owner) {
		CXString sp = lc.spelling(c);
		char name[1024]; snprintf(name, sizeof name, "%s", lc.cstring(sp) ? lc.cstring(sp) : ""); clean_tabs(name);
		lc.disposeString(sp);
		if (!name[0]) {
			// a call clang could not form (error recovery: an argument of a
			// type from a missing header): its callee is the name written
			// before its first "(" — not bound, whatever a sibling binds
			callee_from_tokens(w->tu, c, name, sizeof name);
			if (name[0]) {
				char line_s[PATH_MAX + 1100];
				snprintf(line_s, sizeof line_s, "C\t%s\t%u\t%s\tU\t\t\t0\n", w->owner_path, w->owner_line, name);
				put(&w->out, line_s);
			}
			return 2;
		}
		// an operator call (operator<<, operator()), not a function named operatorX
		int is_op = strncmp(name, "operator", 8) == 0 && !(name[8] == '_' || (name[8] >= 'a' && name[8] <= 'z') ||
			(name[8] >= 'A' && name[8] <= 'Z') || (name[8] >= '0' && name[8] <= '9'));
		if (name[0] && !is_op) {
			CXCursor ref = lc.referenced(c);
			char line_s[PATH_MAX * 4 + 6000];
			if (lc.isNull(ref)) {
				snprintf(line_s, sizeof line_s, "C\t%s\t%u\t%s\tU\t\t\t0\n", w->owner_path, w->owner_line, name);
				put(&w->out, line_s);
			} else if (ref.kind != K_CTOR && ref.kind != K_DTOR) {
				// an instantiation (twice<int>, Box<int>::get) is the template
				// the repo defines: its USR and definition are the pattern's
				CXCursor pattern = lc.specializedTemplate(ref);
				if (!lc.isNull(pattern)) ref = pattern;
				char dpath[PATH_MAX]; unsigned dline = 0;
				CXCursor def = lc.definition(ref);
				CXCursor at = lc.isNull(def) ? ref : def;
				int din = where_(w, lc.location(at), dpath, &dline);
				CXString u = lc.usr(ref);
				char usr[4096]; snprintf(usr, sizeof usr, "%s", lc.cstring(u) ? lc.cstring(u) : ""); clean_tabs(usr);
				lc.disposeString(u);
				char cpath[PATH_MAX]; unsigned cline = 0;
				where_(w, lc.location(lc.canonical(ref)), cpath, &cline);
				if (!din && dpath[0]) {
					snprintf(line_s, sizeof line_s, "C\t%s\t%u\t%s\tE\t\t\t0\n", w->owner_path, w->owner_line, name);
				} else if (ref.kind == K_METHOD && guessed(c, ref)) {
					snprintf(line_s, sizeof line_s, "C\t%s\t%u\t%s\tU\t\t\t0\n", w->owner_path, w->owner_line, name);
				} else {
					// bound to the repo: by USR, its canonical declaration
					// telling apart two programs' functions of one USR; V a
					// virtual method called through an object (an override
					// of the object's class may run)
					char kind = ref.kind == K_METHOD && lc.isVirtual(ref) && dispatches(w, c) ? 'V' : 'B';
					char rpath[PATH_MAX] = ""; unsigned rline = 0;
					if (kind == 'V') receiver(w, c, rpath, &rline);
					snprintf(line_s, sizeof line_s, "C\t%s\t%u\t%s\t%c\t%s\t%s\t%u\t%s\t%u\n", w->owner_path, w->owner_line, name, kind, usr, cpath, cline, rpath, rline);
				}
				put(&w->out, line_s);
			}
		}
	}
	return 2; // recurse
}

// include_visit reports every file the unit read: I a repo file (relative),
// O any other (absolute) — a cached result holds while all are unchanged.
static void include_visit(CXFile f, CXSourceLocation *stack, unsigned n, CXClientData d) {
	walk *w = d;
	CXString s = lc.fileName(f);
	const char *name = lc.cstring(s);
	char real[PATH_MAX];
	if (name && realpath(name, real) && !strpbrk(real, "\t\n")) {
		char line_s[PATH_MAX + 4];
		if (strncmp(real, w->root, w->rootlen) == 0 && real[w->rootlen] == '/') {
			snprintf(line_s, sizeof line_s, "I\t%s\n", real + w->rootlen + 1);
		} else {
			snprintf(line_s, sizeof line_s, "O\t%s\n", real);
		}
		put(&w->out, line_s);
		// a header reached through a symbolic link: the link too (pointed
		// elsewhere, the unit reads another file)
		char dir[PATH_MAX], dreal[PATH_MAX], linked[2 * PATH_MAX + 2];
		snprintf(dir, sizeof dir, "%s", name);
		char *slash = strrchr(dir, '/');
		if (slash && slash != dir) {
			*slash = 0;
			if (realpath(dir, dreal)) {
				snprintf(linked, sizeof linked, "%s/%s", dreal, slash + 1);
				if (strlen(linked) < PATH_MAX && strcmp(linked, real) != 0 && strncmp(linked, w->root, w->rootlen) == 0 && linked[w->rootlen] == '/' &&
					!strpbrk(linked, "\t\n")) {
					snprintf(line_s, sizeof line_s, "I\t%s\n", linked + w->rootlen + 1);
					put(&w->out, line_s);
				}
			}
		}
	}
	lc.disposeString(s);
}

// rp_parse parses file with args and returns the report (caller frees), or
// NULL when the unit could not be parsed at all.
static char *rp_parse(const char *root, const char *file, const char **args, int nargs, int *errors) {
	CXTranslationUnit tu = NULL;
	// Incomplete | KeepGoing: a missing header or an error is not the end
	int rc = lc.parseTU2(lc.index, file, args, nargs, NULL, 0, 0x02 | 0x200, &tu);
	if (rc != 0 || !tu) return NULL;
	walk w;
	memset(&w, 0, sizeof w);
	w.tu = tu;
	w.root = root; w.rootlen = strlen(root);
	put(&w.out, "");
	lc.visitChildren(lc.tuCursor(tu), visit, &w);
	lc.inclusions(tu, include_visit, &w);
	int e = 0;
	unsigned nd = lc.numDiagnostics(tu);
	for (unsigned i = 0; i < nd; i++) {
		CXDiagnostic d = lc.diagnostic(tu, i);
		if (lc.diagnosticSeverity(d) >= 3) {
			// an include not found: a file of that name appearing later
			// changes what the unit compiles to
			CXString sp = lc.diagSpelling(d);
			const char *msg = lc.cstring(sp);
			if (msg && strstr(msg, "file not found")) {
				// with the file whose #include it was (quoted includes are
				// looked for beside it first)
				char from[PATH_MAX] = "", real[PATH_MAX];
				CXFile f = NULL; unsigned l = 0, col = 0, o = 0;
				lc.expansionLocation(lc.diagLocation(d), &f, &l, &col, &o);
				if (f) {
					CXString fs = lc.fileName(f);
					if (lc.cstring(fs) && realpath(lc.cstring(fs), real)) snprintf(from, sizeof from, "%s", real);
					lc.disposeString(fs);
				}
				char line_s[2 * PATH_MAX + 64];
				snprintf(line_s, sizeof line_s, "M\t%.*s", PATH_MAX, msg);
				for (char *q = line_s + 2; *q; q++) if (*q == '\t' || *q == '\n') *q = ' ';
				if (!strpbrk(from, "\t\n")) {
					size_t n = strlen(line_s);
					snprintf(line_s + n, sizeof line_s - n, "\t%s", from);
				}
				size_t n = strlen(line_s);
				snprintf(line_s + n, sizeof line_s - n, "\n");
				put(&w.out, line_s);
			}
			lc.disposeString(sp);
			if (e < 3) {
				// the first errors, for a report of why a unit is partial
				CXString m = lc.formatDiagnostic(d, 0x1 | 0x2);
				char line_s[2048];
				snprintf(line_s, sizeof line_s, "X\t%.1900s\n", lc.cstring(m) ? lc.cstring(m) : "");
				for (char *q = line_s + 2; *q && *q != '\n'; q++) if (*q == '\t') *q = ' ';
				put(&w.out, line_s);
				lc.disposeString(m);
			}
			e++;
		}
		lc.disposeDiagnostic(d);
	}
	*errors = e;
	lc.disposeTU(tu);
	return w.out.p;
}
*/
import "C"

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

var loaded = -1 // 0 ok, else the reason code

// loadMu guards loaded and libclang's state: several indexes may run at once
// in one process (an MCP server refreshing several repos).
var loadMu sync.Mutex

// inWorker is set by Serve: only a worker process lets libclang install
// its crash-recovery signal handlers (they would replace the Go runtime's,
// turning any panic of an indexing process into a bare signal death); the
// pool starts workers without the variable that turns them off.
var inWorker bool

// Load opens libclang (path empty: the usual names); safe to call again.
func Load(path string) error {
	loadMu.Lock()
	defer loadMu.Unlock()
	if loaded == 0 {
		return nil
	}
	if !inWorker {
		os.Setenv("LIBCLANG_DISABLE_CRASH_RECOVERY", "1")
	}
	names := []string{path}
	if path == "" {
		names = libclangNames()
	}
	loaded = 1
	for _, n := range names {
		cp := C.CString(n)
		rc := int(C.rp_load(cp))
		C.free(unsafe.Pointer(cp))
		if rc == 0 || rc > loaded {
			loaded = rc // a library lacking functions says more than none found
		}
		if rc == 0 {
			break
		}
	}
	switch loaded {
	case 0:
		return nil
	case 1:
		return errors.New("libclang not found (install libclang, or set REPONITE_LIBCLANG to its path)")
	default:
		return errors.New("libclang is missing functions this driver needs (too old?)")
	}
}

// Version is the loaded libclang's version string.
func Version() string {
	loadMu.Lock()
	defer loadMu.Unlock()
	if loaded != 0 {
		return ""
	}
	s := C.rp_version()
	defer C.free(unsafe.Pointer(s))
	return C.GoString(s)
}

// Parse parses file (absolute) with args; root is the repo's real path.
func Parse(root, file string, args []string) (Result, error) {
	if loaded != 0 {
		return Result{}, errors.New("libclang not loaded")
	}
	croot, cfile := C.CString(root), C.CString(file)
	defer C.free(unsafe.Pointer(croot))
	defer C.free(unsafe.Pointer(cfile))
	cargs := make([]*C.char, len(args))
	for i, a := range args {
		cargs[i] = C.CString(a)
	}
	defer func() {
		for _, a := range cargs {
			C.free(unsafe.Pointer(a))
		}
	}()
	var argp **C.char
	if len(cargs) > 0 {
		argp = (**C.char)(unsafe.Pointer(&cargs[0]))
	}
	var nerr C.int
	out := C.rp_parse(croot, cfile, argp, C.int(len(args)), &nerr)
	if out == nil {
		return Result{}, errors.New("clang could not parse " + file)
	}
	text := C.GoString(out)
	C.free(unsafe.Pointer(out))
	r := Result{Errors: int(nerr)}
	for _, ln := range strings.Split(text, "\n") {
		f := strings.Split(ln, "\t")
		switch f[0] {
		case "D":
			if len(f) == 6 {
				r.Defs = append(r.Defs, Def{USR: f[1], Path: f[2], Line: atoi(f[3]), DeclPath: f[4], DeclLine: atoi(f[5])})
			}
		case "C":
			if len(f) >= 8 && f[4] != "" {
				c := Call{OwnerPath: f[1], OwnerLine: atoi(f[2]), Name: f[3], Kind: f[4][0], USR: f[5], DeclPath: f[6], DeclLine: atoi(f[7])}
				if len(f) == 10 {
					c.RecvPath, c.RecvLine = f[8], atoi(f[9])
				}
				r.Calls = append(r.Calls, c)
			}
		case "I":
			if len(f) == 2 {
				r.Includes = append(r.Includes, f[1])
			}
		case "O":
			if len(f) == 2 {
				r.Others = append(r.Others, f[1])
			}
		case "M":
			if len(f) >= 2 {
				if m := reNotFound.FindStringSubmatch(f[1]); m != nil {
					p := Probe{Spelling: m[1]}
					if len(f) == 3 {
						p.From = f[2]
					}
					r.Missing = append(r.Missing, p)
				}
			}
		case "X":
			if len(f) == 2 {
				r.Diags = append(r.Diags, f[1])
			}
		case "R":
			if len(f) == 4 {
				r.Refs = append(r.Refs, Ref{OwnerPath: f[1], OwnerLine: atoi(f[2]), Name: f[3]})
			}
		}
	}
	return r, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// Serve is a worker's loop: one JSON Request per line in, one Reply per line
// out, until in closes. A unit that crashes libclang kills only this worker;
// the pool reports that unit failed and starts another.
func Serve(in io.Reader, out io.Writer, libclang string) error {
	inWorker = true
	if err := Load(libclang); err != nil {
		return err
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		var req Request
		var rep Reply
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			rep.Err = err.Error()
		} else if r, err := Parse(req.Root, req.File, req.Args); err != nil {
			rep.Err = err.Error()
		} else {
			rep.Result = r
		}
		if err := enc.Encode(rep); err != nil {
			return err
		}
	}
	return sc.Err()
}

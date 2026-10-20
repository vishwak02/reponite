"""Call-graph ground truth for C/C++, from clang (needs the libclang Python
bindings matching the system libclang: `pip install clang==<version>`).

Every source file is parsed by clang with the repository's include
directories. A header the repository includes but does not hold (a system or
third-party library) is replaced by an empty stub, so each file parses
without its dependencies: a call clang binds to a definition is an edge, a
call it binds outside the repository is resolved there, and a call it cannot
bind (its receiver's type came from a stubbed header) is left undetermined —
listed among the names the definition calls, never guessed.

Calls to constructors and destructors are not recorded (reponite links a
construction to the class), nor is virtual dispatch: an edge goes to the
method the call names statically.

usage: python3 -I cppgt.py <dir> [--libclang PATH] [--jobs N] [--headers DIR]... > truth.json

--headers DIR adds a tree of real headers (a copy of a build image's
/opt/ros/<distro>/include, /usr/include and the workspace's generated
devel/include): every directory named include under it, and eigen3, become
include paths, so far fewer headers are stubbed and far fewer calls stay
undetermined.
"""

import json
import multiprocessing
import os
import re
import sys
import tempfile

import clang.cindex as ci

SOURCES = (".c", ".cc", ".cpp", ".cxx")
HEADERS = (".h", ".hh", ".hpp", ".hxx", ".inl", ".ipp")
SKIP_DIRS = {".git", "build", "devel", "install", "node_modules", "__pycache__", ".venv"}
DEF_KINDS = {
    ci.CursorKind.FUNCTION_DECL, ci.CursorKind.CXX_METHOD, ci.CursorKind.FUNCTION_TEMPLATE,
    ci.CursorKind.CONVERSION_FUNCTION,
}
CLASS_KINDS = {ci.CursorKind.CLASS_DECL, ci.CursorKind.STRUCT_DECL, ci.CursorKind.CLASS_TEMPLATE,
               ci.CursorKind.CLASS_TEMPLATE_PARTIAL_SPECIALIZATION}
INCLUDE = re.compile(r'^\s*#\s*include\s*([<"])([^>"]+)[>"]', re.M)


def is_test_path(rel):
    parts = rel.split("/")
    base = parts[-1].lower()
    return any(p in ("test", "tests") for p in parts[:-1]) or base.startswith("test_") or \
        re.search(r"[_-]test\.", base) is not None


def walk(root):
    for d, dirs, files in os.walk(root):
        dirs[:] = sorted(x for x in dirs if x not in SKIP_DIRS and not x.startswith("."))
        for f in sorted(files):
            yield os.path.join(d, f)


def system_dirs(copied=()):
    """The C and C++ standard library headers. A copied include tree (the
    target image's /usr/include) holding its own libstdc++ is used instead of
    the host's: the code is compiled against that one, and a much newer host
    library is not valid C++17 for this clang."""
    for r in copied:
        if os.path.isdir(os.path.join(r, "c++")):
            vers = sorted((v for v in os.listdir(os.path.join(r, "c++")) if v[:1].isdigit()),
                          key=lambda s: [int(x) if x.isdigit() else 0 for x in s.split(".")])
            if vers:
                v = vers[-1]
                out = [os.path.join(r, "c++", v), os.path.join(r, "x86_64-linux-gnu", "c++", v),
                       os.path.join(r, "c++", v, "backward")]
                gcc = "/usr/lib/gcc/x86_64-linux-gnu"  # compiler builtins (stddef.h, …)
                if os.path.isdir(gcc):
                    for g in sorted(os.listdir(gcc))[-1:]:
                        out.append(os.path.join(gcc, g, "include"))
                out += [os.path.join(r, "x86_64-linux-gnu"), r]
                return [d for d in out if os.path.isdir(d)]
    out = []
    for base in ("/usr/include/c++", "/usr/include/x86_64-linux-gnu/c++"):
        if os.path.isdir(base):
            for v in sorted(os.listdir(base), key=lambda s: [int(x) if x.isdigit() else 0 for x in s.split(".")])[-1:]:
                out.append(os.path.join(base, v))
    gcc = "/usr/lib/gcc/x86_64-linux-gnu"
    if os.path.isdir(gcc):
        for v in sorted(os.listdir(gcc))[-1:]:
            out.append(os.path.join(gcc, v, "include"))
    out += ["/usr/include/x86_64-linux-gnu", "/usr/include"]
    return [d for d in out if os.path.isdir(d)]


def header_dirs(roots):
    """Include paths a copied header tree provides: each directory named
    include (or include/eigen3) under it, shallowest first."""
    out = []
    for r in roots:
        if any(f.endswith((".h", ".hpp")) for f in os.listdir(r)) or os.path.isdir(os.path.join(r, "boost")):
            out.append(r)  # the tree is itself an include directory (a copied /usr/include)
            for sub in ("eigen3", "opencv4"):
                if os.path.isdir(os.path.join(r, sub)):
                    out.append(os.path.join(r, sub))
            continue
        for d, dirs, _ in os.walk(r):
            depth = d[len(r):].count(os.sep)
            if depth > 8:
                dirs[:] = []
                continue
            if os.path.basename(d) == "thirdparty":
                out.append(d)  # vendored libraries included by their own dir (Box2D/Box2D.h)
            if os.path.basename(d) == "include":
                out.append(d)
                if os.path.isdir(os.path.join(d, "eigen3")):
                    out.append(os.path.join(d, "eigen3"))
                dirs[:] = []  # an include tree's own subdirectories are reached through it
    return out


stubbed = []


def stub_missing(root, sources, headers, inc_dirs, sys_dirs, stub_dir):
    """Write an empty header for every include no directory provides."""
    have = set()
    for f in sources + headers:
        try:
            text = open(f, encoding="utf-8", errors="replace").read()
        except OSError:
            continue
        for _, name in INCLUDE.findall(text):
            if name in have:
                continue
            here = os.path.join(os.path.dirname(f), name)
            if os.path.exists(here) or any(os.path.exists(os.path.join(d, name)) for d in inc_dirs + sys_dirs):
                continue
            have.add(name)
            stubbed.append(name)
            dst = os.path.join(stub_dir, name)
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            open(dst, "a").close()


GTEST = re.compile(r"^\s*(?:TYPED_)?TEST(?:_F|_P)?\s*\(\s*(\w+)\s*,\s*(\w+)\s*\)")
_lines = {}


def source_line(c):
    """The source line a cursor sits on (a macro's expansion point)."""
    f = c.location.file
    if f is None:
        return ""
    if f.name not in _lines:
        try:
            _lines[f.name] = open(f.name, encoding="utf-8", errors="replace").read().split("\n")
        except OSError:
            _lines[f.name] = []
    ls = _lines[f.name]
    n = c.location.line
    return ls[n - 1] if 0 < n <= len(ls) else ""


CALLED = re.compile(r"\b([A-Za-z_]\w*)\s*(?=\(|<[^;{}()]*>\s*\()")


def source_text(c):
    """The source lines from a definition's name to the end of its body."""
    f, end = c.location.file, c.extent.end
    if f is None:
        return " ".join(t.spelling for t in c.get_tokens())
    source_line(c)  # loads the file
    ls = _lines.get(f.name, [])
    if end.file is not None and end.file.name == f.name:
        return "\n".join(ls[c.location.line - 1:end.line])
    # a body written after a macro (TEST_F(…) { … }) whose extent clang puts in
    # the macro's header: the braces that follow the name, matched
    text = "\n".join(ls[c.location.line - 1:])
    i = text.find("{")
    depth = 0
    for j in range(i, len(text)) if i >= 0 else ():
        if text[j] == "{":
            depth += 1
        elif text[j] == "}":
            depth -= 1
            if depth == 0:
                return text[:j + 1]
    return text[:2000]


def def_name(c):
    p = c.semantic_parent
    if p is not None and p.kind in CLASS_KINDS and p.spelling:
        return p.spelling + "." + c.spelling
    return c.spelling


def overload_candidates(call):
    """The declarations an unbound call's name looks up to (a dependent call
    in a template: f(t) with t of a parameter type is bound at instantiation,
    but its candidate set is fixed where it is written)."""
    import ctypes
    lib = ci.conf.lib
    if not hasattr(lib, "_cppgt_ovl"):
        lib.clang_getNumOverloadedDecls.argtypes = [ci.Cursor]
        lib.clang_getNumOverloadedDecls.restype = ctypes.c_uint
        lib.clang_getOverloadedDecl.argtypes = [ci.Cursor, ctypes.c_uint]
        lib.clang_getOverloadedDecl.restype = ci.Cursor
        lib._cppgt_ovl = True
    kids = list(call.get_children())
    if not kids:
        return None
    todo, ovl = [kids[0]], None
    while todo and ovl is None:
        k = todo.pop()
        if k.kind == ci.CursorKind.OVERLOADED_DECL_REF:
            ovl = k
            break
        if k.kind in (ci.CursorKind.UNEXPOSED_EXPR, ci.CursorKind.DECL_REF_EXPR):
            todo += list(k.get_children())
    if ovl is None:
        return None
    out = []
    for i in range(lib.clang_getNumOverloadedDecls(ovl)):
        d = lib.clang_getOverloadedDecl(ovl, i)
        d._tu = call._tu
        out.append(d)
    return out


def parse_one(job):
    path, args, root, lib = job
    if not ci.Config.loaded:
        ci.Config.set_library_file(lib)
    index = ci.Index.create()
    try:
        tu = index.parse(path, args=args, options=ci.TranslationUnit.PARSE_INCOMPLETE)
    except ci.TranslationUnitLoadError:
        return [], []
    prefix = root + os.sep
    defs, calls = [], []

    def rel(cur):
        f = cur.location.file
        if f is None:
            return None
        n = os.path.realpath(f.name)
        return n[len(prefix):] if n.startswith(prefix) else None

    def visit(node, owner):
        for c in node.get_children():
            r = rel(c)
            if r is None and owner is None:
                # a library header's declarations (ROS, Boost, the standard
                # library): nothing of the repository's own is inside
                continue
            if c.kind in DEF_KINDS and c.is_definition() and r is not None:
                did = "%s:%d" % (r, c.location.line)
                # every name the body calls, bound or not: a call clang could not
                # bind (its types came from a stubbed header) is not even a
                # CALL_EXPR, and must read as undetermined, not as absent
                toks = [t.spelling for t in c.get_tokens()]
                called = {}
                # read from the source lines, not the cursor's tokens: a body
                # written after a macro (TEST_F(…) { … }) is outside them
                for a in CALLED.findall(source_text(c)):
                    called[a] = called.get(a, 0) + 1
                if c.spelling in called:
                    called[c.spelling] -= 1  # the definition's own name
                name = def_name(c)
                # with googletest present the body is a TestBody method of a
                # generated class; stubbed, TEST_F(…) parses as a function
                if c.spelling in ("TestBody", toks[0] if toks else ""):
                    m = GTEST.match(source_line(c))
                    if m:
                        name = m.group(1) + "." + m.group(2)  # TEST_F(Suite, Case) { … }: Suite.Case
                defs.append({"usr": c.get_usr(), "id": did, "path": r, "line": c.location.line,
                             "end": c.extent.end.line, "name": name, "called": called})
                visit(c, did)
                continue
            if c.kind == ci.CursorKind.CALL_EXPR and owner is not None and c.spelling.startswith("operator"):
                # a == b: reponite records no call site for it, so it is named
                # (undetermined) but never an edge
                calls.append({"from": owner, "name": c.spelling, "usr": None, "external": False,
                              "line": c.location.line, "bound": False})
                visit(c, owner)
                continue
            if c.kind == ci.CursorKind.CALL_EXPR and owner is not None and c.spelling:
                ref = c.referenced
                if ref is not None and ref.kind in (ci.CursorKind.CONSTRUCTOR, ci.CursorKind.DESTRUCTOR):
                    calls.append({"from": owner, "name": c.spelling, "usr": None, "external": False,
                                  "line": c.location.line, "col": c.location.column, "bound": True})
                    visit(c, owner)
                    continue
                target, external, at = None, False, None
                if ref is not None:
                    d = ref.get_definition() or ref
                    if rel(d) is None and d.location.file is not None:
                        external = True
                    target = ref.get_usr()
                    if ref.get_definition() is not None and rel(d) is not None:
                        # the definition this file sees: two programs of the repo may
                        # each define a function of the same signature (same USR)
                        at = "%s:%d" % (rel(d), d.location.line)
                cands = None
                if ref is None:
                    # a dependent call: bound when every declaration its name
                    # finds is one function of the repo (overloads of one name in
                    # one directory are one definition)
                    found = overload_candidates(c)
                    if found:
                        cands = []
                        for d0 in found:
                            if d0.kind == ci.CursorKind.FUNCTION_TEMPLATE or d0.kind in DEF_KINDS or d0.kind == ci.CursorKind.USING_DECLARATION:
                                dd = d0.get_definition() or d0
                                rd = rel(dd)
                                cands.append(None if rd is None else "%s:%d" % (rd, dd.location.line))
                            else:
                                cands.append(None)
                qname = None
                if ref is not None and not external:
                    # a member of a class template's specialization has no
                    # definition of its own: named Class.method, it is matched
                    # to the template's definition below
                    qname = def_name(ref)
                calls.append({"from": owner, "name": c.spelling, "usr": target, "external": external,
                              "line": c.location.line, "col": c.location.column, "bound": ref is not None, "at": at,
                              "qname": qname, "cands": cands})
            # a lambda or a local class: its calls stay with the enclosing definition
            visit(c, owner)

    visit(tu.cursor, None)
    return defs, calls


def main():
    argv = sys.argv[1:]
    lib = "/usr/lib/x86_64-linux-gnu/libclang-21.so.1"
    jobs = os.cpu_count() or 2
    if "--libclang" in argv:
        i = argv.index("--libclang")
        lib = argv[i + 1]
        del argv[i:i + 2]
    extra = []
    while "--headers" in argv:
        i = argv.index("--headers")
        extra.append(os.path.realpath(argv[i + 1]))
        del argv[i:i + 2]
    if "--jobs" in argv:
        i = argv.index("--jobs")
        jobs = int(argv[i + 1])
        del argv[i:i + 2]
    root = os.path.realpath(argv[0])
    files = list(walk(root))
    sources = [f for f in files if f.endswith(SOURCES)]
    headers = [f for f in files if f.endswith(HEADERS)]
    # every directory holding headers, and every include/ directory's parent
    # chain, is an include root: `#include "pkg/x.h"` and `#include "x.h"`
    inc = set()
    for h in headers:
        d = os.path.dirname(h)
        inc.add(d)
        parts = os.path.relpath(d, root).split(os.sep)
        for i, p in enumerate(parts):
            if p == "include":
                inc.add(os.path.join(root, *parts[:i + 1]))
    inc_dirs = sorted(inc)
    # the host's own C and C++ library headers first; the copied tree fills in
    # what the host lacks (ROS, Eigen, Boost, the generated messages)
    sys_dirs = system_dirs(extra)
    sys_dirs += [d for d in header_dirs(extra) if d not in sys_dirs]
    stub_dir = tempfile.mkdtemp(prefix="cppgt-stubs-")
    stub_missing(root, sources, headers, inc_dirs, sys_dirs, stub_dir)
    # Eigen's SSE kernels need the compiler's own intrinsics headers, which
    # a copied tree lacks; its scalar path declares the same API
    args = ["-x", "c++", "-std=c++17", "-w", "-ferror-limit=0", "-nostdinc", "-nostdinc++", "-DEIGEN_DONT_VECTORIZE"]
    for d in inc_dirs + [stub_dir] + sys_dirs:
        args += ["-I", d]
    jobs_list = [(f, (args if not f.endswith(".c") else ["-x", "c", "-std=c11"] + args[3:]), root, lib)
                 for f in sources]
    with multiprocessing.Pool(jobs) as pool:
        results = pool.map(parse_one, jobs_list, chunksize=4)

    # Overloads (and const/non-const twins) of one name in one directory are
    # one definition, as reponite stores them: the first of them stands for
    # all, and a call to any of them is a call to it.
    groups = {}
    for ds, _ in results:
        for d in ds:
            k = (os.path.dirname(d["path"]), d["name"])
            g = groups.setdefault(k, {})
            g[d["id"]] = d
    canon = {}
    defs = []
    for k, g in groups.items():
        first = min(g.values(), key=lambda d: (d["path"], d["line"]))
        for i in g:
            canon[i] = first["id"]
        defs.append({"id": first["id"], "path": first["path"], "line": first["line"], "end": first["end"],
                     "name": first["name"], "test": is_test_path(first["path"])})
    by_usr = {}
    names, written, own_usrs = {}, {}, {}
    for ds, _ in results:
        for d in ds:
            by_usr.setdefault(d["usr"], canon[d["id"]])
            if d["id"] == canon[d["id"]]:
                own_usrs.setdefault(d["id"], set()).add(d["usr"])
            names.setdefault(canon[d["id"]], set()).update(n for n, k in d["called"].items() if k > 0)
            for n, k in d["called"].items():
                key = (canon[d["id"]], n)
                written[key] = max(written.get(key, 0), k)
    edges, external, have = [], set(), set()
    by_qname = {}  # Class.method -> the one definition of that name, if one
    for d in defs:
        by_qname.setdefault(d["name"], set()).add(d["id"])
    bound, unmapped = {}, set()
    for _, cs in results:
        for c in cs:
            c["from"] = canon.get(c["from"], c["from"])
            names.setdefault(c["from"], set()).add(c["name"])
            if c["bound"] and not c["external"] and c["usr"] and c.get("at") not in canon and c["usr"] not in by_usr:
                ids = by_qname.get(c.get("qname") or "", set())
                if len(ids) == 1:
                    c["at"] = next(iter(ids))  # the template's own definition
            if not c["bound"] and c.get("cands"):
                ids = {canon.get(x) if x else None for x in c["cands"]}
                if None not in ids and len(ids) == 1:
                    c["bound"], c["at"], c["usr"] = True, next(iter(ids)), None  # one function of the repo
                elif ids == {None} and not any(c["cands"]):
                    c["bound"], c["external"] = True, True  # only library functions
            known = c["bound"] and (c["usr"] is None or c["external"] or c.get("at") in canon or c["usr"] in by_usr)
            if not known and not c["name"].startswith("operator"):
                unmapped.add(c["from"] + ">" + c["name"])
            if known:
                # one call site once, however often the AST shows it (a lambda's
                # body under its expression and its call operator); a call clang
                # bound to a repository definition this run did not record (a
                # template member it did not instantiate) is not known
                bound.setdefault((c["from"], c["name"]), set()).add((c["line"], c.get("col", 0)))
            to = canon.get(c["at"]) if c.get("at") else None
            if to is None:
                to = by_usr.get(c["usr"]) if c["usr"] else None
            if to:
                if (c["from"], to) not in have:
                    have.add((c["from"], to))
                    edges.append({"from": c["from"], "to": to, "kind": "static", "line": c["line"]})
            elif c["external"]:
                external.add(c["from"] + ">" + c["name"])
    out = {"lang": "cpp", "defs": sorted(defs, key=lambda d: d["id"]),
           "edges": sorted(edges, key=lambda e: (e["from"], e["to"])),
           "names": {k: sorted(v) for k, v in sorted(names.items())},
           "resolved_external": sorted(external),
           # a name called more often than clang bound it: some call of it is
           # undetermined, whatever the others resolved to
           "unresolved": sorted(unmapped | {f + ">" + n for (f, n), k in written.items() if k > len(bound.get((f, n), ()))})}
    print(f"cppgt: {len(sources)} sources, {len(defs)} definitions, {len(edges)} edges, "
          f"{len(stubbed)} headers stubbed: {' '.join(sorted(stubbed)[:40])}", file=sys.stderr)
    json.dump(out, sys.stdout, indent=1)


if __name__ == "__main__":
    main()

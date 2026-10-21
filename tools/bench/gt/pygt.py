"""Ground truth of a Python tree for the reponite benchmark.

Reads every .py file under a directory with the standard library's ast module
and prints JSON: each function/method definition with its span, the calls that
can be resolved statically (a module-level function of the same module, a
function imported by name or through an imported module, a method on self/cls
found in the class or an in-repo base class in method resolution order, super()
calls on the bases, a class called as a constructor), and for every definition
the names it calls
at all — so a scorer can tell a contradicted edge (the name is never called)
from an undetermined one (called on an object whose type is not static).

usage: python3 -I pygt.py <dir> > truth.json
"""

import ast
import json
import os
import sys


def is_test_path(rel):
    """test code by pytest's conventions: test_*.py, *_test.py, conftest.py,
    or under a tests/ or test/ directory (testing.py is not a test)"""
    base = rel.split("/")[-1]
    return (base.startswith("test_") or base.endswith("_test.py") or base == "conftest.py"
            or "/tests/" in "/" + rel or "/test/" in "/" + rel)


def module_name(rel):
    p = rel[:-3]
    if p.endswith("/__init__"):
        p = p[: -len("/__init__")]
    return p.replace("/", ".")


class Collector(ast.NodeVisitor):
    def __init__(self, rel):
        self.rel = rel
        self.defs = []  # (qualname, node, class_name)
        self.classes = {}  # name -> (node, bases as written)
        self.stack = []

    def visit_ClassDef(self, node):
        if not self.stack:
            self.classes[node.name] = (node, [b for b in node.bases])
            self.defs.append((node.name, node, None))  # a class is called as its constructor
        self.stack.append(("class", node.name))
        self.generic_visit(node)
        self.stack.pop()

    def _func(self, node):
        if any((isinstance(d, ast.Name) and d.id == "overload") or (isinstance(d, ast.Attribute) and d.attr == "overload")
               for d in node.decorator_list):
            return  # a typing.overload stub declares a signature, not code
        cls = self.stack[-1][1] if self.stack and self.stack[-1][0] == "class" else None
        nested = any(k == "func" for k, _ in self.stack)
        if not nested:
            q = f"{cls}.{node.name}" if cls else node.name
            self.defs.append((q, node, cls))
        self.stack.append(("func", node.name))
        self.generic_visit(node)
        self.stack.pop()

    visit_FunctionDef = _func
    visit_AsyncFunctionDef = _func


def call_name(func):
    """(qualifier chain, name) of a call target: f -> ([], f); a.b.f -> ([a, b], f)."""
    chain = []
    n = func
    while isinstance(n, ast.Attribute):
        chain.append(n.attr)
        n = n.value
    if isinstance(n, ast.Name):
        chain.append(n.id)
        chain.reverse()
        return chain[:-1], chain[-1]
    if chain:
        return None, chain[0]  # call on an expression: name known, receiver not
    return None, None


def site_key(caller, func):
    """one call site: caller id, called name and its position (as jedigt
    reads it)"""
    if isinstance(func, ast.Attribute):
        return f"{caller}>{func.attr}@{func.end_lineno}:{func.end_col_offset - len(func.attr)}"
    if isinstance(func, ast.Name):
        return f"{caller}>{func.id}@{func.lineno}:{func.col_offset}"
    return f"{caller}>?"


def main():
    root = os.path.abspath(sys.argv[1])
    files = {}
    for d, dirs, fs in os.walk(root):
        dirs[:] = [x for x in dirs if not x.startswith(".") and x not in ("node_modules", "__pycache__")]
        for f in fs:
            if f.endswith(".py"):
                full = os.path.join(d, f)
                rel = os.path.relpath(full, root).replace(os.sep, "/")
                try:
                    with open(full, encoding="utf-8") as fh:
                        files[rel] = ast.parse(fh.read(), filename=rel)
                except (SyntaxError, UnicodeDecodeError, ValueError) as e:
                    # a file this Python cannot parse (newer syntax) is not
                    # ground truth: say so, the scores would silently shrink
                    print(f"pygt: skipped {rel}: {e.__class__.__name__}: {e}", file=sys.stderr)

    mods = {module_name(r): r for r in files}
    info = {}
    for rel, tree in files.items():
        c = Collector(rel)
        c.visit(tree)
        info[rel] = c

    def def_id(rel, node):
        return f"{rel}:{node.lineno}"

    # module -> top-level function name -> id ; class -> method -> id
    funcs, methods, classes, class_ids = {}, {}, {}, {}
    out_defs = []
    for rel, c in info.items():
        for q, node, cls in c.defs:
            if isinstance(node, ast.ClassDef):
                i = def_id(rel, node)
                class_ids[(rel, node.name)] = i
                out_defs.append({"id": i, "path": rel, "line": node.lineno, "end": node.end_lineno, "name": q,
                                 "test": is_test_path(rel)})
                continue
            i = def_id(rel, node)
            out_defs.append({"id": i, "path": rel, "line": node.lineno, "end": node.end_lineno, "name": q,
                             "test": is_test_path(rel)})
            if cls:
                methods.setdefault((rel, cls), {})[node.name] = i
            else:
                funcs.setdefault(rel, {})[node.name] = i
        for name, (node, bases) in c.classes.items():
            classes[(rel, name)] = bases

    dirs = set()
    for r in files:
        d = os.path.dirname(r)
        while d:
            dirs.add(d)
            d = os.path.dirname(d)
    dirs.add("")

    def resolve_module(rel, dotted, level=0):
        if level:
            base = module_name(rel).split(".")
            if not rel.endswith("__init__.py"):
                base = base[:-1]
            base = base[: len(base) - (level - 1)] if level > 1 else base
            dotted = ".".join(base + ([dotted] if dotted else []))
            return mods.get(dotted)
        # an absolute import is found under a source root (the repo root,
        # src/, backend/ ...): the deepest one above the importing file
        first = dotted.split(".")[0]
        roots = [d for d in dirs if (os.path.join(d, first) in dirs or os.path.join(d, first + ".py") in files)
                 # a package is not a source root; the repo root is, __init__.py or not
                 and (d == "" or os.path.join(d, "__init__.py") not in files)]
        above = [d for d in roots if d == "" or rel.startswith(d + "/")]
        for root in sorted(above, key=len, reverse=True) + sorted(set(roots) - set(above), key=len):
            prefix = root.replace("/", ".") + "." if root else ""
            if prefix + dotted in mods:
                return mods[prefix + dotted]
        return None

    def reexported(src, name):
        """what module src binds name to by importing it (a package __init__
        re-exporting a submodule's class): ("func", id) | ("class", key)"""
        if src not in files:
            return None
        got = imports_of(src, files[src]).get(name)
        return got if got and got[0] in ("func", "class") else None

    imports_memo = {}

    def imports_of(rel, tree, body=None):
        """local name -> ("module", rel) | ("func", id) | ("class", (rel, name))"""
        if body is None:
            if rel not in imports_memo:
                imports_memo[rel] = {}  # an import cycle reads as no bindings
                imports_memo[rel] = module_imports(rel, tree, tree.body)
            return imports_memo[rel]
        return module_imports(rel, tree, body)

    def module_imports(rel, tree, body):
        b = {}
        for node in body:
            if isinstance(node, ast.Import):
                for a in node.names:
                    r = resolve_module(rel, a.name)
                    if r:
                        b[a.asname or a.name.split(".")[0]] = ("module", r) if a.asname or "." not in a.name else ("pkg", a.name)
            elif isinstance(node, ast.ImportFrom):
                src = resolve_module(rel, node.module or "", node.level or 0)
                for a in node.names:
                    local = a.asname or a.name
                    sub = resolve_module(rel, ((node.module + ".") if node.module else "") + a.name, node.level or 0)
                    # the package's own namespace first: an __init__ binding
                    # the name (from .m import m) shadows the submodule m
                    if src and src != rel and src.endswith("__init__.py") and (r := reexported(src, a.name)):
                        b[local] = r
                    elif sub:
                        b[local] = ("module", sub)
                    elif src and a.name in funcs.get(src, {}):
                        b[local] = ("func", funcs[src][a.name])
                    elif src and (src, a.name) in classes:
                        b[local] = ("class", (src, a.name))
                    elif src and (r := reexported(src, a.name)):
                        b[local] = r
        return b

    INERT = {"object", "ABC", "Generic", "Protocol"}

    def bases_of(key):
        """the class's bases as keys; None for a base outside the repo"""
        rel = key[0]
        imp = imports_of(rel, files[rel])
        out = []
        for b in classes[key]:
            if isinstance(b, ast.Subscript):
                b = b.value
            name = b.id if isinstance(b, ast.Name) else (b.attr if isinstance(b, ast.Attribute) else None)
            if name in INERT:
                continue
            if isinstance(b, ast.Name) and (rel, b.id) in classes:
                out.append((rel, b.id))
            elif isinstance(b, ast.Name) and imp.get(b.id, (None,))[0] == "class":
                out.append(imp[b.id][1])
            elif isinstance(b, ast.Attribute) and isinstance(b.value, ast.Name) and \
                    imp.get(b.value.id, (None,))[0] == "module" and (imp[b.value.id][1], b.attr) in classes:
                out.append((imp[b.value.id][1], b.attr))
            else:
                out.append(None)
        return out

    mro_memo = {}

    def mro(key, visiting=()):
        """C3 linearization as Python computes it; None marks an external base"""
        if key in mro_memo:
            return mro_memo[key]
        if key in visiting:
            return None
        seqs = []
        direct = bases_of(key)
        for b in direct:
            if b is None:
                seqs.append([None])
                continue
            m = mro(b, visiting + (key,))
            if m is None:
                return None
            seqs.append(list(m))
        seqs.append(list(direct))
        out = [key]
        while True:
            seqs = [s for s in seqs if s]
            if not seqs:
                break
            for s in seqs:
                head = s[0]
                if not any(head in t[1:] for t in seqs):
                    break
            else:
                return None  # no consistent order
            out.append(head)
            seqs = [s[1:] if s[0] == head else s for s in seqs]
        mro_memo[key] = out
        return out

    derived = {}
    for key in classes:
        for b in bases_of(key):
            if b is not None:
                derived.setdefault(b, []).append(key)

    def overriders(key, name):
        """name defined on classes deriving from key: on a subclass instance,
        self.name() runs one of these (dynamic dispatch)"""
        out, todo, seen_cls = [], list(derived.get(key, [])), {key}
        while todo:
            k = todo.pop()
            if k in seen_cls:
                continue
            seen_cls.add(k)
            if name in methods.get(k, {}):
                out.append(methods[k][name])
            todo += derived.get(k, [])
        return out

    def method_on(key, name, skip_own=False):
        order = mro(key)
        if order is None:
            return None
        for k in order[1:] if skip_own else order:
            if k is None:
                return None  # an external base may define it
            if name in methods.get(k, {}):
                return methods[k][name]
        return None

    # pytest fixtures: a test's parameter named like a fixture visible to its
    # module is that fixture's value. Calling it runs a function the fixture
    # defines and returns (a factory: code of the fixture's own body); a
    # method called on it is the method of the class the fixture constructs.
    def fixture_name(fn):
        for d in fn.decorator_list:
            call = d if isinstance(d, ast.Call) else None
            f = call.func if call else d
            if (isinstance(f, ast.Attribute) and f.attr == "fixture") or (isinstance(f, ast.Name) and f.id == "fixture"):
                for kw in (call.keywords if call else []):
                    if kw.arg == "name" and isinstance(kw.value, ast.Constant) and isinstance(kw.value.value, str):
                        return kw.value.value
                return fn.name
        return None

    fixtures = {}  # rel -> fixture name -> (def id, value)
    for rel, c in info.items():
        imp = imports_of(rel, files[rel])
        for q, node, cls in c.defs:
            if isinstance(node, ast.ClassDef) or cls:
                continue
            fname = fixture_name(node)
            if not fname:
                continue
            inner = {n.name for n in ast.walk(node) if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n is not node}
            lambdas = {t.id for n in ast.walk(node) if isinstance(n, ast.Assign) and isinstance(n.value, ast.Lambda)
                       for t in n.targets if isinstance(t, ast.Name)}
            value = None
            def own(n):
                """the nodes of n's own body, not of a function nested in it"""
                for ch in ast.iter_child_nodes(n):
                    if isinstance(ch, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
                        continue
                    yield ch
                    yield from own(ch)
            outs = [n.value for n in own(node) if isinstance(n, (ast.Return, ast.Yield)) and n.value is not None]
            ctor = {}  # name = Cls(...) in the fixture's own body
            for n in own(node):
                if isinstance(n, ast.Assign) and isinstance(n.value, ast.Call) and isinstance(n.value.func, ast.Name):
                    for t in n.targets:
                        if isinstance(t, ast.Name):
                            ctor[t.id] = n.value
            kinds = set()
            for v in outs:
                if isinstance(v, ast.Name) and v.id in ctor and v.id not in inner:
                    v = ctor[v.id]
                if isinstance(v, ast.Name) and (v.id in inner or v.id in lambdas):
                    kinds.add(("factory",))
                elif isinstance(v, ast.Lambda):
                    kinds.add(("factory",))
                elif isinstance(v, ast.Call) and isinstance(v.func, ast.Name):
                    n = v.func.id
                    key = (rel, n) if (rel, n) in classes else (imp[n][1] if imp.get(n, (None,))[0] == "class" else None)
                    kinds.add(("instance", key) if key else ("unknown",))
                else:
                    kinds.add(("unknown",))
            if len(kinds) == 1:
                value = next(iter(kinds))
            fixtures.setdefault(rel, {})[fname] = (def_id(rel, node), value)

    def plugin_modules(rel):
        """modules a conftest/test module makes fixtures visible from:
        pytest_plugins and star imports"""
        out = []
        for n in files[rel].body:
            if isinstance(n, ast.Assign) and any(isinstance(t, ast.Name) and t.id == "pytest_plugins" for t in n.targets):
                vals = n.value.elts if isinstance(n.value, (ast.List, ast.Tuple)) else [n.value]
                for v in vals:
                    if isinstance(v, ast.Constant) and isinstance(v.value, str) and (m := resolve_module(rel, v.value)):
                        out.append(m)
            if isinstance(n, ast.ImportFrom) and any(a.name == "*" for a in n.names):
                if (m := resolve_module(rel, n.module or "", n.level or 0)):
                    out.append(m)
        return out

    def visible_fixtures(rel):
        """fixture name -> (def id, value), nearest definition first"""
        order = [rel]
        d = os.path.dirname(rel)
        while True:
            cf = (d + "/" if d else "") + "conftest.py"
            if cf in files and cf != rel:
                order.append(cf)
            if not d:
                break
            d = os.path.dirname(d)
        for r in list(order):
            order += plugin_modules(r)
        by_id = {v[0]: v for fx in fixtures.values() for v in fx.values()}
        out = {}
        for r in order:
            for k, v in fixtures.get(r, {}).items():
                out.setdefault(k, v)
            # a fixture imported by name into the module (a conftest
            # gathering fixtures from submodules)
            for local, b in imports_of(r, files[r]).items():
                if b[0] == "func" and b[1] in by_id:
                    out.setdefault(local, by_id[b[1]])
        return out

    edges, names = [], {}
    seen = set()
    fixture_judged, fixture_external = set(), set()
    for rel, c in info.items():
        imp = imports_of(rel, files[rel])
        vis_fx = visible_fixtures(rel) if is_test_path(rel) else {}
        for q, node, cls in c.defs:
            if isinstance(node, ast.ClassDef):
                continue  # a class body's calls belong to its methods
            frm = def_id(rel, node)
            called = set()
            # imports inside the function bind names for its calls too
            local_imports = [n for n in ast.walk(node) if isinstance(n, (ast.Import, ast.ImportFrom))]
            fimp = dict(imp)
            if local_imports:
                fimp.update(imports_of(rel, files[rel], local_imports))
            params = {a.arg for a in node.args.args + node.args.kwonlyargs}
            local_names = set(params)
            # a test or fixture receives fixtures through its parameters
            takes_fixtures = vis_fx and (node.name.startswith("test") or fixture_name(node) is not None)
            fx_params = {p: vis_fx[p] for p in params if takes_fixtures and p in vis_fx and p not in ("self", "cls")}
            # decorators and default values run where the function is
            # defined, not when it is called: only the body's calls count
            body = [n for stmt in node.body for n in ast.walk(stmt)]
            for sub in body:
                if isinstance(sub, (ast.Assign, ast.AnnAssign)):
                    for t in (sub.targets if isinstance(sub, ast.Assign) else [sub.target]):
                        if isinstance(t, ast.Name):
                            local_names.add(t.id)
            for sub in body:
                if not isinstance(sub, ast.Call):
                    continue
                chain, name = call_name(sub.func)
                f = sub.func
                if isinstance(f, ast.Attribute) and isinstance(f.value, ast.Call) and \
                        isinstance(f.value.func, ast.Name) and f.value.func.id == "super" and cls and \
                        (not f.value.args or (isinstance(f.value.args[0], ast.Name) and f.value.args[0].id == cls)):
                    called.add(f.attr)
                    to = method_on((rel, cls), f.attr, skip_own=True)
                    if to and (frm, to) not in seen:
                        seen.add((frm, to))
                        edges.append({"from": frm, "to": to, "kind": "static", "line": sub.lineno})
                    continue
                if name is None:
                    continue
                called.add(name)
                to = None
                if chain == [] and name in fx_params:
                    fid, value = fx_params[name]
                    if value == ("factory",):
                        fixture_judged.add(site_key(frm, sub.func))
                        if (frm, fid) not in seen:
                            seen.add((frm, fid))
                            edges.append({"from": frm, "to": fid, "kind": "fixture", "line": sub.lineno})
                    elif value is not None:
                        # the fixture hands over a value it does not define
                        # (a mock, an instance): calling it runs none of the
                        # fixture's code
                        fixture_judged.add(site_key(frm, sub.func))
                        fixture_external.add(frm + ">" + name)
                    continue
                if chain and len(chain) == 1 and chain[0] in fx_params:
                    fid, value = fx_params[chain[0]]
                    if value and value[0] == "instance":
                        to = method_on(value[1], name)
                        if to:
                            fixture_judged.add(site_key(frm, sub.func))
                            if (frm, to) not in seen:
                                seen.add((frm, to))
                                edges.append({"from": frm, "to": to, "kind": "fixture", "line": sub.lineno})
                    continue
                if chain == [] and name not in local_names and fimp.get(name, (None,))[0] in ("module", "pkg") \
                        and name not in funcs.get(rel, {}):
                    # a module object called: no function of the repo runs
                    fixture_judged.add(site_key(frm, sub.func))
                    fixture_external.add(frm + ">" + name)
                    continue
                if chain == [] and (name not in local_names or name in fimp):
                    if name in funcs.get(rel, {}):
                        to = funcs[rel][name]
                    elif fimp.get(name, (None,))[0] == "func":
                        to = fimp[name][1]
                    elif (rel, name) in classes or fimp.get(name, (None,))[0] == "class":
                        key = (rel, name) if (rel, name) in classes else fimp[name][1]
                        to = class_ids.get(key)  # Cls(...) constructs Cls
                elif chain and len(chain) == 1 and chain[0] in ("self", "cls") and cls:
                    to = method_on((rel, cls), name)
                    if to:
                        for o in overriders((rel, cls), name):
                            if (frm, o) not in seen:
                                seen.add((frm, o))
                                edges.append({"from": frm, "to": o, "kind": "dispatch", "line": sub.lineno})
                elif chain and len(chain) == 1 and fimp.get(chain[0], (None,))[0] == "module":
                    m = fimp[chain[0]][1]
                    to = funcs.get(m, {}).get(name)
                elif chain and len(chain) == 1 and chain[0] not in local_names and \
                        ((rel, chain[0]) in classes or fimp.get(chain[0], (None,))[0] == "class"):
                    # Cls.f(): the class's own (or inherited) f — a classmethod
                    # or static method, or an unbound call
                    key = (rel, chain[0]) if (rel, chain[0]) in classes else fimp[chain[0]][1]
                    to = method_on(key, name)
                if to and (frm, to) not in seen:
                    seen.add((frm, to))
                    edges.append({"from": frm, "to": to, "kind": "static", "line": sub.lineno})
            names[frm] = sorted(called)
    out_defs.sort(key=lambda d: d["id"])
    json.dump({"lang": "python", "defs": out_defs, "edges": edges, "names": names,
               "fixture_judged": sorted(fixture_judged), "fixture_external": sorted(fixture_external)}, sys.stdout, indent=1)


if __name__ == "__main__":
    main()

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
                except (SyntaxError, UnicodeDecodeError, ValueError):
                    pass

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
                                 "test": "test" in rel.split("/")[-1] or "/tests/" in "/" + rel})
                continue
            i = def_id(rel, node)
            out_defs.append({"id": i, "path": rel, "line": node.lineno, "end": node.end_lineno, "name": q,
                             "test": "test" in rel.split("/")[-1] or "/tests/" in "/" + rel})
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
                 and os.path.join(d, "__init__.py") not in files]  # a package is not a source root
        above = [d for d in roots if d == "" or rel.startswith(d + "/")]
        for root in sorted(above, key=len, reverse=True) + sorted(set(roots) - set(above), key=len):
            prefix = root.replace("/", ".") + "." if root else ""
            if prefix + dotted in mods:
                return mods[prefix + dotted]
        return None

    def imports_of(rel, tree, body=None):
        """local name -> ("module", rel) | ("func", id) | ("class", (rel, name))"""
        b = {}
        for node in (body if body is not None else tree.body):
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
                    if sub:
                        b[local] = ("module", sub)
                    elif src and a.name in funcs.get(src, {}):
                        b[local] = ("func", funcs[src][a.name])
                    elif src and (src, a.name) in classes:
                        b[local] = ("class", (src, a.name))
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

    edges, names = [], {}
    seen = set()
    for rel, c in info.items():
        imp = imports_of(rel, files[rel])
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
            local_names = {a.arg for a in node.args.args + node.args.kwonlyargs}
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
                if to and (frm, to) not in seen:
                    seen.add((frm, to))
                    edges.append({"from": frm, "to": to, "kind": "static", "line": sub.lineno})
            names[frm] = sorted(called)
    out_defs.sort(key=lambda d: d["id"])
    json.dump({"lang": "python", "defs": out_defs, "edges": edges, "names": names}, sys.stdout, indent=1)


if __name__ == "__main__":
    main()

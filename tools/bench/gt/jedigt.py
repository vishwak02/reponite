"""Type-inferred call edges for the Python ground truth (needs `pip install jedi`).

pygt.py resolves only what is static by name: a call on a value whose type is
not written down (`order.save()`, `self.helper.run()`) is left undetermined,
and those edges cannot be scored. This adds, for every call inside a
definition, the target Jedi infers (assignments, return types, attributes,
class hierarchy) when it is a definition of the repo — as edges of kind
"inferred" — and lists the calls Jedi resolves outside the repo (a dict's
get, a library method) as "resolved_external", so the scorer can judge
most of what was undetermined.

usage: python3 -I jedigt.py <dir> <pygt truth.json> > truth+inferred.json
"""

import ast
import json
import os
import sys

import jedi


def resolve_call(script, line, col):
    """the definitions Jedi names for the call whose callee name is at
    (line, col): functions, classes, modules; [] when undetermined"""
    try:
        targets = script.goto(line, col, follow_imports=True)
    except Exception:  # jedi gives up on some constructs
        return []
    # a parameter (cls(...) in a classmethod, call_next(request)): goto stops
    # at its name, on the def line, which by_place would read as the
    # enclosing function itself; what it holds is the caller's value, so the
    # call is undetermined
    targets = [t for t in targets if t.type != "param"]
    if targets and all(t.type == "statement" for t in targets):
        # a name bound by assignment (`_aggs = es.aggs`, a tuple of them):
        # goto stops at the assignment, the value is the callee
        try:
            inferred = script.infer(line, col)
        except Exception:
            inferred = []
        if inferred and all(t.type in ("function", "class") for t in inferred):
            return inferred
        return []  # a variable whose value Jedi cannot name: undetermined
    return targets


def base_name(e):
    """the last name of a base class expression (abc.ABC, Generic[T])"""
    if isinstance(e, ast.Subscript):
        e = e.value
    if isinstance(e, ast.Attribute):
        return e.attr
    if isinstance(e, ast.Name):
        return e.id
    return ""


def stub_kind(fn):
    """what a method's definition says of its body: "abstract"
    (@abstractmethod), "ellipsis" (only ... or raise NotImplementedError, and
    a docstring), "pass" (only pass or a docstring), or "" for code"""
    for d in fn.decorator_list:
        if base_name(d.func if isinstance(d, ast.Call) else d) == "abstractmethod":
            return "abstract"
    body = list(fn.body)
    if body and isinstance(body[0], ast.Expr) and isinstance(body[0].value, ast.Constant) and \
            isinstance(body[0].value.value, str):
        body = body[1:]
    kind = "pass"
    for st in body:
        if isinstance(st, ast.Pass):
            continue
        if isinstance(st, ast.Expr) and isinstance(st.value, ast.Constant) and st.value.value is Ellipsis:
            kind = "ellipsis"
            continue
        if isinstance(st, ast.Raise) and st.exc is not None and \
                base_name(st.exc.func if isinstance(st.exc, ast.Call) else st.exc) == "NotImplementedError":
            kind = "ellipsis"
            continue
        return ""
    return kind


def main():
    root = os.path.abspath(sys.argv[1])
    with open(sys.argv[2], encoding="utf-8") as fh:
        truth = json.load(fh)
    defs_by_path = {}
    for d in truth["defs"]:
        defs_by_path.setdefault(d["path"], []).append(d)
    by_place = {(d["path"], d["line"]): d["id"] for d in truth["defs"]}
    have = {(e["from"], e["to"]) for e in truth["edges"]}
    # source roots: directories holding top-level packages (a package is
    # never one), so `from works.serializers import X` resolves as it runs
    roots = {root}
    for d, dirs, files in os.walk(root):
        dirs[:] = [x for x in dirs if not x.startswith(".") and x not in ("node_modules", "__pycache__")]
        if "__init__.py" in files and not os.path.exists(os.path.join(os.path.dirname(d), "__init__.py")):
            roots.add(os.path.dirname(d))
    projects = {}

    def project_for(rel):
        # as the file runs: a script's own directory is sys.path[0], and pytest
        # puts the first directory above a test without an __init__.py first;
        # `from utils.metrics import Metrics` next to the script is its utils
        d = os.path.dirname(os.path.join(root, rel))
        if d not in projects:
            near = []
            cur = d
            while cur.startswith(root):
                near.append(cur)
                if cur == root:
                    break
                cur = os.path.dirname(cur)
            projects[d] = jedi.Project(root, added_sys_path=near + sorted(r for r in roots if r not in near))
        return projects[d]

    def fresh_project(project):
        # a Project keeps inference state across every Script made from it:
        # one it has given up on can stay given up, where a new one (same
        # paths) resolves the call standalone
        return jedi.Project(root, added_sys_path=project.added_sys_path)

    def enclosing(path, line):
        best = None
        for d in defs_by_path.get(path, []):
            if d["line"] <= line <= d["end"] and (best is None or d["line"] > best["line"]):
                best = d
        return best

    # names the repo imports under another name (`import X as _X`): an untyped
    # call of the alias is just as undetermined for the name it stands for
    aliases = {}
    for rel in defs_by_path:
        try:
            with open(os.path.join(root, rel), encoding="utf-8") as fh:
                t = ast.parse(fh.read(), filename=rel)
        except (SyntaxError, UnicodeDecodeError, ValueError, OSError):
            continue
        for n in ast.walk(t):
            if isinstance(n, ast.ImportFrom):
                for a in n.names:
                    if a.asname and a.asname != a.name:
                        aliases.setdefault(a.asname, set()).add(a.name)
        for n in ast.walk(t):
            # _real = net.ssh_command, _n, _pct = text.n, text.pct: a name
            # bound to another module's function
            if not isinstance(n, ast.Assign) or len(n.targets) != 1:
                continue
            tg, val = n.targets[0], n.value
            names = tg.elts if isinstance(tg, ast.Tuple) else [tg]
            vals = val.elts if isinstance(val, ast.Tuple) else [val]
            if len(names) != len(vals):
                continue
            for nm, v in zip(names, vals):
                if isinstance(nm, ast.Name) and isinstance(v, ast.Attribute) and nm.id != v.attr:
                    aliases.setdefault(nm.id, set()).add(v.attr)

    # abstract methods: a call Jedi resolves to one (@abstractmethod, or a
    # stub body in an ABC or Protocol) runs, through an instance of a
    # concrete subclass, one of the subclasses' overrides — never the
    # abstract body. The class hierarchy comes from Jedi resolving each
    # class's bases.
    classes = {}  # (rel, class line) -> {"kind", "methods": name -> (line, stub), "bases": [place]}
    method_at = {}  # (rel, def line) -> (class place, name)
    for rel in sorted(defs_by_path):
        full = os.path.join(root, rel)
        try:
            with open(full, encoding="utf-8") as fh:
                src = fh.read()
            tree = ast.parse(src, filename=rel)
        except (SyntaxError, UnicodeDecodeError, ValueError, OSError):
            continue
        script = None
        for node in ast.walk(tree):
            if not isinstance(node, ast.ClassDef):
                continue
            names = [base_name(b) for b in node.bases] + [base_name(k.value) for k in node.keywords if k.arg == "metaclass"]
            kind = "protocol" if "Protocol" in names else ("abc" if "ABC" in names or "ABCMeta" in names else "")
            place = (rel, node.lineno)
            info = {"kind": kind, "methods": {}, "bases": []}
            for m in node.body:
                if isinstance(m, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    info["methods"][m.name] = (m.lineno, stub_kind(m))
                    method_at[(rel, m.lineno)] = (place, m.name)
            for b in node.bases:
                if base_name(b) in ("ABC", "Protocol", "object", "Generic"):
                    continue
                e = b.value if isinstance(b, ast.Subscript) else b
                if isinstance(e, ast.Attribute):
                    bl, bc = e.end_lineno, e.end_col_offset - len(e.attr)
                elif isinstance(e, ast.Name):
                    bl, bc = e.lineno, e.col_offset
                else:
                    continue
                if script is None:
                    script = jedi.Script(src, path=full, project=project_for(rel))
                try:
                    bts = script.goto(bl, bc, follow_imports=True)
                except Exception:
                    bts = []
                for t in bts:
                    if t.type == "class" and t.module_path is not None and t.line is not None and \
                            str(t.module_path).startswith(root + os.sep):
                        info["bases"].append((os.path.relpath(str(t.module_path), root).replace(os.sep, "/"), t.line))
            classes[place] = info
    derived = {}
    for place, info in classes.items():
        for b in info["bases"]:
            derived.setdefault(b, []).append(place)

    def class_kind(place, seen=()):
        info = classes.get(place)
        if info is None or place in seen:
            return ""
        if info["kind"]:
            return info["kind"]
        # ABCMeta is inherited; a class deriving from a Protocol without
        # naming Protocol again implements it
        for b in info["bases"]:
            if class_kind(b, seen + (place,)) == "abc":
                return "abc"
        return ""

    def is_abstract(place, name):
        line, stub = classes[place]["methods"][name]
        return stub == "abstract" or stub == "ellipsis" and class_kind(place) != "" or \
            stub == "pass" and classes[place]["kind"] == "protocol"

    def concrete_overrides(rel, line):
        hit = method_at.get((rel, line))
        if hit is None or not is_abstract(*hit):
            return []
        place, name = hit
        out, seen, queue = [], {place}, list(derived.get(place, []))
        while queue:
            c = queue.pop(0)
            if c in seen:
                continue
            seen.add(c)
            if name in classes[c]["methods"] and not is_abstract(c, name):
                out.append((c[0], classes[c]["methods"][name][0]))
            queue.extend(derived.get(c, []))
        return out

    added = dispatched = 0
    external, unresolved = set(truth.pop("fixture_external", [])), set()
    judged = set(truth.pop("fixture_judged", []))  # pygt read these as pytest fixtures
    for rel in sorted(defs_by_path):
        full = os.path.join(root, rel)
        try:
            with open(full, encoding="utf-8") as fh:
                src = fh.read()
            tree = ast.parse(src, filename=rel)
        except (SyntaxError, UnicodeDecodeError, ValueError, OSError) as e:
            print(f"jedigt: skipped {rel}: {e.__class__.__name__}", file=sys.stderr)
            continue
        project = project_for(rel)
        script = jedi.Script(src, path=full, project=project)
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call):
                continue
            f = node.func
            if isinstance(f, ast.Attribute):
                line, col = f.end_lineno, f.end_col_offset - len(f.attr)
            elif isinstance(f, ast.Name):
                line, col = f.lineno, f.col_offset
            else:
                continue
            caller = enclosing(rel, node.lineno)
            if caller is None:
                continue
            if f"{caller['id']}>{f.attr if isinstance(f, ast.Attribute) else f.id}@{line}:{col}" in judged:
                continue  # pygt read this call as a pytest fixture's
            targets = resolve_call(script, line, col)
            if not targets:
                # Jedi's inference state builds up across one script's
                # queries, and one project's: it can give up on a call that a
                # new script of a new project (the same paths) resolves
                targets = resolve_call(jedi.Script(src, path=full, project=fresh_project(project)), line, col)
            name = f.attr if isinstance(f, ast.Attribute) else f.id
            if not targets:
                # this call stays undetermined, whatever another call of the
                # same name in the caller resolved to
                unresolved.add(caller["id"] + ">" + name)
                for orig in aliases.get(name, ()):
                    unresolved.add(caller["id"] + ">" + orig)
                continue
            # outside the repo only when Jedi names a real function, class or
            # module there: on some code it lands on an unrelated stub line
            # (an Enum classmethod "resolved" to builtins.pyi as an instance)
            if targets and all((t.module_path is None or not str(t.module_path).startswith(root + os.sep))
                               and t.type in ("function", "class", "module") for t in targets):
                external.add(caller["id"] + ">" + name)  # a builtin or library function
                continue
            for t in targets:
                if t.module_path is None or t.line is None:
                    continue
                tp = os.path.relpath(str(t.module_path), root).replace(os.sep, "/")
                to = by_place.get((tp, t.line))
                if to is None or (caller["id"], to) in have:
                    continue
                have.add((caller["id"], to))
                truth["edges"].append({"from": caller["id"], "to": to, "kind": "inferred", "line": node.lineno})
                added += 1
            for t in targets:
                if t.type != "function" or t.module_path is None or t.line is None:
                    continue
                tp = os.path.relpath(str(t.module_path), root).replace(os.sep, "/")
                for op in concrete_overrides(tp, t.line):
                    to = by_place.get(op)
                    if to is None or (caller["id"], to) in have:
                        continue
                    have.add((caller["id"], to))
                    truth["edges"].append({"from": caller["id"], "to": to, "kind": "dispatch", "line": node.lineno})
                    dispatched += 1
    truth["resolved_external"] = sorted(external)
    truth["unresolved"] = sorted(unresolved)
    print(f"jedigt: {added} inferred edges, {dispatched} to concrete overrides of abstract methods, "
          f"{len(external)} calls resolved outside the repo", file=sys.stderr)
    json.dump(truth, sys.stdout, indent=1)


if __name__ == "__main__":
    main()

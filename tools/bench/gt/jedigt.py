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
    project = jedi.Project(root, added_sys_path=sorted(roots))

    def enclosing(path, line):
        best = None
        for d in defs_by_path.get(path, []):
            if d["line"] <= line <= d["end"] and (best is None or d["line"] > best["line"]):
                best = d
        return best

    added = 0
    external = set()
    for rel in sorted(defs_by_path):
        full = os.path.join(root, rel)
        try:
            with open(full, encoding="utf-8") as fh:
                src = fh.read()
            tree = ast.parse(src, filename=rel)
        except (SyntaxError, UnicodeDecodeError, ValueError, OSError) as e:
            print(f"jedigt: skipped {rel}: {e.__class__.__name__}", file=sys.stderr)
            continue
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
            try:
                targets = script.goto(line, col, follow_imports=True)
            except Exception:  # jedi gives up on some constructs
                continue
            name = f.attr if isinstance(f, ast.Attribute) else f.id
            if targets and all(t.module_path is None or not str(t.module_path).startswith(root + os.sep) for t in targets):
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
    truth["resolved_external"] = sorted(external)
    print(f"jedigt: {added} inferred edges, {len(external)} calls resolved outside the repo", file=sys.stderr)
    json.dump(truth, sys.stdout, indent=1)


if __name__ == "__main__":
    main()

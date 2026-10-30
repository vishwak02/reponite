// reponite's TypeScript/JavaScript precise worker: run by node, it reads one
// JSON request per line on stdin — a project's files and the typescript
// package to load — builds a program with the TypeScript compiler, resolves
// every call, `new` and JSX element of the project's files to the
// declaration the checker binds it to, and writes one JSON reply per line.
// Every file the compiler reads or looks for is reported, so the caller can
// tell later whether the result still holds without running it again.
"use strict";
const fs = require("fs");
const path = require("path");
const readline = require("readline");

const loaded = new Map(); // typescript package directory -> module

function loadTS(dir) {
  if (!loaded.has(dir)) loaded.set(dir, require(path.join(dir, "lib", "typescript.js")));
  return loaded.get(dir);
}

// tracker answers the compiler's file system questions and records them.
// Under the root only the repo's own files exist (the list the request
// gives), except node_modules directories, which are read from disk; every
// answer is recorded by a key: "R:<rel>" under the root, else the absolute
// path.
//
// A ref copied out of git (isolated) sits in a temporary directory: the
// directories above the copy exist (and are not recorded), and anything else
// in the temporary directory does not — so no answer depends on where the
// copy was put.
function tracker(root, repoFiles, isolated) {
  const files = new Set(repoFiles);
  const dirs = new Set([""]);
  for (const f of repoFiles) {
    let d = path.posix.dirname(f);
    while (d !== "." && !dirs.has(d)) {
      dirs.add(d);
      d = path.posix.dirname(d);
    }
  }
  const reads = new Set(), probes = {}, lists = {};
  const where = (p) => {
    const abs = path.resolve(root, p);
    if (abs === root) return { key: "R:", rel: "", abs, disk: false };
    if (abs.startsWith(root + path.sep)) {
      const rel = abs.slice(root.length + 1).split(path.sep).join("/");
      const disk = rel.split("/").includes("node_modules");
      return { key: "R:" + rel, rel, abs, disk };
    }
    return { key: abs, rel: null, abs, disk: true };
  };
  // around: for an isolated copy, true for a directory above the root,
  // false for anything else in the temporary directory beside it, null
  // otherwise (an answer to record)
  const tmp = path.dirname(root);
  const around = (w) => {
    if (!isolated || !w.disk || w.rel !== null) return null;
    if (root.startsWith(w.abs + path.sep) || w.abs === path.sep) return true;
    if (w.abs.startsWith(tmp + path.sep)) return false;
    return null;
  };
  const statOf = (abs) => {
    try {
      return fs.statSync(abs);
    } catch {
      return null;
    }
  };
  return {
    reads, probes, lists,
    fileExists(p) {
      const w = where(p);
      const a = around(w);
      if (a !== null) return false;
      let ok;
      if (w.disk) {
        const st = statOf(w.abs);
        ok = !!st && st.isFile();
      } else ok = files.has(w.rel);
      probes["f:" + w.key] = ok;
      return ok;
    },
    directoryExists(p) {
      const w = where(p);
      const a = around(w);
      if (a !== null) return a;
      let ok;
      if (w.disk) {
        const st = statOf(w.abs);
        ok = !!st && st.isDirectory();
      } else ok = dirs.has(w.rel);
      probes["d:" + w.key] = ok;
      return ok;
    },
    readFile(p) {
      const w = where(p);
      if (around(w) !== null) return undefined;
      if (!w.disk && !files.has(w.rel)) {
        probes["f:" + w.key] = false;
        return undefined;
      }
      let text;
      try {
        text = fs.readFileSync(w.abs, "utf8");
      } catch {
        probes["f:" + w.key] = false;
        return undefined;
      }
      reads.add(w.key);
      return text;
    },
    getDirectories(p) {
      const w = where(p);
      if (around(w) !== null) return [];
      let out = [];
      if (w.disk) {
        try {
          out = fs.readdirSync(w.abs, { withFileTypes: true }).filter((e) => e.isDirectory()).map((e) => e.name);
        } catch {
          out = [];
        }
      } else {
        const pre = w.rel === "" ? "" : w.rel + "/";
        for (const d of dirs) {
          if (d !== "" && d.startsWith(pre) && !d.slice(pre.length).includes("/")) out.push(d.slice(pre.length));
        }
      }
      out.sort();
      lists[w.key] = out.join("\n");
      return out;
    },
    // repo is where a declaration lives: "repo" (one of the repo's own
    // files), "ext" (a library, the compiler's lib, node_modules)
    repo(fileName) {
      const w = where(fileName);
      return !w.disk && files.has(w.rel) ? w.rel : null;
    },
  };
}

const defaults = (ts) => ({
  allowJs: true, checkJs: false, noEmit: true, skipLibCheck: true, jsx: ts.JsxEmit.Preserve,
  target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext, moduleResolution: ts.ModuleResolutionKind.Bundler,
  esModuleInterop: true, allowSyntheticDefaultImports: true, resolveJsonModule: true, maxNodeModuleJsDepth: 0,
});

// options reads a project's compiler options from its config ("" none);
// a solution-style config (references, no files of its own) lends the
// options of the reference that holds the application's sources.
function options(ts, t, root, config) {
  let opts = defaults(ts);
  if (!config) return opts;
  const host = {
    useCaseSensitiveFileNames: true,
    readDirectory: () => [],
    fileExists: (p) => t.fileExists(p),
    readFile: (p) => t.readFile(p),
  };
  const parse = (file) => {
    const abs = path.resolve(root, file);
    const read = ts.readConfigFile(abs, (p) => t.readFile(p));
    if (read.error || !read.config) return null;
    return { raw: read.config, parsed: ts.parseJsonConfigFileContent(read.config, host, path.dirname(abs), undefined, abs) };
  };
  const top = parse(config);
  if (!top) return opts;
  let chosen = top.parsed.options;
  const refs = top.parsed.projectReferences || [];
  if (refs.length > 0 && !(top.raw.compilerOptions && Object.keys(top.raw.compilerOptions).length > 0)) {
    let best = null;
    for (const r of refs) {
      let p = r.path;
      if (!p.endsWith(".json")) p = path.join(p, "tsconfig.json");
      const sub = parse(p);
      if (!sub) continue;
      const inc = JSON.stringify(sub.raw.include || sub.raw.files || []);
      const score = (/app/i.test(path.basename(p)) ? 2 : 0) + (/src/.test(inc) ? 1 : 0);
      if (!best || score > best.score) best = { score, opts: sub.parsed.options };
    }
    if (best) chosen = best.opts;
  }
  opts = { ...opts, ...chosen, noEmit: true, allowJs: true, skipLibCheck: true, maxNodeModuleJsDepth: 0 };
  delete opts.outDir;
  delete opts.declaration;
  delete opts.composite;
  delete opts.incremental;
  delete opts.tsBuildInfoFile;
  if (opts.jsx === undefined) opts.jsx = ts.JsxEmit.Preserve;
  return opts;
}

function analyze(req) {
  const ts = loadTS(req.ts);
  const root = req.root;
  const t = tracker(root, req.repo, !!req.isolated);
  // the compiler itself is part of what a result depends on
  t.readFile(path.join(req.ts, "package.json"));
  const opts = options(ts, t, root, req.config);
  const base = ts.createCompilerHost(opts, true);
  const host = {
    getSourceFile(fileName, lang) {
      const text = t.readFile(fileName);
      return text === undefined ? undefined : ts.createSourceFile(fileName, text, lang, false);
    },
    getDefaultLibFileName: (o) => base.getDefaultLibFileName(o),
    getDefaultLibLocation: () => base.getDefaultLibLocation(),
    writeFile: () => {},
    getCurrentDirectory: () => root,
    getCanonicalFileName: (f) => f,
    useCaseSensitiveFileNames: () => true,
    getNewLine: () => "\n",
    fileExists: (p) => t.fileExists(p),
    readFile: (p) => t.readFile(p),
    directoryExists: (p) => t.directoryExists(p),
    getDirectories: (p) => t.getDirectories(p),
    realpath: (p) => p,
  };
  const rootNames = req.files.map((f) => path.join(root, f));
  const program = ts.createProgram({ rootNames, options: opts, host });
  const checker = program.getTypeChecker();
  const own = new Set(req.files.filter((f) => !/\.d\.[cm]?ts$/.test(f)));
  const calls = [];
  const lineOf = (node) => node.getSourceFile().getLineAndCharacterOfPosition(node.getStart()).line + 1;
  const textOf = (n) => (n && (ts.isIdentifier(n) || ts.isPrivateIdentifier(n) || ts.isStringLiteral(n)) ? n.text : "");

  const unwrap = (e) => {
    while (e && (ts.isParenthesizedExpression(e) || ts.isAsExpression(e) || ts.isNonNullExpression(e) ||
      ts.isTypeAssertionExpression(e) || (ts.isSatisfiesExpression && ts.isSatisfiesExpression(e)))) e = e.expression;
    return e;
  };
  const fnLike = (e) => e && (ts.isArrowFunction(e) || ts.isFunctionExpression(e));
  // named reports an object literal member that a declaration names: the
  // literal is a variable's value (const api = { get() {} }), or a member
  // of one; a literal passed along inline holds no definition
  const named = (d) => {
    let o = d.parent;
    while (o && ts.isObjectLiteralExpression(o)) {
      let p = o.parent;
      while (p && (ts.isParenthesizedExpression(p) || ts.isAsExpression(p) || (ts.isSatisfiesExpression && ts.isSatisfiesExpression(p)))) p = p.parent;
      if (p && (ts.isVariableDeclaration(p) || ts.isPropertyDeclaration(p) || ts.isExportAssignment(p))) return true;
      if (!p || !ts.isPropertyAssignment(p)) return false;
      o = p.parent;
    }
    return true; // not an object literal's member
  };

  // def classifies a declaration: {path, line, name, ctor} for a definition
  // with a body in the repo, "ext" for one outside it, "abstract" for a
  // declaration without a body in the repo (an interface member, a
  // function type, an overload signature), "value" for a value whose
  // content the declaration does not show (a variable, a parameter)
  function def(d) {
    if (!d) return null;
    const sf = d.getSourceFile();
    const rel = t.repo(sf.fileName);
    const at = (nameNode, name, ctor) => ({ path: rel, line: lineOf(nameNode || d), name, ctor: !!ctor });
    if (rel === null || sf.isDeclarationFile) return rel === null ? "ext" : "abstract";
    switch (d.kind) {
      case ts.SyntaxKind.FunctionDeclaration:
      case ts.SyntaxKind.MethodDeclaration:
      case ts.SyntaxKind.GetAccessor:
      case ts.SyntaxKind.SetAccessor:
        if (!d.body) return "abstract";
        if (!d.name || !named(d)) return "value"; // export default function () {}: no name to locate it by
        return at(d.name, textOf(d.name) || d.name.getText());
      case ts.SyntaxKind.Constructor:
        return def(d.parent);
      case ts.SyntaxKind.ClassDeclaration:
      case ts.SyntaxKind.ClassExpression:
        if (d.name) return at(d.name, textOf(d.name), true);
        if (ts.isVariableDeclaration(d.parent) && ts.isIdentifier(d.parent.name)) return at(d.parent.name, d.parent.name.text, true);
        return "value";
      case ts.SyntaxKind.VariableDeclaration:
      case ts.SyntaxKind.PropertyDeclaration:
      case ts.SyntaxKind.PropertyAssignment: {
        const init = unwrap(d.initializer);
        if (!init || !d.name || !(ts.isIdentifier(d.name) || ts.isPrivateIdentifier(d.name) || ts.isStringLiteral(d.name)) || !named(d)) return "value";
        // a variable that may be assigned again holds what it was given last
        const fixed = !ts.isVariableDeclaration(d) || (ts.isVariableDeclarationList(d.parent) && d.parent.flags & ts.NodeFlags.Const);
        if (!fixed) return "value";
        if (fnLike(init)) return at(d.name, textOf(d.name));
        if (ts.isClassExpression(init)) return at(d.name, textOf(d.name), true);
        // const X = memo(function X() {}), forwardRef((p, r) => ...): a
        // wrapper called on a function is what the name holds
        if (ts.isCallExpression(init) && init.arguments.some((a) => fnLike(unwrap(a)))) return at(d.name, textOf(d.name));
        return "value";
      }
      case ts.SyntaxKind.ArrowFunction:
      case ts.SyntaxKind.FunctionExpression: {
        // a function value: the definition that holds it, or none (a callback)
        const p = d.parent;
        if (p && (ts.isVariableDeclaration(p) || ts.isPropertyDeclaration(p) || ts.isPropertyAssignment(p)) && unwrap(p.initializer) === d) return def(p);
        if (d.name) return at(d.name, d.name.text);
        return "value";
      }
      case ts.SyntaxKind.ShorthandPropertyAssignment:
      case ts.SyntaxKind.Parameter:
      case ts.SyntaxKind.BindingElement:
      case ts.SyntaxKind.ExportAssignment:
      case ts.SyntaxKind.ExportSpecifier:
      case ts.SyntaxKind.ImportSpecifier:
      case ts.SyntaxKind.ImportClause:
      case ts.SyntaxKind.NamespaceImport:
      case ts.SyntaxKind.ImportEqualsDeclaration:
        return "value";
      case ts.SyntaxKind.MethodSignature:
      case ts.SyntaxKind.PropertySignature:
      case ts.SyntaxKind.CallSignature:
      case ts.SyntaxKind.ConstructSignature:
      case ts.SyntaxKind.FunctionType:
      case ts.SyntaxKind.ConstructorType:
      case ts.SyntaxKind.InterfaceDeclaration:
      case ts.SyntaxKind.TypeAliasDeclaration:
      case ts.SyntaxKind.TypeLiteral:
      case ts.SyntaxKind.IndexSignature:
        return "abstract";
    }
    return "value";
  }

  // resolve is what a call binds to: ["B", [targets]] bound into the repo,
  // ["E"] bound outside it, ["U"] not bound (any, unknown, a value the
  // checker cannot follow to a definition)
  function resolve(nameNode) {
    let sym;
    try {
      sym = checker.getSymbolAtLocation(nameNode);
      if (sym && sym.flags & ts.SymbolFlags.Alias) sym = checker.getAliasedSymbol(sym);
    } catch {
      sym = undefined;
    }
    const decls = (sym && sym.declarations) || [];
    const targets = [];
    const values = [];
    let ext = false, abstract = false;
    for (const d of decls) {
      const r = def(d);
      if (r === "ext") ext = true;
      else if (r === "abstract") abstract = true;
      else if (r && typeof r === "object") targets.push(r);
      else values.push(d);
    }
    if (targets.length > 0) return ["B", targets];
    if (decls.length > 0 && ext && !abstract && values.length === 0) return ["E"];
    // a value — a variable holding a function, a parameter — is not
    // followed: its type names a function, not the one it holds (and a
    // constant alias, const run = start, is left to the inferred edges)
    return ["U"];
  }

  function emit(rel, nameNode, ctorCall) {
    const name = textOf(nameNode);
    if (!name) return;
    const line = lineOf(nameNode);
    const [kind, targets] = resolve(nameNode);
    if (kind !== "B") {
      calls.push([rel, line, name, kind]);
      return;
    }
    for (const x of targets) {
      // a `new` of a function, a call of a class: not a construction this
      // can name (a class is never called without new in TypeScript)
      if (x.ctor !== ctorCall) {
        calls.push([rel, line, name, "U"]);
        continue;
      }
      calls.push([rel, line, name, "B", x.path, x.line, x.name, x.ctor ? 1 : 0]);
    }
  }

  const calleeName = (e) => {
    e = unwrap(e);
    if (!e) return null;
    if (ts.isIdentifier(e)) return e;
    if (ts.isPropertyAccessExpression(e)) return e.name;
    return null;
  };

  for (const sf of program.getSourceFiles()) {
    const rel = t.repo(sf.fileName);
    if (rel === null || !own.has(rel)) continue;
    const visit = (node) => {
      if (ts.isCallExpression(node)) {
        const n = calleeName(node.expression);
        if (n && node.expression.kind !== ts.SyntaxKind.SuperKeyword && node.expression.kind !== ts.SyntaxKind.ImportKeyword) emit(rel, n, false);
      } else if (ts.isNewExpression(node)) {
        const n = calleeName(node.expression);
        if (n) emit(rel, n, true);
      } else if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
        const tag = node.tagName;
        const n = ts.isIdentifier(tag) ? tag : ts.isPropertyAccessExpression(tag) ? tag.name : null;
        // an intrinsic element (<div>) is not a component
        if (n && !(ts.isIdentifier(tag) && /^[a-z]/.test(tag.text))) {
          const [kind, targets] = resolve(n);
          const line = lineOf(n);
          if (kind !== "B") calls.push([rel, line, n.text, kind]);
          else for (const x of targets) calls.push([rel, line, n.text, "B", x.path, x.line, x.name, x.ctor ? 1 : 0]);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  }
  return {
    version: ts.version,
    calls,
    reads: [...t.reads].sort(),
    probes: t.probes,
    lists: t.lists,
    files: program.getSourceFiles().length,
  };
}

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on("line", (line) => {
  if (!line.trim()) return;
  let out;
  try {
    out = analyze(JSON.parse(line));
  } catch (e) {
    out = { err: String((e && e.stack) || e).slice(0, 2000) };
  }
  process.stdout.write(JSON.stringify(out) + "\n");
});

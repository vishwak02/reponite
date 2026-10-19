// Ground truth of a TypeScript/JavaScript tree for the reponite benchmark,
// from the TypeScript compiler's type checker: every function, method,
// class and function-valued variable with its span, and every call (and JSX
// element) whose target the checker resolves to one of them. For each
// definition, the names it calls at all are listed too, so a scorer can tell
// a contradicted edge from one the checker could not type.
//
//   (cd tools/bench/gt && npm ci) && node tools/bench/gt/tsgt.mjs <dir> > truth.json
import { createRequire } from "node:module";
import fs from "node:fs";
import path from "node:path";

const [, , dirArg] = process.argv;
const root = path.resolve(dirArg);
const require = createRequire(import.meta.url);
const ts = require("typescript");

const exts = new Set([".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"]);
const files = [];
(function walk(d) {
  for (const e of fs.readdirSync(d, { withFileTypes: true })) {
    if (e.name.startsWith(".") || e.name === "node_modules" || e.name === "dist" || e.name === "build") continue;
    const p = path.join(d, e.name);
    if (e.isDirectory()) walk(p);
    else if (exts.has(path.extname(e.name)) && !e.name.endsWith(".d.ts")) files.push(p);
  }
})(root);

// honour every tsconfig's paths (aliases like @/), without its file lists
let options = { allowJs: true, checkJs: false, jsx: ts.JsxEmit.Preserve, noEmit: true, skipLibCheck: true,
  target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext, moduleResolution: ts.ModuleResolutionKind.Bundler,
  esModuleInterop: true, allowSyntheticDefaultImports: true };
const configs = [];
(function findConfigs(d) {
  for (const e of fs.readdirSync(d, { withFileTypes: true })) {
    if (e.name === "node_modules" || e.name.startsWith(".")) continue;
    const p = path.join(d, e.name);
    if (e.isDirectory()) findConfigs(p);
    else if (e.name === "tsconfig.json" || e.name === "tsconfig.app.json") configs.push(p);
  }
})(root);
const programs = [];
const byConfig = new Map(); // config dir -> files under it
for (const f of files) {
  let best = null;
  for (const c of configs) {
    const cd = path.dirname(c);
    if (f.startsWith(cd + path.sep) && (!best || cd.length > path.dirname(best).length)) best = c;
  }
  const k = best || "";
  if (!byConfig.has(k)) byConfig.set(k, []);
  byConfig.get(k).push(f);
}
for (const [cfg, fs_] of byConfig) {
  let opts = { ...options };
  if (cfg) {
    const read = ts.readConfigFile(cfg, ts.sys.readFile);
    if (!read.error) {
      const parsed = ts.parseJsonConfigFileContent(read.config, ts.sys, path.dirname(cfg));
      opts = { ...opts, ...parsed.options, noEmit: true, allowJs: true, skipLibCheck: true };
    }
  }
  programs.push(ts.createProgram(fs_, opts));
}

const rel = (p) => path.relative(root, p).split(path.sep).join("/");
const defs = [];
const defAt = new Map(); // declaration node -> id
const edges = [];
const names = {};
const seenEdge = new Set();

// const X = memo(function X(){}) / forwardRef((p, r) => ...) / React.memo(...):
// a wrapper called on the function names it after the variable. A method
// called on a value (z.object().refine((d) => ...)) is not a wrapper.
function wrapperCall(call, fn) {
  if (!ts.isCallExpression(call) || call.arguments[0] !== fn) return false;
  const c = call.expression;
  return ts.isIdentifier(c) || (ts.isPropertyAccessExpression(c) && ts.isIdentifier(c.expression));
}

function nameOf(node) {
  if (node.name && ts.isIdentifier(node.name)) return node.name.text;
  if (node.name && ts.isPrivateIdentifier && ts.isPrivateIdentifier(node.name)) return node.name.text;
  if (ts.isConstructorDeclaration(node)) return "constructor";
  const p = node.parent;
  if (p && ts.isVariableDeclaration(p) && ts.isIdentifier(p.name)) return p.name.text;
  if (p && ts.isPropertyDeclaration(p) && p.name && ts.isIdentifier(p.name)) return p.name.text;
  if (p && ts.isPropertyAssignment(p) && p.name && ts.isIdentifier(p.name)) return p.name.text;
  if (p && wrapperCall(p, node) && p.parent && ts.isVariableDeclaration(p.parent) && ts.isIdentifier(p.parent.name)) return p.parent.name.text;
  return null;
}

// a function nested in another function belongs to it, as in the Python
// extractor and in reponite's model (its calls are the outer function's)
function insideFunction(n) {
  for (let p = n.parent; p; p = p.parent) {
    if (ts.isClassDeclaration(p) || ts.isClassExpression(p)) return false;
    if (ts.isFunctionDeclaration(p) || ts.isFunctionExpression(p) || ts.isArrowFunction(p) || ts.isMethodDeclaration(p) ||
      ts.isConstructorDeclaration(p) || ts.isGetAccessor(p) || ts.isSetAccessor(p)) return true;
  }
  return false;
}

function isDefNode(n) {
  if (insideFunction(n)) return false;
  return ts.isFunctionDeclaration(n) || ts.isMethodDeclaration(n) || ts.isClassDeclaration(n) ||
    ((ts.isArrowFunction(n) || ts.isFunctionExpression(n)) && nameOf(n) !== null &&
      (ts.isVariableDeclaration(n.parent) || ts.isPropertyDeclaration(n.parent) ||
        (wrapperCall(n.parent, n) && n.parent.parent && ts.isVariableDeclaration(n.parent.parent))));
}

function className(n) {
  for (let p = n.parent; p; p = p.parent) {
    if (ts.isClassDeclaration(p) || ts.isClassExpression(p)) return p.name ? p.name.text : null;
  }
  return null;
}

const visited = new Set();
for (const program of programs) {
  program.getTypeChecker(); // binds the files: parent links exist only after this
  for (const sf of program.getSourceFiles()) {
    const fn = path.resolve(sf.fileName);
    if (!fn.startsWith(root + path.sep) || fn.includes(path.sep + "node_modules" + path.sep) || visited.has(fn)) continue;
    visited.add(fn);
    (function collect(n) {
      if (isDefNode(n)) {
        const nm = nameOf(n);
        if (nm) {
          const line = sf.getLineAndCharacterOfPosition((n.name || n).getStart(sf)).line + 1;
          const end = sf.getLineAndCharacterOfPosition(n.getEnd()).line + 1;
          const cls = ts.isClassDeclaration(n) ? null : className(n);
          const id = rel(fn) + ":" + line;
          if (!defAt.has(n)) {
            defAt.set(n, id);
            defs.push({ id, path: rel(fn), line, end, name: cls ? cls + "." + nm : nm, test: /\.(test|spec)\.|__tests__|\/tests?\//.test(rel(fn)) });
          }
        }
      }
      ts.forEachChild(n, collect);
    })(sf);
  }
}

function enclosingDef(n) {
  for (let p = n.parent; p; p = p.parent) if (defAt.has(p) && !ts.isClassDeclaration(p)) return defAt.get(p);
  return null;
}

function targetDecl(checker, expr) {
  let sym = checker.getSymbolAtLocation(expr);
  if (!sym) return null;
  if (sym.flags & ts.SymbolFlags.Alias) sym = checker.getAliasedSymbol(sym);
  const d = (sym.declarations || [])[0];
  if (!d) return null;
  // const f = () => ... : the variable's initializer is the definition
  if (ts.isVariableDeclaration(d) && d.initializer) {
    let init = d.initializer;
    if (ts.isCallExpression(init) && init.arguments.length && (ts.isArrowFunction(init.arguments[0]) || ts.isFunctionExpression(init.arguments[0])) &&
      wrapperCall(init, init.arguments[0])) init = init.arguments[0];
    return init;
  }
  if (ts.isPropertyDeclaration(d) && d.initializer) return d.initializer;
  return d;
}

const done = new Set();
for (const program of programs) {
  const checker = program.getTypeChecker();
  for (const sf of program.getSourceFiles()) {
    const fn = path.resolve(sf.fileName);
    if (!fn.startsWith(root + path.sep) || fn.includes(path.sep + "node_modules" + path.sep) || done.has(fn)) continue;
    done.add(fn);
    (function walk(n) {
      let callee = null;
      if (ts.isCallExpression(n) || ts.isNewExpression(n)) callee = n.expression;
      else if (ts.isJsxOpeningElement(n) || ts.isJsxSelfClosingElement(n)) callee = n.tagName;
      if (callee) {
        const from = enclosingDef(n);
        if (from) {
          const nameNode = ts.isPropertyAccessExpression(callee) ? callee.name : callee;
          const called = nameNode.getText ? nameNode.getText(sf) : null;
          if (called && /^[A-Za-z_$][\w$]*$/.test(called)) (names[from] ||= new Set()).add(called);
          const d = targetDecl(checker, nameNode);
          const to = d && defAt.get(d);
          if (to && !seenEdge.has(from + ">" + to)) {
            seenEdge.add(from + ">" + to);
            edges.push({ from, to, kind: "static", line: sf.getLineAndCharacterOfPosition(n.getStart(sf)).line + 1 });
          }
        }
      }
      ts.forEachChild(n, walk);
    })(sf);
  }
}

defs.sort((a, b) => (a.id < b.id ? -1 : 1));
const namesOut = Object.fromEntries(Object.entries(names).map(([k, v]) => [k, [...v].sort()]));
process.stdout.write(JSON.stringify({ lang: "typescript", defs, edges, names: namesOut }, null, 1));

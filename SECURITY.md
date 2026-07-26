# Security policy

## Reporting a vulnerability

Please report security issues **privately** through
[GitHub's private vulnerability reporting](https://github.com/vishwak02/reponite/security/advisories/new)
rather than opening a public issue.

Please include what an attacker could achieve, the steps to reproduce, and the affected
version (`reponite version`). You can expect an initial response within a week.

## What reponite touches

Understanding the tool's footprint usually answers "is this a vulnerability?" quickly.

**It reads your source code and git objects.** Indexing walks a directory (or reads a
commit's tree via go-git) and stores file contents and derived symbol data in a local
SQLite database at `<repo>/.reponite/index.db`.

**Everything is local by default.** There is exactly one outbound network path in the
entire tool — the *optional* embeddings endpoint for neural semantic search — and it is
behind the `neural` build tag and disabled unless you set `REPONITE_EMBED_ENDPOINT`. If you
enable it, the text of the symbols being ranked is sent to the endpoint you configured.

**Servers bind to localhost.** `reponite serve` defaults to `127.0.0.1:8899`, and
`reponite mcp` speaks over stdio. Neither has authentication, because neither is intended
to be exposed. If you pass `--addr 0.0.0.0:…`, you are publishing a **read-only view of your
source code** to that network — put it behind your own authentication.

**Index contents mirror source sensitivity.** The index stores raw file content so `grep`
can search any indexed ref. Treat `.reponite/index.db` with the same care as the repository
itself, and use `.reponiteignore` to exclude paths that shouldn't be indexed at all.

## Things that are in scope

- Path traversal or arbitrary writes during indexing (e.g. a crafted repo escaping its directory)
- Crashes or unbounded memory from malformed input: source files, stack traces, `.reponiteignore`, or a hostile `index.scip`
- The `serve` HTTP surface reading or writing outside the indexed repositories
- Anything that causes reponite to execute code from an indexed repository

## Things that are not

- Exposing `serve` on a public interface yourself
- Secrets in your source appearing in `grep` results (index what you mean to index)
- Wrong or low-confidence analysis results — those are correctness bugs; please
  [file them as issues](https://github.com/vishwak02/reponite/issues/new/choose), they're
  very welcome

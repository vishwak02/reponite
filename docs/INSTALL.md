# Installing reponite

reponite is a single binary. Install it, index a repository, and it is ready to answer
questions from the command line, an AI agent (MCP), or the web dashboard.

- [Requirements](#requirements)
- [Install a release binary](#install-a-release-binary)
- [Build from source](#build-from-source)
- [Check the install](#check-the-install)
- [First index](#first-index)
- [Connect an AI agent](#connect-an-ai-agent)
- [Where reponite keeps its data](#where-reponite-keeps-its-data)
- [Upgrading](#upgrading)
- [Uninstalling](#uninstalling)
- [Troubleshooting](#troubleshooting)

## Requirements

| | |
|---|---|
| Operating system | Linux or macOS, amd64 or arm64 |
| To run | nothing else — the binary carries its parsers, storage and MCP server |
| To index git history (`--git`, `--tags`) | the repository's `.git` directory (read directly, no `git` command needed) |
| For `why` | the `git` command on `PATH` (it reads `git log -L`) |
| To build from source | Go 1.22 or newer and a C compiler (gcc or clang) for the parsers |

## Install a release binary

```sh
curl -fsSL https://raw.githubusercontent.com/vishwak02/reponite/main/install.sh | sh
```

The script downloads the binary of the [latest release](https://github.com/vishwak02/reponite/releases/latest)
for your OS and architecture, checks it against the release's `SHA256SUMS` when the release
has one, and installs it to `/usr/local/bin` (with `sudo` when that directory is not writable).
Choose another directory with `REPONITE_BINDIR`:

```sh
curl -fsSL https://raw.githubusercontent.com/vishwak02/reponite/main/install.sh | REPONITE_BINDIR=~/.local/bin sh
```

Or download the file for your platform from the releases page yourself
(`reponite-linux-amd64`, `reponite-linux-arm64`, `reponite-darwin-amd64`,
`reponite-darwin-arm64`), make it executable and put it on your `PATH`.

A release binary has every feature of its tag. Features added to `main` since the last
release are listed under *Unreleased* in the [changelog](../CHANGELOG.md); to use them before
the next release, build from source.

## Build from source

```sh
git clone https://github.com/vishwak02/reponite
cd reponite
make cli                      # → bin/reponite, with every adapter
install -m 755 bin/reponite ~/.local/bin/reponite
```

`make cli` fetches the parser and storage modules on first use, so the first build needs
network access. It is the same as:

```sh
go build -mod=mod -tags "sqlite treesitter mcp neural" -o bin/reponite ./cmd/reponite
```

The build tags select the adapters:

| Tag | Adds |
|---|---|
| *(none)* | the pure core: `version`, `guide`, `demo`, `setup` — builds anywhere, no C compiler |
| `sqlite` | the on-disk index |
| `treesitter` | the language parsers and git-history indexing (needs CGO) |
| `mcp` | the MCP server for AI agents |
| `neural` | the optional embeddings client for semantic search |

A build without `sqlite` and `treesitter` answers the index-backed commands with a message
saying which tags they need.

## Check the install

```sh
reponite version
reponite demo        # an in-memory run that needs no index
reponite --help      # every command
```

## First index

```sh
cd ~/src/my-project
reponite index .                 # the working tree, as ref HEAD
reponite index . --git v1.0.0    # a tag, read from git without checking it out
reponite map --format text       # what is in it
```

Indexing a repository also registers it in your fleet, so `serve`, `mcp` and the
cross-repo commands find it from any directory. The [user guide](USER_GUIDE.md) covers what
to ask next.

## Connect an AI agent

`reponite setup` writes the MCP server entry into a client's configuration:

```sh
reponite setup ~/src/my-project                         # Claude Desktop
reponite setup ~/src/my-project --client claude-code    # Claude Code (the repo's .mcp.json)
reponite setup ~/src/my-project --client cursor
reponite setup ~/src/my-project --client windsurf
reponite setup ~/src/my-project --config <file>         # another client using "mcpServers"
reponite setup ~/src/my-project --print                 # show the entry, write nothing
```

Restart the client afterwards. For a client that uses a different configuration format, add
a server that runs:

```
command: /path/to/reponite
args:    ["mcp", "/path/to/repo"]
```

With no directory, `reponite mcp` serves every repository in the fleet. The VS Code extension
in [`editors/vscode`](../editors/vscode) adds editor commands on top of the same binary.

## Where reponite keeps its data

| What | Where | Change with |
|---|---|---|
| A repository's index | `<repo>/.reponite/index.db` | `REPONITE_STORE_DIR` → `$REPONITE_STORE_DIR/<repo>/index.db` |
| The fleet registry (repo names and paths only) | `$XDG_CONFIG_HOME/reponite/fleet.json`, else `~/.config/reponite/fleet.json` | `REPONITE_FLEET` |
| The keyword-search cache | `$REPONITE_STORE_DIR/<repo>/semindex/`, else the user cache directory | — |

Add `.reponite/` to a repository's `.gitignore`. With `REPONITE_STORE_DIR` set and `--git`
indexing, reponite only reads a repository and never writes into it.

Nothing leaves the machine. The one network path is the optional embeddings endpoint for
semantic search (`REPONITE_EMBED_ENDPOINT`), used only when you set it.

## Upgrading

Run the install script again, or `git pull && make cli`. When a new version changes what is
extracted from source, `reponite version` shows a higher `index_ver`; the next `reponite
index` of a ref re-extracts it, and answers comparing refs indexed under different versions
say so. Re-index the refs you use:

```sh
reponite index .
reponite index . --git v1.0.0,v1.1.0
```

## Uninstalling

```sh
rm "$(command -v reponite)"
rm -r ~/.config/reponite                 # the fleet registry
rm -r <repo>/.reponite                   # each repository's index
```

Remove the `reponite` entry from your agent's MCP configuration as well.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `requires the index backend — build with the sqlite/treesitter/mcp tags` | The binary was built without the adapters. Use a release binary or `make cli`. |
| `no prebuilt binary for <os>/<arch>` from the install script | No release asset for that platform. Build from source. |
| A command reports `nothing was indexed` / `ref not indexed` | Run `reponite index` for that repository and ref; `reponite refs` lists what is indexed. |
| A command does not see another repository | `reponite fleet list` shows the registry; `reponite fleet add <dir>` registers an indexed repo, `--local` restricts a command to the current one. |
| The agent shows no reponite tools | Restart the client after `reponite setup`; check the entry with `reponite setup <dir> --print`. |
| `cgo: C compiler "gcc" not found` while building | Install gcc or clang (`build-essential`, Xcode Command Line Tools). |
| `why` returns no commits | The ref was indexed from a working tree. Index it with `--git`, and make sure `git` is on `PATH`. |
| `N file(s) not parsed (too large to be source, or binary)` after indexing | Generated headers, data blobs and bundled assets are skipped on purpose. Raise `REPONITE_MAX_SOURCE_BYTES` to parse larger files. |
| Indexing a very large tree uses a lot of memory | Peak memory grows with the number of symbols (about 5 GB for a full Linux kernel). `GOGC=30` lowers it further at some cost in time; Exclude generated or vendored directories in `.reponiteignore`; `REPONITE_MEMPROFILE=<file>` records where memory goes. |
| A command is slow | `REPONITE_CPUPROFILE=<file> reponite <command> …` records where the time goes; attach the file to an issue. |

#!/usr/bin/env bash
# Runs the public benchmark and fails when a score drops below the gate.
#
#   tools/bench/run.sh [workdir]
#
# Clones each repo of tools/bench/repos.txt at its pinned commit (reusing a
# clone already there), indexes it, computes ground truth with the
# extractors in tools/bench/gt, and scores. Needs Go, git, python3 (3.14+, so
# the ast parses the newest syntax the repos use) and Node
# (for the TypeScript checker; `npm ci` is run in tools/bench/gt).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
top="$(cd "$here/../.." && pwd)"
work="${1:-$top/.bench}"
mkdir -p "$work/repos" "$work/truth" "$work/bin"

# recall gates (percent); precision of resolved answers is always 100
recall_truth=99
recall_routes=100
recall_calls=100

(cd "$top" && go build -mod=mod -tags "sqlite treesitter" -o "$work/bin/reponite-bench-index" ./cmd/reponite)
(cd "$top" && go build -mod=mod -tags "bench sqlite treesitter" -o "$work/bin/reponite-bench-score" ./tools/bench/score)
(cd "$top" && go build -mod=mod -tags bench -o "$work/bin/reponite-bench-gogt" ./tools/bench/gt/gogt)
for tool in go git python3 node; do
  if ! command -v "$tool" >/dev/null; then
    echo "run.sh: $tool is not on PATH (needed for the benchmark)" >&2
    exit 2
  fi
done
if [ ! -d "$here/gt/node_modules/typescript" ]; then
  if ! command -v npm >/dev/null; then
    echo "run.sh: npm is not on PATH (needed once, to install the TypeScript checker)" >&2
    exit 2
  fi
  (cd "$here/gt" && npm ci --silent)
fi
export PATH="$work/bin:$PATH"

# one store per reponite build: an index made by other code is never reused
store="$work/store-$(sha256sum "$work/bin/reponite-bench-index" | cut -c1-12)"
export REPONITE_STORE_DIR="$store"
export REPONITE_FLEET="$store/fleet.json"
mkdir -p "$store"

status=0
while read -r repo url commit checks; do
  case "$repo" in
    "" | \#*) continue ;;
  esac
  dir="$work/repos/$repo"
  if [ ! -d "$dir/.git" ]; then
    git clone --quiet --filter=blob:none "$url" "$dir"
  fi
  git -C "$dir" checkout --quiet --detach "$commit"
  reponite-bench-index index "$dir" >/dev/null
  for check in ${checks//,/ }; do
    truth="$work/truth/$repo.$check.json"
    case "$check" in
      go) reponite-bench-gogt "$dir" > "$truth" ;;
      py) python3 -I "$here/gt/pygt.py" "$dir" > "$truth" ;;
      ts) node "$here/gt/tsgt.mjs" "$dir" > "$truth" ;;
    esac
    case "$check" in
      go | py | ts)
        reponite-bench-score -store "$store" -repo "$repo" -truth "$truth" \
          -min-precision 100 -min-recall "$recall_truth" || status=1 ;;
      routes)
        reponite-bench-score -store "$store" -repo "$repo" -routes "$here/golden/routes/$repo.txt" \
          -min-precision 100 -min-recall "$recall_routes" || status=1 ;;
      calls)
        reponite-bench-score -store "$store" -repo "$repo" -calls "$here/golden/calls/$repo.txt" \
          -min-precision 100 -min-recall "$recall_calls" || status=1 ;;
    esac
  done
done < "$here/repos.txt"
exit "$status"

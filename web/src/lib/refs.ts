// Which two refs of a repo "since last index" compares. The index keeps no
// dates, so the order is read from the names: the working ref (HEAD, else a
// branch) is the newest, a commit id or version tag older, and versions
// order by their numbers (v1.10 after v1.9).

const SHA = /^[0-9a-f]{7,40}$/i;
const VERSION = /^v?\d+(?:[.\-_]\d+)*(?:[-+.]?[a-z][\w.]*)?$/i;

// a branch: what is checked out or pushed to, not a fixed snapshot
export function isBranch(ref: string): boolean {
  return ref === "HEAD" || (!SHA.test(ref) && !VERSION.test(ref.replace(/^.*\//, "")));
}

// compares two refs by version, numbers numerically: negative when a is older
export function compareVersions(a: string, b: string): number {
  const parts = (s: string) => s.replace(/^.*\//, "").replace(/^v/i, "").split(/[.\-_+]/);
  const pa = parts(a);
  const pb = parts(b);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i] ?? "";
    const y = pb[i] ?? "";
    // a release is newer than its candidates (1.0.0 > 1.0.0-rc1), older
    // than its patches (1.0 < 1.0.1)
    if (x === "" || y === "") {
      if (x === y) continue;
      const rest = x === "" ? y : x;
      const sign = x === "" ? 1 : -1;
      return /^\d+$/.test(rest) ? -sign : sign;
    }
    const nx = /^\d+$/.test(x);
    const ny = /^\d+$/.test(y);
    if (nx && ny && +x !== +y) return +x - +y;
    if (nx !== ny) return nx ? 1 : -1; // 1.0 is newer than 1.0-rc
    if (!nx && x !== y) return x < y ? -1 : 1;
  }
  return 0;
}

// rank orders refs oldest first: commit ids, then versions, then branches
// (HEAD last), each group in its own order.
function rank(ref: string): number {
  if (ref === "HEAD") return 3;
  if (isBranch(ref)) return 2;
  if (SHA.test(ref)) return 0;
  return 1;
}

// latestPair is [from, to]: the newest ref and the one before it, or null
// for a repo with fewer than two refs. A HEAD that only stands for a pinned
// ref (the server's --refs) is not a ref of its own and is skipped.
export function latestPair(refs: string[], pinnedHead = false): [string, string] | null {
  const rs = [...new Set(refs)].filter((r) => !(pinnedHead && r === "HEAD"));
  if (rs.length < 2) return null;
  rs.sort((a, b) => rank(a) - rank(b) || (rank(a) === 1 ? compareVersions(a, b) : a < b ? -1 : a > b ? 1 : 0));
  return [rs[rs.length - 2], rs[rs.length - 1]];
}

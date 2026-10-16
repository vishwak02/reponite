// history_git.go is the git adapter behind query.HistoryProvider (`reponite
// why`). It shells out to the git binary because only git implements
// `log -L` (line-range history through every edit) and the pickaxe; every
// command it runs reads the object store and nothing else — no checkout, no
// index refresh (GIT_OPTIONAL_LOCKS=0), no fetch — so pointing it at a clone
// that must not be written to is safe. The parsing of what git prints is pure
// (query.ParseLineLog) and unit-tested there; this file only runs commands.
package processing

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/vishwak02/reponite/internal/query"
)

// logFormat is the per-commit header query.ParseLineLog reads.
const logFormat = "--format=%x1e%H%x1f%at%x1f%an%x1f%s%x1f%b%x1d"

// GitHistory reads history from each repo's git object store.
type GitHistory struct {
	dirs    map[string]string // repo name -> directory holding its .git
	Timeout time.Duration     // per git command (0 = 30s)
}

var _ query.HistoryProvider = (*GitHistory)(nil)

// NewGitHistory returns a provider for the given repo -> directory map.
// Repos missing from the map have no history (every call errors).
func NewGitHistory(dirs map[string]string) *GitHistory {
	return &GitHistory{dirs: dirs}
}

// Has reports whether repo maps to a directory git can read.
func (g *GitHistory) Has(repo string) bool {
	_, err := g.git(repo, "rev-parse", "--git-dir")
	return err == nil
}

func (g *GitHistory) git(repo string, args ...string) (string, error) {
	dir, ok := g.dirs[repo]
	if !ok || dir == "" {
		return "", fmt.Errorf("no directory known for repo %s", repo)
	}
	to := g.Timeout
	if to <= 0 {
		to = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), to)
	defer cancel()
	full := append([]string{"-C", dir, "--no-pager", "-c", "core.quotepath=off", "-c", "log.showSignature=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s timed out after %s", args[0], to)
		}
		msg := strings.TrimSpace(errb.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return out.String(), nil
}

// LineHistory runs `git log -L start,end:path commit`.
func (g *GitHistory) LineHistory(repo string, r query.LineRange, limit int) ([]query.Change, error) {
	if r.Commit == "" || r.Path == "" || r.Start <= 0 || r.End < r.Start {
		return nil, fmt.Errorf("incomplete line range")
	}
	args := []string{"log", "--no-color", "--no-ext-diff", logFormat,
		fmt.Sprintf("-L%d,%d:%s", r.Start, r.End, r.Path)}
	if limit > 0 {
		args = append(args, "-n", strconv.Itoa(limit))
	}
	out, err := g.git(repo, append(args, r.Commit)...)
	if err != nil {
		return nil, err
	}
	cs := query.ParseLineLog(out, r.Name)
	for i := range cs {
		if len(cs[i].Paths) == 0 {
			cs[i].Paths = []string{r.Path}
		}
	}
	return cs, nil
}

// Pickaxe runs `git log -S needle commit -- paths`.
func (g *GitHistory) Pickaxe(repo, commit, needle string, paths []string, limit int) ([]query.Change, error) {
	if needle == "" {
		return nil, fmt.Errorf("empty needle")
	}
	args := []string{"log", "--no-color", logFormat, "-S" + needle}
	if limit > 0 {
		args = append(args, "-n", strconv.Itoa(limit))
	}
	args = append(args, commit, "--")
	args = append(args, paths...)
	out, err := g.git(repo, args...)
	if err != nil {
		return nil, err
	}
	cs := query.ParseLineLog(out, needle)
	for i := range cs {
		cs[i].Paths = append([]string(nil), paths...)
	}
	return cs, nil
}

// Contains walks each tip's history once (rev-list) and reports which of
// commits each tip includes.
func (g *GitHistory) Contains(repo string, commits []string, tips map[string]string) (map[string][]string, error) {
	want := map[string]bool{}
	for _, c := range commits {
		want[c] = true
	}
	byTip := map[string][]string{} // tip commit -> labels (one walk per distinct commit)
	for label, c := range tips {
		byTip[c] = append(byTip[c], label)
	}
	out := map[string][]string{}
	for tip, labels := range byTip {
		list, err := g.git(repo, "rev-list", tip)
		if err != nil {
			return nil, err
		}
		for _, sha := range strings.Fields(list) {
			if want[sha] {
				out[sha] = append(out[sha], labels...)
			}
		}
	}
	return out, nil
}

// CommitTimes reads committer times; an unknown commit is simply absent.
func (g *GitHistory) CommitTimes(repo string, commits []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(commits) == 0 {
		return out, nil
	}
	parse := func(s string) {
		for _, l := range strings.Split(s, "\n") {
			f := strings.Fields(l)
			if len(f) == 2 {
				if t, err := strconv.ParseInt(f[1], 10, 64); err == nil {
					out[f[0]] = t
				}
			}
		}
	}
	args := append([]string{"log", "--no-walk=unsorted", "--format=%H %ct"}, commits...)
	if s, err := g.git(repo, args...); err == nil {
		parse(s)
		return out, nil
	}
	// One unknown commit fails the batch: retry one by one.
	var last error
	for _, c := range commits {
		s, err := g.git(repo, "log", "--no-walk", "--format=%H %ct", c)
		if err != nil {
			last = err
			continue
		}
		parse(s)
	}
	if len(out) == 0 && last != nil {
		return nil, last
	}
	return out, nil
}

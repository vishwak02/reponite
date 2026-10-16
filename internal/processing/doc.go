// Package processing is the write path: walking a working tree or a git
// revision's tree, parsing source (tree-sitter, behind the build tag) and
// storing launch, parameter, data and project files as content, resolving
// call edges, and the behavior-hash pass (reverse-topological, SCC-condensed,
// memoized), which is pure and unit-tested without any adapter.
package processing

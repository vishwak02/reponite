// Target production toolchain is Go 1.22+. Pinned to 1.18 here only so the
// dependency-free core compiles in the build sandbox (module proxy is blocked).
module github.com/reponite/reponite

go 1.18

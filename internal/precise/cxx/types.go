package cxx

// Def is a function definition of the repo the unit contains.
type Def struct {
	USR      string
	Path     string // repo-relative
	Line     int
	DeclPath string // its first declaration (the class body's, a header's)
	DeclLine int
}

// Call is one call inside a repo definition.
type Call struct {
	OwnerPath string
	OwnerLine int
	Name      string
	Kind      byte // 'B' bound into the repo (USR), 'V' bound to a virtual method (overrides may run), 'E' bound outside it, 'U' not bound
	USR       string
	DeclPath  string // the bound declaration's canonical (first) declaration
	DeclLine  int
	RecvPath  string // a virtual call's object's class definition ("" unknown)
	RecvLine  int
	OwnerName string // the calling definition's name: several may share its line (a macro's members)
}

// Result is what one translation unit showed.
type Result struct {
	Defs     []Def
	Calls    []Call
	Includes []string // repo files the unit included
	Others   []string // other files it read (absolute): libraries, generated headers
	Missing  []Probe  // includes it did not find, as written, and the file that wrote each
	Refs     []Ref    // references to functions by name inside repo definitions
	Errors   int      // error diagnostics
	Diags    []string // the first of them
}

// Request is one unit for a worker to parse.
type Request struct {
	Root string   `json:"root"`
	File string   `json:"file"`
	Args []string `json:"args"`
}

// Reply is a worker's answer to one Request.
type Reply struct {
	Result
	Err string `json:"err,omitempty"`
}

// Probe is a file name a unit looked for: written as Spelling, in an #include
// or __has_include of From (an absolute path when the walker reports it; a
// repo or submodule directory, rootMark/ or libMark/, in a manifest), and
// before which include directory Before ("" all of them) it must not appear.
type Probe struct {
	Spelling string
	From     string
	Before   string
}

// Ref is a reference to a function by name inside a repo definition — the
// callee of a call, or a call clang's error recovery dropped.
type Ref struct {
	OwnerPath string
	OwnerLine int
	Name      string
	OwnerName string
}

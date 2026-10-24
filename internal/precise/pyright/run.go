// Package pyright is the Python driver of the compiler-exact tier.
package pyright

import (
	"context"
	"io"
	"path"
	"strings"
	"time"

	"github.com/vishwak02/reponite/internal/precise"
)

// Kind is this driver's directory in the precise store.
const Kind = "py"

// Config is how to run the driver.
type Config struct {
	Node    string
	Pyright string
	Jobs    int
	Store   *precise.Store
	Timeout time.Duration
	Log     io.Writer
}

// Input is one ref's Python code.
type Input struct {
	Files       map[string]string
	Extra       map[string]string
	Dir         string
	Read        func(rel string) ([]byte, error)
	Materialize func() (root string, cleanup func(), err error)
	CachedOnly  bool
}

// Edge is one call pyright proved.
type Edge struct {
	CallerPath string
	CallerLine int
	Name       string
	CalleePath string
	CalleeLine int
	CalleeName string
	Ctor       bool
	External   bool
	Unbound    bool
}

// Output is what the driver proved for one ref.
type Output struct {
	Edges   []Edge
	Files   int
	Cached  int
	Failed  int
	Version string
	Errs    []string
}

// IsInput reports a file the driver reads.
func IsInput(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".py", ".pyi":
		return true
	}
	switch path.Base(rel) {
	case "pyrightconfig.json", "pyproject.toml":
		return true
	}
	return false
}

// Run is not written yet.
func Run(ctx context.Context, in Input, cfg Config) (Output, error) { return Output{}, nil }

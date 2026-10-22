//go:build !cgo

package cxx

import (
	"errors"
	"io"
)

var errNoCgo = errors.New("this reponite was built without cgo: the C/C++ compiler driver is unavailable")

// Load reports the driver unavailable in a build without cgo.
func Load(string) error { return errNoCgo }

// Version is empty without the driver.
func Version() string { return "" }

// Parse is unavailable without cgo.
func Parse(string, string, []string) (Result, error) { return Result{}, errNoCgo }

// Serve is unavailable without cgo.
func Serve(io.Reader, io.Writer, string) error { return errNoCgo }

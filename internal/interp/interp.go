// Package interp implements the tree-walking interpreter for vent.
package interp

import (
	"errors"
	"io"
)

// ErrNotImplemented is returned while the interpreter is still a stub.
var ErrNotImplemented = errors.New("interpreter not implemented yet")

// Run executes the program in src, writing program output to out.
// filename is used only for diagnostics.
func Run(filename string, src []byte, out io.Writer) error {
	_, _, _ = filename, src, out
	return ErrNotImplemented
}

package interp

import (
	"io"

	"github.com/qnighy/vent/internal/syntax"
)

// Builtins are a stopgap: output will move to a capability passed to main.
func (in *Interp) defineBuiltins() {
	for _, b := range []*BuiltinValue{
		{Name: "print", Fn: builtinPrint("print", false)},
		{Name: "println", Fn: builtinPrint("println", true)},
	} {
		in.globals.define(b.Name, b, false)
	}
}

// builtinPrint implements print(x) and println(x). println() prints just a newline.
func builtinPrint(name string, newline bool) func(*Interp, syntax.Pos, []Value) (Value, error) {
	return func(in *Interp, pos syntax.Pos, args []Value) (Value, error) {
		var s string
		switch {
		case len(args) == 1:
			s = display(args[0])
		case len(args) == 0 && newline:
		default:
			return nil, errorf(pos, "%s expects 1 argument, got %d", name, len(args))
		}
		if newline {
			s += "\n"
		}
		if _, err := io.WriteString(in.out, s); err != nil {
			return nil, errorf(pos, "write error: %v", err)
		}
		return UnitValue{}, nil
	}
}

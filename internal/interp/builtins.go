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
		{Name: "panic", Fn: builtinPanic},
		{Name: "assert", Fn: builtinAssert},
		{Name: "assert_eq", Fn: builtinAssertEq},
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

// builtinPanic implements panic(message), which aborts the program.
func builtinPanic(in *Interp, pos syntax.Pos, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, errorf(pos, "panic expects 1 argument, got %d", len(args))
	}
	return nil, errorf(pos, "panic: %s", display(args[0]))
}

// builtinAssert implements assert(cond) and assert(cond, message).
func builtinAssert(in *Interp, pos syntax.Pos, args []Value) (Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, errorf(pos, "assert expects 1 or 2 arguments, got %d", len(args))
	}
	cond, ok := args[0].(BoolValue)
	if !ok {
		return nil, errorf(pos, "assert expects a Bool, got %s", args[0].TypeName())
	}
	if cond {
		return UnitValue{}, nil
	}
	if len(args) == 2 {
		return nil, errorf(pos, "assertion failed: %s", display(args[1]))
	}
	return nil, errorf(pos, "assertion failed")
}

// builtinAssertEq implements assert_eq(left, right).
func builtinAssertEq(in *Interp, pos syntax.Pos, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, errorf(pos, "assert_eq expects 2 arguments, got %d", len(args))
	}
	eq, err := equal(pos, args[0], args[1])
	if err != nil {
		return nil, err
	}
	if !eq {
		return nil, errorf(pos, "assertion failed: left == right\n  left: %s\n right: %s", debug(args[0]), debug(args[1]))
	}
	return UnitValue{}, nil
}

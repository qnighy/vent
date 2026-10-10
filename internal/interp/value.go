package interp

import (
	"strconv"

	"github.com/qnighy/vent/internal/syntax"
)

// Value is a runtime value.
type Value interface {
	// TypeName returns the user-facing name of the value's type.
	TypeName() string
}

type IntValue int64

type BoolValue bool

type StringValue string

type UnitValue struct{}

// FuncValue is a user-defined function.
type FuncValue struct {
	Decl *syntax.FnDecl
}

// BuiltinValue is a function implemented in Go.
type BuiltinValue struct {
	Name string
	Fn   func(in *Interp, pos syntax.Pos, args []Value) (Value, error)
}

func (IntValue) TypeName() string      { return "Int" }
func (BoolValue) TypeName() string     { return "Bool" }
func (StringValue) TypeName() string   { return "String" }
func (UnitValue) TypeName() string     { return "()" }
func (*FuncValue) TypeName() string    { return "fn" }
func (*BuiltinValue) TypeName() string { return "fn" }

// display formats v as print/println show it.
func display(v Value) string {
	switch v := v.(type) {
	case IntValue:
		return strconv.FormatInt(int64(v), 10)
	case BoolValue:
		return strconv.FormatBool(bool(v))
	case StringValue:
		return string(v)
	case UnitValue:
		return "()"
	case *FuncValue:
		return "<fn " + v.Decl.Name + ">"
	case *BuiltinValue:
		return "<builtin fn " + v.Name + ">"
	default:
		panic("unknown value type")
	}
}

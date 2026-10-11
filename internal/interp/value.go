package interp

import (
	"strconv"
	"strings"

	"github.com/qnighy/vent/internal/check"
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

// FuncValue is a user-defined function or method. Env binds the type
// parameters in scope of its body.
type FuncValue struct {
	Decl *syntax.FnDecl
	Env  check.Env
}

// VariantValue is the constructor of an enum variant with fields, which
// makes an EnumValue when called.
type VariantValue struct {
	Decl    *syntax.EnumDecl
	Variant int
}

// BuiltinValue is a function implemented in Go.
type BuiltinValue struct {
	Name string
	Fn   func(in *Interp, pos syntax.Pos, args []Value) (Value, error)
}

// ArrayValue is a growable array. See shared for its value semantics.
type ArrayValue struct {
	Elems  []Value
	shared bool
}

// StructValue is an instance of a user-defined struct. Fields are in declaration order.
type StructValue struct {
	Decl   *syntax.StructDecl
	Fields []Value
	shared bool
}

// EnumValue is a value of a user-defined enum. Fields are those of the
// variant. An EnumValue is never written in place, so it needs no copy on
// write; its fields are shared when matched out.
type EnumValue struct {
	Decl    *syntax.EnumDecl
	Variant int
	Fields  []Value
}

// Composite values have value semantics, implemented by copy-on-write.
//
// A composite may be referenced from several places. Whenever a reference is
// duplicated (a value is read out of a place to be stored elsewhere), the
// composite is marked shared. Before it is written through a place, a shared
// composite is replaced in that place by a fresh, unshared shallow copy whose
// children are marked shared in turn.
//
// The shared flag is sticky: it is never cleared even if the other references
// die, so this may copy more often than necessary. Precise clone points are
// left to the auto-clone analysis.

// share marks v as shared if it is a composite, and returns v.
func share(v Value) Value {
	switch v := v.(type) {
	case *ArrayValue:
		v.shared = true
	case *StructValue:
		v.shared = true
	}
	return v
}

// unshare returns a version of v that may be written in place, copying it if
// necessary. The caller must store the result back where v came from.
func unshare(v Value) Value {
	switch v := v.(type) {
	case *ArrayValue:
		if v.shared {
			elems := make([]Value, len(v.Elems))
			for i, e := range v.Elems {
				elems[i] = share(e)
			}
			return &ArrayValue{Elems: elems}
		}
	case *StructValue:
		if v.shared {
			fields := make([]Value, len(v.Fields))
			for i, f := range v.Fields {
				fields[i] = share(f)
			}
			return &StructValue{Decl: v.Decl, Fields: fields}
		}
	}
	return v
}

func (IntValue) TypeName() string       { return "Int" }
func (BoolValue) TypeName() string      { return "Bool" }
func (StringValue) TypeName() string    { return "String" }
func (UnitValue) TypeName() string      { return "()" }
func (*ArrayValue) TypeName() string    { return "Array" }
func (v *StructValue) TypeName() string { return v.Decl.Name }
func (v *EnumValue) TypeName() string   { return v.Decl.Name }
func (*FuncValue) TypeName() string     { return "fn" }
func (*VariantValue) TypeName() string  { return "fn" }
func (*BuiltinValue) TypeName() string  { return "fn" }

// display formats v as print/println show it.
// Strings are shown as is at the top level, and quoted within composites.
func display(v Value) string {
	if s, ok := v.(StringValue); ok {
		return string(s)
	}
	var sb strings.Builder
	writeValue(&sb, v)
	return sb.String()
}

// debug formats v as it appears within composites, with strings quoted.
func debug(v Value) string {
	var sb strings.Builder
	writeValue(&sb, v)
	return sb.String()
}

func writeValue(sb *strings.Builder, v Value) {
	switch v := v.(type) {
	case StringValue:
		writeQuoted(sb, string(v))
	case *ArrayValue:
		sb.WriteByte('[')
		for i, e := range v.Elems {
			if i > 0 {
				sb.WriteString(", ")
			}
			writeValue(sb, e)
		}
		sb.WriteByte(']')
	case *StructValue:
		sb.WriteString(v.Decl.Name)
		if len(v.Fields) == 0 {
			sb.WriteString(" {}")
			return
		}
		sb.WriteString(" { ")
		for i, f := range v.Fields {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(v.Decl.Fields[i].Name)
			sb.WriteString(": ")
			writeValue(sb, f)
		}
		sb.WriteString(" }")
	case *EnumValue:
		sb.WriteString(v.Decl.Name + "::" + v.Decl.Variants[v.Variant].Name)
		if len(v.Fields) == 0 {
			return
		}
		sb.WriteByte('(')
		for i, f := range v.Fields {
			if i > 0 {
				sb.WriteString(", ")
			}
			writeValue(sb, f)
		}
		sb.WriteByte(')')
	default:
		sb.WriteString(displayScalar(v))
	}
}

// writeQuoted writes s as a string literal that the lexer accepts.
func writeQuoted(sb *strings.Builder, s string) {
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case '\n':
			sb.WriteString(`\n`)
		case '\t':
			sb.WriteString(`\t`)
		case '\r':
			sb.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				sb.WriteString(`\u{` + strconv.FormatInt(int64(r), 16) + `}`)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
}

func displayScalar(v Value) string {
	switch v := v.(type) {
	case IntValue:
		return strconv.FormatInt(int64(v), 10)
	case BoolValue:
		return strconv.FormatBool(bool(v))
	case UnitValue:
		return "()"
	case *FuncValue:
		return "<fn " + v.Decl.Name + ">"
	case *BuiltinValue:
		return "<builtin fn " + v.Name + ">"
	case *VariantValue:
		return "<fn " + v.Decl.Name + "::" + v.Decl.Variants[v.Variant].Name + ">"
	default:
		panic("unknown value type")
	}
}

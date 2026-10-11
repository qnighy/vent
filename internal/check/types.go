package check

import (
	"strings"

	"github.com/qnighy/vent/internal/syntax"
)

// Type is a static type.
type Type interface {
	String() string
}

// Basic is a predeclared scalar type.
type Basic struct {
	name string
}

var (
	Int    = &Basic{"Int"}
	Bool   = &Basic{"Bool"}
	String = &Basic{"String"}
	Unit   = &Basic{"()"}
)

// NeverType is the type of expressions that never produce a value, such as return.
type NeverType struct{}

// InvalidType is the type of expressions that failed to check. It is
// compatible with every type so that one mistake is reported only once.
type InvalidType struct{}

var (
	Never   = &NeverType{}
	Invalid = &InvalidType{}
)

type ArrayType struct {
	Elem Type
}

// StructType is a nominal type. Two struct types are identical only if they
// are the same object.
type StructType struct {
	Decl   *syntax.StructDecl
	Fields []Type
}

type FuncType struct {
	Params []Type
	Result Type
}

// BuiltinType is the type of a builtin function. Calls to it are checked by
// rules specific to each builtin.
type BuiltinType struct {
	Name string
}

func (t *Basic) String() string       { return t.name }
func (*NeverType) String() string     { return "!" }
func (*InvalidType) String() string   { return "{invalid}" }
func (t *ArrayType) String() string   { return "Array<" + t.Elem.String() + ">" }
func (t *StructType) String() string  { return t.Decl.Name }
func (t *BuiltinType) String() string { return "builtin " + t.Name }

func (t *FuncType) String() string {
	var sb strings.Builder
	sb.WriteString("fn(")
	for i, p := range t.Params {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(p.String())
	}
	sb.WriteString(")")
	if t.Result != Unit {
		sb.WriteString(" -> " + t.Result.String())
	}
	return sb.String()
}

func (t *StructType) field(name string) (Type, bool) {
	for i, f := range t.Decl.Fields {
		if f.Name == name {
			return t.Fields[i], true
		}
	}
	return nil, false
}

// identical reports whether a and b are the same type.
func identical(a, b Type) bool {
	switch a := a.(type) {
	case *ArrayType:
		b, ok := b.(*ArrayType)
		return ok && identical(a.Elem, b.Elem)
	case *FuncType:
		b, ok := b.(*FuncType)
		if !ok || len(a.Params) != len(b.Params) || !identical(a.Result, b.Result) {
			return false
		}
		for i := range a.Params {
			if !identical(a.Params[i], b.Params[i]) {
				return false
			}
		}
		return true
	case *BuiltinType:
		b, ok := b.(*BuiltinType)
		return ok && a.Name == b.Name
	default:
		return a == b
	}
}

// assignable reports whether a value of type src can be used where dst is expected.
func assignable(src, dst Type) bool {
	if isInvalid(src) || isInvalid(dst) || src == Never {
		return true
	}
	return identical(src, dst)
}

func isInvalid(t Type) bool {
	return t == Invalid
}

// comparable reports whether == is defined on t: it is structural equality,
// which functions do not have.
func comparable(t Type) bool {
	return comparableRec(t, map[*StructType]bool{})
}

// comparableRec is comparable for possibly recursive types. A struct already
// being visited is assumed to be comparable.
func comparableRec(t Type, visiting map[*StructType]bool) bool {
	switch t := t.(type) {
	case *Basic, *InvalidType:
		return true
	case *ArrayType:
		return comparableRec(t.Elem, visiting)
	case *StructType:
		if visiting[t] {
			return true
		}
		visiting[t] = true
		for _, f := range t.Fields {
			if !comparableRec(f, visiting) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

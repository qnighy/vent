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

// NeverType is the type of expressions that never produce a value, such as
// return. It unifies with every type without determining it.
type NeverType struct{}

// InvalidType is the type of expressions that failed to check. It unifies
// with every type without determining it, so that one mistake is reported
// only once.
type InvalidType struct{}

var (
	Never   = &NeverType{}
	Invalid = &InvalidType{}
)

// TypeVar is a type yet to be determined by unification.
type TypeVar struct {
	// ref is the type the variable is bound to, or nil if unbound.
	ref Type
}

// TypeParam is a type parameter. Within its scope it is a rigid type,
// identical only to itself, about which nothing is known but its bounds.
// Outside, it is replaced by a type argument.
type TypeParam struct {
	Name   string
	Bounds []*Trait
}

type ArrayType struct {
	Elem Type
}

// StructDef is a struct declaration. Field types refer to Params.
type StructDef struct {
	Decl   *syntax.StructDecl
	Params []*TypeParam
	Fields []Type
}

// EnumDef is an enum declaration. Field types refer to Params.
type EnumDef struct {
	Decl     *syntax.EnumDecl
	Params   []*TypeParam
	Variants [][]Type
}

// StructType is a struct applied to type arguments. Struct types are nominal:
// two are identical only if they have the same definition and arguments.
type StructType struct {
	Def  *StructDef
	Args []Type
}

// EnumType is an enum applied to type arguments. Enum types are nominal.
type EnumType struct {
	Def  *EnumDef
	Args []Type
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
func (t *TypeParam) String() string   { return t.Name }
func (t *ArrayType) String() string   { return "Array<" + t.Elem.String() + ">" }
func (t *StructType) String() string  { return applied(t.Def.Decl.Name, t.Args) }
func (t *EnumType) String() string    { return applied(t.Def.Decl.Name, t.Args) }
func (t *BuiltinType) String() string { return "builtin " + t.Name }

func applied(name string, args []Type) string {
	if len(args) == 0 {
		return name
	}
	var sb strings.Builder
	sb.WriteString(name + "<")
	for i, a := range args {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(a.String())
	}
	sb.WriteString(">")
	return sb.String()
}

// String shows an unbound variable as _.
func (t *TypeVar) String() string {
	if t.ref == nil {
		return "_"
	}
	return t.ref.String()
}

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
	if prune(t.Result) != Unit {
		sb.WriteString(" -> " + t.Result.String())
	}
	return sb.String()
}

// field returns the type of the named field.
func (t *StructType) field(name string) (Type, bool) {
	for i, f := range t.Def.Decl.Fields {
		if f.Name == name {
			return subst(t.Def.Fields[i], bindParams(t.Def.Params, t.Args)), true
		}
	}
	return nil, false
}

// variant returns the index and field types of the named variant.
func (t *EnumType) variant(name string) (int, []Type, bool) {
	for i, v := range t.Def.Decl.Variants {
		if v.Name == name {
			return i, t.variantFields(i), true
		}
	}
	return 0, nil, false
}

func (t *EnumType) variantFields(i int) []Type {
	env := bindParams(t.Def.Params, t.Args)
	var fields []Type
	for _, f := range t.Def.Variants[i] {
		fields = append(fields, subst(f, env))
	}
	return fields
}

// Trait is a trait: a set of method signatures, which refer to Self.
type Trait struct {
	Name string
	Decl *syntax.TraitDecl // nil for builtin traits
	Self *TypeParam
	// Methods lists the methods in declaration order.
	Methods []*Func
}

func (t *Trait) method(name string) *Func {
	for _, m := range t.Methods {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// Eq is the builtin trait of the types that == is defined on. It has no
// methods and is implemented by the language, never by impls.
var Eq = &Trait{Name: "Eq"}

func init() {
	Eq.Self = &TypeParam{Name: "Self", Bounds: []*Trait{Eq}}
}

// Impl is an impl block: inherent methods of a type (Trait is nil), or an
// implementation of Trait for the types that Self matches.
type Impl struct {
	Decl   *syntax.ImplDecl
	Params []*TypeParam
	Trait  *Trait
	Self   Type
	// Methods lists the methods in declaration order.
	Methods []*Func
}

func (impl *Impl) method(name string) *Func {
	for _, m := range impl.Methods {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// Func is the signature of a function or method.
type Func struct {
	Name string
	Decl *syntax.FnDecl
	// Impl is the impl block that declares the method, if any.
	Impl *Impl
	// Trait is the trait that declares the method, if any.
	Trait *Trait
	// TypeParams are the function's own type parameters. For a method,
	// those of the impl come first in its type arguments.
	TypeParams []*TypeParam
	// Params and Result may refer to the type parameters of the function,
	// of its impl, and to Self of its trait.
	Params []Type
	Result Type
	// HasSelf is set if the first parameter is the receiver self.
	HasSelf bool

	// Scope of the body.
	scope map[string]*TypeParam
	self  Type
}

// AllTypeParams lists the type parameters that type arguments of a reference
// to f instantiate: those of its inherent impl, then its own.
func (f *Func) AllTypeParams() []*TypeParam {
	if f.Impl != nil && f.Impl.Trait == nil {
		return append(append([]*TypeParam(nil), f.Impl.Params...), f.TypeParams...)
	}
	return f.TypeParams
}

func (f *Func) typ() *FuncType {
	return &FuncType{Params: f.Params, Result: f.Result}
}

func isInvalid(t Type) bool {
	return prune(t) == Invalid
}

// comparable reports whether == may be defined on t: it is structural
// equality, which functions do not have. Undetermined parts are assumed to be
// comparable. A type parameter is comparable if it is bounded by Eq.
func comparable(t Type) bool {
	return comparableRec(t, map[string]bool{})
}

// maxComparableTypes bounds the number of types comparable visits.
const maxComparableTypes = 1000

// comparableRec is comparable for possibly recursive types. A type already
// being visited is assumed to be comparable.
func comparableRec(t Type, visiting map[string]bool) bool {
	if len(visiting) > maxComparableTypes {
		// A polymorphically recursive type, such as enum T<A> { X(T<Array<A>>) },
		// has infinitely many component types.
		return true
	}
	switch t := prune(t).(type) {
	case *Basic, *InvalidType, *NeverType, *TypeVar:
		return true
	case *TypeParam:
		return hasBound(t, Eq)
	case *ArrayType:
		return comparableRec(t.Elem, visiting)
	case *StructType:
		if visiting[t.String()] {
			return true
		}
		visiting[t.String()] = true
		for _, f := range t.Def.Decl.Fields {
			ft, _ := t.field(f.Name)
			if !comparableRec(ft, visiting) {
				return false
			}
		}
		return true
	case *EnumType:
		if visiting[t.String()] {
			return true
		}
		visiting[t.String()] = true
		for i := range t.Def.Variants {
			for _, f := range t.variantFields(i) {
				if !comparableRec(f, visiting) {
					return false
				}
			}
		}
		return true
	default:
		return false
	}
}

func hasBound(p *TypeParam, trait *Trait) bool {
	for _, b := range p.Bounds {
		if b == trait {
			return true
		}
	}
	return false
}

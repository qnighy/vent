// Package check implements the static checker for vent.
//
// Checking is local: every function body is checked on its own against the
// signatures of the top-level declarations.
//
// Generic functions are checked once, with their type parameters as rigid
// types. Which implementation a trait method call runs may depend on the
// type arguments of the function containing it, so the checker records what
// each name refers to (see Ref) and the interpreter resolves it with the
// type arguments of the running function.
//
// Types are inferred bottom-up and solved by unification, one constraint at a
// time in check order, which is the lexical order of the source. The node
// whose constraint first fails is the one reported. Where the meaning of an
// operation depends on a type, as + does on Int and String, or a trait
// method call does on the type implementing the trait, the type must be
// determined (as far as needed) when the checker reaches the operation:
// constraints found later cannot change it. For a call, "reaching" means
// having checked the call with its arguments and its expected type, such as
// that of a let with a type annotation.
//
// A program with static errors can still run. Each node that fails to check
// is "poisoned": evaluating it aborts with its error instead of evaluating it
// (or any of its subexpressions). Errors in top-level declarations that make
// the program meaningless as a whole, such as duplicate definitions, are fatal.
package check

import (
	"fmt"
	"sort"

	"github.com/qnighy/vent/internal/syntax"
)

// Error is a static error.
type Error struct {
	Pos syntax.Pos
	Msg string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v: static error: %s", e.Pos, e.Msg)
}

// Info is the result of checking a program.
type Info struct {
	// Errors lists every static error, including fatal ones, in source order.
	Errors []*Error
	// Fatal lists the errors that prevent the program from running at all.
	Fatal []*Error
	// Poisoned maps each node that failed to check to its error. Keys are
	// syntax.Expr, *syntax.LetStmt, *syntax.MatchArm, *syntax.FnDecl,
	// *syntax.StructDecl, *syntax.EnumDecl and *syntax.ImplDecl.
	Poisoned map[any]*Error
	// Types records the type of each expression that checked.
	Types map[syntax.Expr]Type
	// Refs records what each reference to a function (*syntax.IdentExpr),
	// path (*syntax.PathExpr), method call (*syntax.MethodCallExpr) and
	// variant pattern (*syntax.VariantPat) that checked refers to.
	Refs map[any]*Ref
	// Structs records the struct that each struct literal (*syntax.StructLit)
	// and struct pattern (*syntax.StructPat) that checked names.
	Structs map[any]*StructDef
	// Funcs maps each function and method declaration to its signature.
	Funcs map[*syntax.FnDecl]*Func

	// impls lists the impls of each trait.
	impls map[*Trait][]*Impl
}

// RefKind is the kind of a Ref.
type RefKind int

const (
	// RefFunc is a function or an inherent method, Func. TypeArgs
	// instantiate Func.AllTypeParams().
	RefFunc RefKind = iota
	// RefTraitMethod is the method Func of Trait, implemented for Self.
	// TypeArgs instantiate the method's own type parameters. The
	// implementation is chosen when it runs; see Info.Resolve.
	RefTraitMethod
	// RefVariant is the variant Variant of Enum.
	RefVariant
	// RefArrayMethod is a builtin method of Array.
	RefArrayMethod
)

// Ref is what a name refers to. Its types may refer to the type parameters
// in scope where the name appears.
type Ref struct {
	Kind     RefKind
	Func     *Func
	Trait    *Trait
	Self     Type
	Enum     *EnumDef
	Variant  int
	TypeArgs []Type
}

// Builtins lists the names of the builtin functions.
var Builtins = []string{"print", "println", "panic", "assert", "assert_eq"}

// builtinTypes lists the names of the builtin types and traits.
var builtinTypes = []string{"Int", "Bool", "String", "Array", "Self", "Eq"}

// Check checks prog.
func Check(prog *syntax.Program) *Info {
	c := &checker{
		info: &Info{
			Poisoned: map[any]*Error{},
			Types:    map[syntax.Expr]Type{},
			Refs:     map[any]*Ref{},
			Structs:  map[any]*StructDef{},
			Funcs:    map[*syntax.FnDecl]*Func{},
			impls:    map[*Trait][]*Impl{},
		},
		structs:  map[string]*StructDef{},
		enums:    map[string]*EnumDef{},
		traits:   map[string]*Trait{},
		funcs:    map[string]*Func{},
		inherent: map[any][]*Func{},
	}
	c.declare(prog)
	for _, f := range c.bodies {
		c.checkFunc(f)
	}
	for e, t := range c.info.Types {
		c.info.Types[e] = zonk(t)
	}
	for _, ref := range c.info.Refs {
		if ref.Self != nil {
			ref.Self = zonk(ref.Self)
		}
		ref.TypeArgs = zonkAll(ref.TypeArgs)
	}
	sort.SliceStable(c.info.Errors, func(i, j int) bool {
		a, b := c.info.Errors[i].Pos, c.info.Errors[j].Pos
		return a.Line < b.Line || (a.Line == b.Line && a.Col < b.Col)
	})
	return c.info
}

type checker struct {
	info    *Info
	structs map[string]*StructDef
	enums   map[string]*EnumDef
	traits  map[string]*Trait
	funcs   map[string]*Func
	// traitOrder lists the traits in declaration order.
	traitOrder []*Trait
	// inherent lists the inherent methods of each *StructDef and *EnumDef.
	inherent map[any][]*Func
	// bodies lists the functions whose bodies are checked.
	bodies []*Func

	// Type names in scope: type parameters and Self.
	tscope map[string]*TypeParam
	self   Type

	// State for the function being checked.
	result   Type
	scope    *scope
	loops    []*loopCtx
	deferred []deferred
	// pending lists the trait obligations yet to be proven.
	pending []obligation
}

type variable struct {
	typ     Type
	mutable bool
}

type scope struct {
	parent *scope
	vars   map[string]*variable
}

func (s *scope) lookup(name string) *variable {
	for ; s != nil; s = s.parent {
		if v, ok := s.vars[name]; ok {
			return v
		}
	}
	return nil
}

type loopCtx struct {
	// isLoop is true for `loop`, whose breaks determine its type.
	isLoop bool
	// result is the type of the loop, set by its first break.
	result Type
}

// deferred is a check of a constraint that does not affect the meaning of
// the program, made once the function has been checked.
type deferred func()

func (c *checker) addError(pos syntax.Pos, format string, args ...any) *Error {
	err := &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
	c.info.Errors = append(c.info.Errors, err)
	return err
}

func (c *checker) fatal(pos syntax.Pos, format string, args ...any) {
	c.info.Fatal = append(c.info.Fatal, c.addError(pos, format, args...))
}

// poison records an error on node and returns Invalid.
// Evaluating node aborts with the first error recorded on it.
func (c *checker) poison(node any, pos syntax.Pos, format string, args ...any) Type {
	err := c.addError(pos, format, args...)
	if _, ok := c.info.Poisoned[node]; !ok {
		c.info.Poisoned[node] = err
	}
	return Invalid
}

// CheckFile parses and checks src. It returns every error in source order,
// each prefixed with filename.
func CheckFile(filename string, src []byte) []error {
	prog, err := syntax.Parse(src)
	if err != nil {
		return []error{fmt.Errorf("%s:%w", filename, err)}
	}
	var errs []error
	for _, err := range Check(prog).Errors {
		errs = append(errs, fmt.Errorf("%s:%w", filename, err))
	}
	return errs
}

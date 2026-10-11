// Package check implements the static checker for vent.
//
// Checking is local: every function body is checked on its own against the
// signatures of the top-level declarations.
//
// Types are inferred bottom-up and solved by unification, one constraint at a
// time in check order, which is the lexical order of the source. The node
// whose constraint first fails is the one reported. Where the meaning of an
// operation depends on a type, as + does on Int and String, the type must be
// determined (as far as needed) when the checker reaches the operation:
// constraints found later cannot change it.
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
	// syntax.Expr, *syntax.LetStmt, *syntax.FnDecl and *syntax.StructDecl.
	Poisoned map[any]*Error
	// Types records the type of each expression that checked.
	Types map[syntax.Expr]Type
}

// Builtins lists the names of the builtin functions.
var Builtins = []string{"print", "println", "panic", "assert", "assert_eq"}

// Check checks prog.
func Check(prog *syntax.Program) *Info {
	c := &checker{
		info: &Info{
			Poisoned: map[any]*Error{},
			Types:    map[syntax.Expr]Type{},
		},
		structs: map[string]*StructType{},
		funcs:   map[string]*FuncType{},
	}
	c.declare(prog)
	for _, fn := range prog.Fns {
		if c.funcDecls[fn.Name] == fn {
			c.checkFunc(fn)
		}
	}
	for e, t := range c.info.Types {
		c.info.Types[e] = zonk(t)
	}
	sort.SliceStable(c.info.Errors, func(i, j int) bool {
		a, b := c.info.Errors[i].Pos, c.info.Errors[j].Pos
		return a.Line < b.Line || (a.Line == b.Line && a.Col < b.Col)
	})
	return c.info
}

type checker struct {
	info      *Info
	structs   map[string]*StructType
	funcs     map[string]*FuncType
	funcDecls map[string]*syntax.FnDecl

	// State for the function being checked.
	result   Type
	scope    *scope
	loops    []*loopCtx
	deferred []deferred
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

// declare collects the top-level declarations and resolves their signatures.
func (c *checker) declare(prog *syntax.Program) {
	builtin := map[string]bool{}
	for _, name := range Builtins {
		builtin[name] = true
	}
	// Functions and structs share a namespace for now.
	defined := func(name string) bool {
		return builtin[name] || c.structs[name] != nil || c.funcDecls[name] != nil
	}
	c.funcDecls = map[string]*syntax.FnDecl{}

	for _, st := range prog.Structs {
		if defined(st.Name) {
			c.fatal(st.Pos, "duplicate definition of %s", st.Name)
			continue
		}
		seen := map[string]bool{}
		for _, f := range st.Fields {
			if seen[f.Name] {
				c.fatal(f.Pos, "duplicate field %s in %s", f.Name, st.Name)
			}
			seen[f.Name] = true
		}
		c.structs[st.Name] = &StructType{Decl: st}
	}
	for _, fn := range prog.Fns {
		if defined(fn.Name) {
			c.fatal(fn.Pos, "duplicate definition of %s", fn.Name)
			continue
		}
		c.funcDecls[fn.Name] = fn
	}

	// Resolve signatures once all names are known.
	for _, st := range prog.Structs {
		t := c.structs[st.Name]
		if t == nil || t.Decl != st {
			continue
		}
		for _, f := range st.Fields {
			t.Fields = append(t.Fields, c.resolveType(st, f.Type))
		}
	}
	for _, fn := range prog.Fns {
		if c.funcDecls[fn.Name] != fn {
			continue
		}
		sig := &FuncType{Result: Unit}
		for _, param := range fn.Params {
			if param.Type == nil {
				c.poison(fn, param.Pos, "missing type annotation for parameter %s", param.Name)
				sig.Params = append(sig.Params, Invalid)
				continue
			}
			sig.Params = append(sig.Params, c.resolveType(fn, param.Type))
		}
		if fn.Result != nil {
			sig.Result = c.resolveType(fn, fn.Result)
		}
		c.funcs[fn.Name] = sig
	}
}

// resolveType resolves a type annotation, poisoning owner on failure.
func (c *checker) resolveType(owner any, t syntax.TypeExpr) Type {
	switch t := t.(type) {
	case *syntax.UnitType:
		return Unit
	case *syntax.FuncType:
		ft := &FuncType{Result: Unit}
		valid := true
		for _, p := range t.Params {
			pt := c.resolveType(owner, p)
			valid = valid && !isInvalid(pt)
			ft.Params = append(ft.Params, pt)
		}
		if t.Result != nil {
			ft.Result = c.resolveType(owner, t.Result)
			valid = valid && !isInvalid(ft.Result)
		}
		if !valid {
			return Invalid
		}
		return ft
	case *syntax.NamedType:
		if t.Name == "Array" {
			if len(t.Args) != 1 {
				return c.poison(owner, t.Pos, "Array expects 1 type argument, got %d", len(t.Args))
			}
			elem := c.resolveType(owner, t.Args[0])
			if isInvalid(elem) {
				return Invalid
			}
			return &ArrayType{Elem: elem}
		}
		var typ Type
		switch t.Name {
		case "Int":
			typ = Int
		case "Bool":
			typ = Bool
		case "String":
			typ = String
		default:
			st := c.structs[t.Name]
			if st == nil {
				return c.poison(owner, t.Pos, "undefined type %s", t.Name)
			}
			typ = st
		}
		if len(t.Args) != 0 {
			return c.poison(owner, t.Pos, "%s does not take type arguments", t.Name)
		}
		return typ
	default:
		panic(fmt.Sprintf("unknown type expression %T", t))
	}
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

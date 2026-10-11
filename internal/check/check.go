// Package check implements the static checker for vent.
//
// Checking is local: every function body is checked on its own against the
// signatures of the top-level declarations. Types flow forward: a local
// variable takes the type of its initializer or annotation, and expected types
// are passed down into subexpressions, never inferred from later uses.
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
	result Type
	scope  *scope
	loops  []*loopCtx
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
	// want is the required type of the loop, if any.
	want Type
	// hint is the expected type of the loop, used only to guide inference.
	hint Type
	// breakType is the type of the first break, if any.
	breakType Type
}

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

func (c *checker) checkFunc(fn *syntax.FnDecl) {
	sig := c.funcs[fn.Name]
	c.result = sig.Result
	c.scope = &scope{vars: map[string]*variable{}}
	c.loops = nil
	for i, param := range fn.Params {
		c.scope.vars[param.Name] = &variable{typ: sig.Params[i]}
	}
	c.checkBlock(fn.Body, sig.Result, sig.Result)
	c.scope = nil
}

// lookup resolves a name used as a value.
func (c *checker) lookup(name string) (Type, *variable) {
	if v := c.scope.lookup(name); v != nil {
		return v.typ, v
	}
	if f := c.funcs[name]; f != nil {
		return f, nil
	}
	for _, b := range Builtins {
		if b == name {
			return &BuiltinType{Name: name}, nil
		}
	}
	return nil, nil
}

// checkAgainst checks that e has a type assignable to want. Blocks and
// branches pass want down so that mismatches are reported where they occur.
func (c *checker) checkAgainst(e syntax.Expr, want Type) Type {
	switch e := e.(type) {
	case *syntax.BlockExpr:
		return c.checkBlock(e, want, want)
	case *syntax.IfExpr:
		return c.checkIf(e, want, want)
	case *syntax.LoopExpr:
		return c.checkLoop(e, want, want)
	}
	t := c.infer(e, want)
	if !assignable(t, want) {
		return c.poison(e, e.Position(), "mismatched types: expected %v, found %v", want, t)
	}
	return t
}

// infer returns the type of e. hint is the expected type, if known; it only
// guides inference (as for empty arrays) and is not enforced.
func (c *checker) infer(e syntax.Expr, hint Type) Type {
	t := c.infer1(e, hint)
	if !isInvalid(t) {
		c.info.Types[e] = t
	}
	return t
}

func (c *checker) infer1(e syntax.Expr, hint Type) Type {
	switch e := e.(type) {
	case *syntax.IntLit:
		return Int
	case *syntax.StringLit:
		return String
	case *syntax.BoolLit:
		return Bool
	case *syntax.UnitLit:
		return Unit
	case *syntax.IdentExpr:
		t, _ := c.lookup(e.Name)
		if t == nil {
			return c.poison(e, e.Pos, "undefined variable %s", e.Name)
		}
		return t
	case *syntax.UnaryExpr:
		x := c.infer(e.X, nil)
		switch {
		case isInvalid(x) || x == Never:
			return x
		case e.Op == syntax.Minus && x == Int:
			return Int
		case e.Op == syntax.Bang && x == Bool:
			return Bool
		}
		return c.poison(e, e.Pos, "invalid operand for unary %v: %v", e.Op, x)
	case *syntax.BinaryExpr:
		x := c.infer(e.X, nil)
		y := c.infer(e.Y, x)
		return c.binaryOp(e, e.Op, x, y)
	case *syntax.AssignExpr:
		return c.checkAssign(e)
	case *syntax.CallExpr:
		return c.checkCall(e)
	case *syntax.ArrayLit:
		return c.checkArrayLit(e, hint)
	case *syntax.StructLit:
		return c.checkStructLit(e)
	case *syntax.IndexExpr:
		x := c.infer(e.X, nil)
		c.checkAgainst(e.Index, Int)
		switch x := x.(type) {
		case *ArrayType:
			return x.Elem
		case *InvalidType, *NeverType:
			return x
		}
		return c.poison(e, e.Pos, "cannot index into a value of type %v", x)
	case *syntax.FieldExpr:
		x := c.infer(e.X, nil)
		switch x := x.(type) {
		case *StructType:
			if f, ok := x.field(e.Name); ok {
				return f
			}
			return c.poison(e, e.Pos, "no field %s on %v", e.Name, x)
		case *InvalidType, *NeverType:
			return x
		}
		return c.poison(e, e.Pos, "cannot access field %s of %v", e.Name, x)
	case *syntax.MethodCallExpr:
		return c.checkMethodCall(e)
	case *syntax.BlockExpr:
		return c.checkBlock(e, hint, nil)
	case *syntax.IfExpr:
		return c.checkIf(e, hint, nil)
	case *syntax.WhileExpr:
		c.checkAgainst(e.Cond, Bool)
		c.loops = append(c.loops, &loopCtx{})
		c.checkBlock(e.Body, Unit, Unit)
		c.loops = c.loops[:len(c.loops)-1]
		return Unit
	case *syntax.LoopExpr:
		return c.checkLoop(e, hint, nil)
	case *syntax.BreakExpr:
		c.checkBreak(e)
		return Never
	case *syntax.ContinueExpr:
		return Never
	case *syntax.ReturnExpr:
		if e.Value == nil {
			if !assignable(Unit, c.result) {
				c.poison(e, e.Pos, "mismatched types: expected %v, found ()", c.result)
			}
		} else {
			c.checkAgainst(e.Value, c.result)
		}
		return Never
	default:
		panic(fmt.Sprintf("unknown expression %T", e))
	}
}

// checkBlock checks a block. If want is non-nil, the block must have a type assignable to it.
func (c *checker) checkBlock(b *syntax.BlockExpr, hint, want Type) Type {
	c.scope = &scope{parent: c.scope, vars: map[string]*variable{}}
	defer func() { c.scope = c.scope.parent }()

	diverges := false
	for _, stmt := range b.Stmts {
		switch stmt := stmt.(type) {
		case *syntax.LetStmt:
			var t Type
			if stmt.Type != nil {
				t = c.resolveType(stmt, stmt.Type)
				c.checkAgainst(stmt.Value, t)
			} else {
				t = c.infer(stmt.Value, nil)
			}
			diverges = diverges || t == Never
			c.scope.vars[stmt.Name] = &variable{typ: t, mutable: stmt.Mutable}
		case *syntax.ExprStmt:
			if c.infer(stmt.X, nil) == Never {
				diverges = true
			}
		default:
			panic(fmt.Sprintf("unknown statement %T", stmt))
		}
	}

	if b.Tail != nil {
		if want != nil {
			return c.checkAgainst(b.Tail, want)
		}
		return c.infer(b.Tail, hint)
	}
	t := Type(Unit)
	if diverges {
		t = Never
	}
	if want != nil && !assignable(t, want) {
		return c.poison(b, b.Pos, "mismatched types: expected %v, found ()", want)
	}
	return t
}

func (c *checker) checkIf(e *syntax.IfExpr, hint, want Type) Type {
	c.checkAgainst(e.Cond, Bool)
	if e.Else == nil {
		c.checkBlock(e.Then, Unit, Unit)
		if want != nil && !assignable(Unit, want) {
			return c.poison(e, e.Pos, "mismatched types: expected %v, found () (if without else)", want)
		}
		return Unit
	}
	if want != nil {
		c.checkBlock(e.Then, want, want)
		c.checkAgainst(e.Else, want)
		return want
	}
	// The then branch determines the type unless it diverges.
	t := c.checkBlock(e.Then, hint, nil)
	if t == Never {
		return c.infer(e.Else, hint)
	}
	c.checkAgainst(e.Else, t)
	return t
}

func (c *checker) checkLoop(e *syntax.LoopExpr, hint, want Type) Type {
	ctx := &loopCtx{isLoop: true, want: want, hint: hint}
	c.loops = append(c.loops, ctx)
	c.checkBlock(e.Body, Unit, Unit)
	c.loops = c.loops[:len(c.loops)-1]
	if ctx.breakType == nil {
		return Never
	}
	return ctx.breakType
}

func (c *checker) checkBreak(e *syntax.BreakExpr) {
	ctx := c.loops[len(c.loops)-1]
	if !ctx.isLoop {
		return // break in while carries no value; the parser checks that.
	}
	// The type of a loop is its required type, or else the type of its first break.
	want := ctx.want
	if want == nil {
		want = ctx.breakType
	}
	var t Type
	switch {
	case e.Value == nil:
		t = Unit
		if want != nil && !assignable(Unit, want) {
			c.poison(e, e.Pos, "mismatched types: expected %v, found ()", want)
		}
	case want != nil:
		t = c.checkAgainst(e.Value, want)
	default:
		t = c.infer(e.Value, ctx.hint)
	}
	if ctx.breakType == nil && t != Never {
		ctx.breakType = t
	}
}

// binaryOp checks a binary operation on operands of types x and y.
// node is poisoned on failure.
func (c *checker) binaryOp(node syntax.Expr, op syntax.TokenKind, x, y Type) Type {
	if isInvalid(x) || isInvalid(y) {
		return Invalid
	}
	if x == Never || y == Never {
		return Never
	}
	switch op {
	case syntax.AndAnd, syntax.OrOr:
		if x == Bool && y == Bool {
			return Bool
		}
		return c.poison(node, node.Position(), "operands of %v must be Bool, got %v and %v", op, x, y)
	case syntax.Eq, syntax.Ne:
		if identical(x, y) && comparable(x) {
			return Bool
		}
		return c.poison(node, node.Position(), "cannot compare %v and %v", x, y)
	case syntax.Lt, syntax.Le, syntax.Gt, syntax.Ge:
		if x == y && (x == Int || x == String) {
			return Bool
		}
	case syntax.Plus:
		if x == y && (x == Int || x == String) {
			return x
		}
	case syntax.Minus, syntax.Star, syntax.Slash, syntax.Percent:
		if x == Int && y == Int {
			return Int
		}
	}
	return c.poison(node, node.Position(), "invalid operands for %v: %v and %v", op, x, y)
}

var compoundOps = map[syntax.TokenKind]syntax.TokenKind{
	syntax.PlusAssign:    syntax.Plus,
	syntax.MinusAssign:   syntax.Minus,
	syntax.StarAssign:    syntax.Star,
	syntax.SlashAssign:   syntax.Slash,
	syntax.PercentAssign: syntax.Percent,
}

func (c *checker) checkAssign(e *syntax.AssignExpr) Type {
	target := c.checkPlace(e, e.Target, "assign to")
	if op, ok := compoundOps[e.Op]; ok {
		v := c.infer(e.Value, target)
		r := c.binaryOp(e, op, target, v)
		if !assignable(r, target) {
			c.poison(e, e.Pos, "mismatched types: expected %v, found %v", target, r)
		}
	} else {
		c.checkAgainst(e.Value, target)
	}
	return Unit
}

// checkPlace checks a place that node writes to and returns its type.
// verb describes the write, such as "assign to".
func (c *checker) checkPlace(node syntax.Expr, place syntax.Expr, verb string) Type {
	t := c.infer(place, nil)
	root := place
	for {
		switch r := root.(type) {
		case *syntax.IndexExpr:
			root = r.X
			continue
		case *syntax.FieldExpr:
			root = r.X
			continue
		}
		break
	}
	ident, ok := root.(*syntax.IdentExpr)
	if !ok {
		return t // A temporary may be written, to no lasting effect.
	}
	rt, v := c.lookup(ident.Name)
	switch {
	case rt == nil:
		// Already reported as undefined.
	case v == nil:
		c.poison(node, node.Position(), "cannot %s function %s", verb, ident.Name)
	case !v.mutable:
		c.poison(node, node.Position(), "cannot %s immutable variable %s", verb, ident.Name)
	}
	return t
}

func (c *checker) checkArgs(args []syntax.Expr, params []Type) {
	for i, arg := range args {
		if i < len(params) {
			c.checkAgainst(arg, params[i])
		} else {
			c.infer(arg, nil)
		}
	}
}

func (c *checker) checkCall(e *syntax.CallExpr) Type {
	callee := c.infer(e.Fn, nil)
	switch ft := callee.(type) {
	case *FuncType:
		c.checkArgs(e.Args, ft.Params)
		if len(e.Args) != len(ft.Params) {
			name := "function"
			if id, ok := e.Fn.(*syntax.IdentExpr); ok {
				name = id.Name
			}
			return c.poison(e, e.Pos, "%s expects %d arguments, got %d", name, len(ft.Params), len(e.Args))
		}
		return ft.Result
	case *BuiltinType:
		return c.checkBuiltinCall(e, ft.Name)
	case *InvalidType, *NeverType:
		c.checkArgs(e.Args, nil)
		return ft
	default:
		c.checkArgs(e.Args, nil)
		return c.poison(e, e.Pos, "cannot call a value of type %v", callee)
	}
}

func (c *checker) checkBuiltinCall(e *syntax.CallExpr, name string) Type {
	arity := func(min, max int) bool {
		if len(e.Args) < min || len(e.Args) > max {
			want := fmt.Sprint(min)
			if min != max {
				want = fmt.Sprintf("%d or %d", min, max)
			}
			c.poison(e, e.Pos, "%s expects %s arguments, got %d", name, want, len(e.Args))
			return false
		}
		return true
	}
	switch name {
	case "print", "println":
		c.checkArgs(e.Args, nil)
		min := 1
		if name == "println" {
			min = 0
		}
		if !arity(min, 1) {
			return Invalid
		}
		return Unit
	case "panic":
		c.checkArgs(e.Args, nil)
		if !arity(1, 1) {
			return Invalid
		}
		return Never
	case "assert":
		c.checkArgs(e.Args, []Type{Bool})
		if !arity(1, 2) {
			return Invalid
		}
		return Unit
	case "assert_eq":
		var types []Type
		var prev Type
		for _, arg := range e.Args {
			prev = c.infer(arg, prev)
			types = append(types, prev)
		}
		if !arity(2, 2) {
			return Invalid
		}
		x, y := types[0], types[1]
		if !isInvalid(x) && !isInvalid(y) && x != Never && y != Never && !(identical(x, y) && comparable(x)) {
			return c.poison(e, e.Pos, "cannot compare %v and %v", x, y)
		}
		return Unit
	default:
		panic("unknown builtin " + name)
	}
}

func (c *checker) checkMethodCall(e *syntax.MethodCallExpr) Type {
	recv := c.infer(e.Recv, nil)
	var params []Type
	var result Type
	mutates := false
	switch rt := recv.(type) {
	case *ArrayType:
		switch e.Name {
		case "len":
			result = Int
		case "push":
			params, result, mutates = []Type{rt.Elem}, Unit, true
		case "pop":
			result, mutates = rt.Elem, true
		}
	case *InvalidType, *NeverType:
		c.checkArgs(e.Args, nil)
		return rt
	}
	if result == nil {
		c.checkArgs(e.Args, nil)
		return c.poison(e, e.Pos, "no method %s on %v", e.Name, recv)
	}
	if mutates {
		c.checkPlace(e, e.Recv, "mutate")
	}
	c.checkArgs(e.Args, params)
	if len(e.Args) != len(params) {
		return c.poison(e, e.Pos, "%s expects %d arguments, got %d", e.Name, len(params), len(e.Args))
	}
	return result
}

func (c *checker) checkArrayLit(e *syntax.ArrayLit, hint Type) Type {
	if at, ok := hint.(*ArrayType); ok {
		for _, elem := range e.Elems {
			c.checkAgainst(elem, at.Elem)
		}
		return at
	}
	if isInvalid(hint) {
		for _, x := range e.Elems {
			c.infer(x, nil)
		}
		return Invalid
	}
	if len(e.Elems) == 0 {
		return c.poison(e, e.Pos, "cannot infer the element type of an empty array; add a type annotation")
	}
	// The first element that does not diverge determines the element type.
	var elem Type
	for _, x := range e.Elems {
		if elem == nil {
			if t := c.infer(x, nil); t != Never {
				elem = t
			}
		} else {
			c.checkAgainst(x, elem)
		}
	}
	if elem == nil {
		return Never
	}
	if isInvalid(elem) {
		return Invalid
	}
	return &ArrayType{Elem: elem}
}

func (c *checker) checkStructLit(e *syntax.StructLit) Type {
	st := c.structs[e.Name]
	if st == nil {
		for _, init := range e.Fields {
			c.infer(init.Value, nil)
		}
		return c.poison(e, e.Pos, "undefined struct %s", e.Name)
	}
	seen := map[string]bool{}
	var err Type
	for _, init := range e.Fields {
		ft, ok := st.field(init.Name)
		switch {
		case !ok:
			c.infer(init.Value, nil)
			err = c.poison(e, init.Pos, "no field %s on %s", init.Name, e.Name)
		case seen[init.Name]:
			c.infer(init.Value, nil)
			err = c.poison(e, init.Pos, "field %s specified more than once", init.Name)
		default:
			seen[init.Name] = true
			c.checkAgainst(init.Value, ft)
		}
	}
	for _, f := range st.Decl.Fields {
		if !seen[f.Name] {
			err = c.poison(e, e.Pos, "missing field %s in %s", f.Name, e.Name)
		}
	}
	if err != nil {
		return Invalid
	}
	return st
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

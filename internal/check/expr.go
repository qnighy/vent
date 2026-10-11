package check

import (
	"fmt"

	"github.com/qnighy/vent/internal/syntax"
)

func (c *checker) checkFunc(fn *syntax.FnDecl) {
	sig := c.funcs[fn.Name]
	c.result = sig.Result
	c.scope = &scope{vars: map[string]*variable{}}
	c.loops = nil
	c.deferred = nil
	for i, param := range fn.Params {
		c.scope.vars[param.Name] = &variable{typ: sig.Params[i]}
	}
	c.check(fn.Body, sig.Result)
	for _, d := range c.deferred {
		d()
	}
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

func fresh() Type {
	return &TypeVar{}
}

// check infers the type of e and unifies it with want.
func (c *checker) check(e syntax.Expr, want Type) Type {
	return c.expect(e, c.infer(e), want)
}

// expect unifies t, the type of e, with want. On failure it poisons the node
// that produced the mismatching value: the innermost tail of e.
func (c *checker) expect(e syntax.Expr, t, want Type) Type {
	if unify(t, want) {
		return want
	}
	return c.mismatch(e, want, t)
}

func (c *checker) mismatch(e syntax.Expr, want, got Type) Type {
	for {
		b, ok := e.(*syntax.BlockExpr)
		if !ok || b.Tail == nil {
			break
		}
		e = b.Tail
	}
	if b, ok := e.(*syntax.BlockExpr); ok {
		return c.poison(b, b.Pos, "mismatched types: expected %v, found %v", want, got)
	}
	return c.poison(e, e.Position(), "mismatched types: expected %v, found %v", want, got)
}

// infer returns the type of e, built bottom-up from its subexpressions.
func (c *checker) infer(e syntax.Expr) Type {
	t := c.infer1(e)
	if !isInvalid(t) {
		c.info.Types[e] = t
	}
	return t
}

func (c *checker) infer1(e syntax.Expr) Type {
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
		x := c.infer(e.X)
		want := Type(Int)
		if e.Op == syntax.Bang {
			want = Bool
		}
		if !unify(x, want) {
			return c.poison(e, e.Pos, "invalid operand for unary %v: %v", e.Op, x)
		}
		return want
	case *syntax.BinaryExpr:
		x := c.infer(e.X)
		y := c.infer(e.Y)
		return c.binaryOp(e, e.Op, x, y)
	case *syntax.AssignExpr:
		return c.checkAssign(e)
	case *syntax.CallExpr:
		return c.checkCall(e)
	case *syntax.ArrayLit:
		elem := fresh()
		for _, x := range e.Elems {
			if t := c.infer(x); !unify(t, elem) {
				c.mismatch(x, elem, t)
			}
		}
		return &ArrayType{Elem: elem}
	case *syntax.StructLit:
		return c.checkStructLit(e)
	case *syntax.IndexExpr:
		x := c.infer(e.X)
		c.check(e.Index, Int)
		switch x := prune(x).(type) {
		case *InvalidType, *NeverType:
			return x
		}
		elem := fresh()
		if !unify(x, &ArrayType{Elem: elem}) {
			return c.poison(e, e.Pos, "cannot index into a value of type %v", x)
		}
		return elem
	case *syntax.FieldExpr:
		switch x := prune(c.infer(e.X)).(type) {
		case *StructType:
			if f, ok := x.field(e.Name); ok {
				return f
			}
			return c.poison(e, e.Pos, "no field %s on %v", e.Name, x)
		case *InvalidType, *NeverType:
			return x
		case *TypeVar:
			return c.poison(e, e.Pos, "cannot infer the type whose field %s is accessed; add a type annotation", e.Name)
		default:
			return c.poison(e, e.Pos, "cannot access field %s of %v", e.Name, x)
		}
	case *syntax.MethodCallExpr:
		return c.checkMethodCall(e)
	case *syntax.BlockExpr:
		return c.checkBlock(e)
	case *syntax.IfExpr:
		return c.checkIf(e)
	case *syntax.WhileExpr:
		c.check(e.Cond, Bool)
		c.loops = append(c.loops, &loopCtx{})
		c.check(e.Body, Unit)
		c.loops = c.loops[:len(c.loops)-1]
		return Unit
	case *syntax.LoopExpr:
		ctx := &loopCtx{isLoop: true}
		c.loops = append(c.loops, ctx)
		c.check(e.Body, Unit)
		c.loops = c.loops[:len(c.loops)-1]
		if ctx.result == nil {
			return Never
		}
		return ctx.result
	case *syntax.BreakExpr:
		c.checkBreak(e)
		return Never
	case *syntax.ContinueExpr:
		return Never
	case *syntax.ReturnExpr:
		if e.Value == nil {
			if !unify(Unit, c.result) {
				c.poison(e, e.Pos, "mismatched types: expected %v, found ()", c.result)
			}
		} else {
			c.check(e.Value, c.result)
		}
		return Never
	default:
		panic(fmt.Sprintf("unknown expression %T", e))
	}
}

func (c *checker) checkBlock(b *syntax.BlockExpr) Type {
	c.scope = &scope{parent: c.scope, vars: map[string]*variable{}}
	defer func() { c.scope = c.scope.parent }()

	diverges := false
	for _, stmt := range b.Stmts {
		switch stmt := stmt.(type) {
		case *syntax.LetStmt:
			var t Type
			if stmt.Type != nil {
				t = c.resolveType(stmt, stmt.Type)
				c.check(stmt.Value, t)
			} else {
				t = c.infer(stmt.Value)
			}
			diverges = diverges || prune(t) == Never
			c.scope.vars[stmt.Name] = &variable{typ: t, mutable: stmt.Mutable}
		case *syntax.ExprStmt:
			if prune(c.infer(stmt.X)) == Never {
				diverges = true
			}
		default:
			panic(fmt.Sprintf("unknown statement %T", stmt))
		}
	}
	if b.Tail != nil {
		return c.infer(b.Tail)
	}
	if diverges {
		return Never
	}
	return Unit
}

func (c *checker) checkIf(e *syntax.IfExpr) Type {
	c.check(e.Cond, Bool)
	if e.Else == nil {
		c.check(e.Then, Unit)
		return Unit
	}
	t := c.infer(e.Then)
	u := c.infer(e.Else)
	if prune(t) == Never {
		return u
	}
	if !unify(t, u) {
		c.mismatch(e.Else, t, u)
	}
	return t
}

// checkBreak checks a break. The first break of a loop determines its type.
func (c *checker) checkBreak(e *syntax.BreakExpr) {
	ctx := c.loops[len(c.loops)-1]
	if !ctx.isLoop {
		return // A break in while carries no value; the parser checks that.
	}
	var t Type = Unit
	if e.Value != nil {
		t = c.infer(e.Value)
	}
	switch {
	case ctx.result == nil:
		if prune(t) != Never {
			ctx.result = t
		}
	case !unify(t, ctx.result):
		if e.Value == nil {
			c.poison(e, e.Pos, "mismatched types: expected %v, found ()", ctx.result)
		} else {
			c.mismatch(e.Value, ctx.result, t)
		}
	}
}

// binaryOp checks a binary operation on operands of types x and y.
// node is poisoned on failure.
func (c *checker) binaryOp(node syntax.Expr, op syntax.TokenKind, x, y Type) Type {
	if isInvalid(x) || isInvalid(y) {
		return Invalid
	}
	if prune(x) == Never || prune(y) == Never {
		return Never
	}
	invalid := func() Type {
		return c.poison(node, node.Position(), "invalid operands for %v: %v and %v", op, x, y)
	}
	switch op {
	case syntax.AndAnd, syntax.OrOr:
		if unify(x, Bool) && unify(y, Bool) {
			return Bool
		}
		return c.poison(node, node.Position(), "operands of %v must be Bool, got %v and %v", op, x, y)
	case syntax.Eq, syntax.Ne:
		if !unify(x, y) {
			return c.poison(node, node.Position(), "cannot compare %v and %v", x, y)
		}
		c.deferComparable(node, x)
		return Bool
	case syntax.Minus, syntax.Star, syntax.Slash, syntax.Percent:
		if unify(x, Int) && unify(y, Int) {
			return Int
		}
		return invalid()
	}

	// + and the ordering operators mean different things on Int and String,
	// so the operand type must already be known here.
	t := prune(x)
	if _, ok := t.(*TypeVar); ok {
		t = prune(y)
	}
	if _, ok := t.(*TypeVar); ok {
		return c.poison(node, node.Position(), "cannot infer the operand type of %v; add a type annotation", op)
	}
	if (t != Int && t != String) || !unify(x, t) || !unify(y, t) {
		return invalid()
	}
	if op == syntax.Plus {
		return t
	}
	return Bool
}

// deferComparable checks at the end of the function that values of type t
// can be compared. Equality works the same way on every comparable type, so
// it does not matter when t becomes known.
func (c *checker) deferComparable(node syntax.Expr, t Type) {
	c.deferred = append(c.deferred, func() {
		if !comparable(t) {
			c.poison(node, node.Position(), "cannot compare %v and %v", t, t)
		}
	})
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
		v := c.infer(e.Value)
		r := c.binaryOp(e, op, target, v)
		if !unify(r, target) {
			c.poison(e, e.Pos, "mismatched types: expected %v, found %v", target, r)
		}
	} else {
		c.check(e.Value, target)
	}
	return Unit
}

// checkPlace checks a place that node writes to and returns its type.
// verb describes the write, such as "assign to".
func (c *checker) checkPlace(node syntax.Expr, place syntax.Expr, verb string) Type {
	t := c.infer(place)
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

// checkArgs checks args against params. Extra arguments are inferred only.
func (c *checker) checkArgs(args []syntax.Expr, params []Type) {
	for i, arg := range args {
		if i < len(params) {
			c.check(arg, params[i])
		} else {
			c.infer(arg)
		}
	}
}

func (c *checker) checkCall(e *syntax.CallExpr) Type {
	callee := prune(c.infer(e.Fn))
	if v, ok := callee.(*TypeVar); ok {
		// Calling does not depend on the function, so its type can be inferred.
		ft := &FuncType{Result: fresh()}
		for range e.Args {
			ft.Params = append(ft.Params, fresh())
		}
		unify(v, ft)
		callee = ft
	}
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
		for _, arg := range e.Args {
			types = append(types, c.infer(arg))
		}
		if !arity(2, 2) {
			return Invalid
		}
		x, y := types[0], types[1]
		if !unify(x, y) {
			return c.poison(e, e.Pos, "cannot compare %v and %v", x, y)
		}
		c.deferComparable(e, x)
		return Unit
	default:
		panic("unknown builtin " + name)
	}
}

func (c *checker) checkMethodCall(e *syntax.MethodCallExpr) Type {
	recv := prune(c.infer(e.Recv))
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
	case *TypeVar:
		// Which method is called depends on the receiver type.
		c.checkArgs(e.Args, nil)
		return c.poison(e, e.Pos, "cannot infer the receiver type of method %s; add a type annotation", e.Name)
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

func (c *checker) checkStructLit(e *syntax.StructLit) Type {
	st := c.structs[e.Name]
	if st == nil {
		for _, init := range e.Fields {
			c.infer(init.Value)
		}
		return c.poison(e, e.Pos, "undefined struct %s", e.Name)
	}
	seen := map[string]bool{}
	var err Type
	for _, init := range e.Fields {
		ft, ok := st.field(init.Name)
		switch {
		case !ok:
			c.infer(init.Value)
			err = c.poison(e, init.Pos, "no field %s on %s", init.Name, e.Name)
		case seen[init.Name]:
			c.infer(init.Value)
			err = c.poison(e, init.Pos, "field %s specified more than once", init.Name)
		default:
			seen[init.Name] = true
			c.check(init.Value, ft)
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

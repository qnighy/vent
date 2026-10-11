// Package interp implements the tree-walking interpreter for vent.
package interp

import (
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/qnighy/vent/internal/check"
	"github.com/qnighy/vent/internal/syntax"
)

// maxCallDepth bounds recursion so that runaway programs fail deterministically
// instead of exhausting the Go stack.
const maxCallDepth = 10000

// RuntimeError is an error raised while evaluating a program.
type RuntimeError struct {
	Pos syntax.Pos
	Msg string
}

func (e *RuntimeError) Error() string {
	return fmt.Sprintf("%v: runtime error: %s", e.Pos, e.Msg)
}

func errorf(pos syntax.Pos, format string, args ...any) error {
	return &RuntimeError{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

// Control-flow signals travel up the Go call stack as errors.
// The parser guarantees that they never escape their enclosing loop or function.
type breakSignal struct{ value Value }
type continueSignal struct{}
type returnSignal struct{ value Value }

func (*breakSignal) Error() string    { return "break outside of a loop" }
func (*continueSignal) Error() string { return "continue outside of a loop" }
func (*returnSignal) Error() string   { return "return outside of a function" }

// Interp holds the state of a running program.
type Interp struct {
	out     io.Writer
	globals *scope
	structs map[string]*syntax.StructDecl
	info    *check.Info
	prog    *syntax.Program
	depth   int
	// env binds the type parameters of the running function. Which
	// implementation a trait method call runs may depend on them.
	env check.Env
}

// Run executes the program in src, writing program output to out.
// filename is used only for diagnostics.
func Run(filename string, src []byte, out io.Writer) error {
	return withFilename(filename, run(src, out))
}

// withFilename prefixes err with filename.
func withFilename(filename string, err error) error {
	switch err.(type) {
	case nil:
		return nil
	case *syntax.Error, *check.Error, *RuntimeError:
		// These already start with a line:col position.
		return fmt.Errorf("%s:%w", filename, err)
	default:
		return fmt.Errorf("%s: %w", filename, err)
	}
}

func run(src []byte, out io.Writer) error {
	in, err := load(src, out)
	if err != nil {
		return err
	}
	var mainFn *FuncValue
	if b := in.globals.lookup("main"); b != nil {
		mainFn, _ = b.value.(*FuncValue)
	}
	if mainFn == nil {
		return errors.New("no main function")
	}
	if len(mainFn.Decl.Params) != 0 {
		return errorf(mainFn.Decl.Pos, "main must not take parameters")
	}
	if len(mainFn.Decl.TypeParams) != 0 {
		return errorf(mainFn.Decl.Pos, "main must not have type parameters")
	}
	_, err = in.callFunc(mainFn.Decl.Pos, mainFn, nil)
	return err
}

// load parses src and defines its top-level declarations.
func load(src []byte, out io.Writer) (*Interp, error) {
	prog, err := syntax.Parse(src)
	if err != nil {
		return nil, err
	}
	info := check.Check(prog)
	if len(info.Fatal) > 0 {
		return nil, info.Fatal[0]
	}
	in := &Interp{
		out:     out,
		globals: newScope(nil),
		structs: map[string]*syntax.StructDecl{},
		info:    info,
		prog:    prog,
	}
	in.defineBuiltins()
	for _, st := range prog.Structs {
		in.structs[st.Name] = st
	}
	for _, fn := range prog.Fns {
		in.globals.define(fn.Name, &FuncValue{Decl: fn}, false)
	}
	return in, nil
}

// poison returns the static error of node, if any. Evaluating a node that
// failed to check aborts with its error.
func (in *Interp) poison(node any) error {
	if err, ok := in.info.Poisoned[node]; ok {
		return err
	}
	return nil
}

type binding struct {
	value   Value
	mutable bool
}

type scope struct {
	parent *scope
	vars   map[string]*binding
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, vars: map[string]*binding{}}
}

func (s *scope) lookup(name string) *binding {
	for ; s != nil; s = s.parent {
		if b, ok := s.vars[name]; ok {
			return b
		}
	}
	return nil
}

// define introduces a new binding, shadowing any existing one with the same name.
func (s *scope) define(name string, v Value, mutable bool) {
	s.vars[name] = &binding{value: v, mutable: mutable}
}

func (in *Interp) callFunc(pos syntax.Pos, fn *FuncValue, args []Value) (Value, error) {
	decl := fn.Decl
	if len(args) != len(decl.Params) {
		return nil, errorf(pos, "%s expects %d arguments, got %d", decl.Name, len(decl.Params), len(args))
	}
	if in.depth >= maxCallDepth {
		return nil, errorf(pos, "stack overflow")
	}
	if err := in.poison(decl); err != nil {
		return nil, err
	}
	in.depth++
	outerEnv := in.env
	in.env = fn.Env
	defer func() {
		in.depth--
		in.env = outerEnv
	}()

	sc := newScope(in.globals)
	for i, param := range decl.Params {
		sc.define(param.Name, args[i], false)
	}
	v, err := in.evalBlock(decl.Body, sc)
	if ret, ok := err.(*returnSignal); ok {
		return ret.value, nil
	}
	return v, err
}

func (in *Interp) evalBlock(block *syntax.BlockExpr, parent *scope) (Value, error) {
	if err := in.poison(block); err != nil {
		return nil, err
	}
	sc := newScope(parent)
	for _, stmt := range block.Stmts {
		switch stmt := stmt.(type) {
		case *syntax.LetStmt:
			if err := in.poison(stmt); err != nil {
				return nil, err
			}
			v, err := in.eval(stmt.Value, sc)
			if err != nil {
				return nil, err
			}
			sc.define(stmt.Name, v, stmt.Mutable)
		case *syntax.ExprStmt:
			if _, err := in.eval(stmt.X, sc); err != nil {
				return nil, err
			}
		default:
			panic(fmt.Sprintf("unknown statement %T", stmt))
		}
	}
	if block.Tail == nil {
		return UnitValue{}, nil
	}
	return in.eval(block.Tail, sc)
}

func (in *Interp) eval(e syntax.Expr, sc *scope) (Value, error) {
	if err := in.poison(e); err != nil {
		return nil, err
	}
	switch e := e.(type) {
	case *syntax.IntLit:
		return IntValue(e.Value), nil
	case *syntax.StringLit:
		return StringValue(e.Value), nil
	case *syntax.BoolLit:
		return BoolValue(e.Value), nil
	case *syntax.UnitLit:
		return UnitValue{}, nil
	case *syntax.IdentExpr, *syntax.IndexExpr, *syntax.FieldExpr:
		if ref := in.info.Refs[e]; ref != nil {
			// A generic function is instantiated where it is named.
			return in.funcValue(e.Position(), ref)
		}
		// The value is copied out of a place.
		v, err := in.evalRead(e, sc)
		if err != nil {
			return nil, err
		}
		return share(v), nil
	case *syntax.ArrayLit:
		elems := make([]Value, len(e.Elems))
		for i, x := range e.Elems {
			v, err := in.eval(x, sc)
			if err != nil {
				return nil, err
			}
			elems[i] = v
		}
		return &ArrayValue{Elems: elems}, nil
	case *syntax.StructLit:
		return in.evalStructLit(e, sc)
	case *syntax.PathExpr:
		return in.evalPath(e)
	case *syntax.MatchExpr:
		return in.evalMatch(e, sc)
	case *syntax.MethodCallExpr:
		return in.evalMethodCall(e, sc)
	case *syntax.UnaryExpr:
		x, err := in.evalRead(e.X, sc)
		if err != nil {
			return nil, err
		}
		return unaryOp(e.Pos, e.Op, x)
	case *syntax.BinaryExpr:
		return in.evalBinary(e, sc)
	case *syntax.AssignExpr:
		return in.evalAssign(e, sc)
	case *syntax.CallExpr:
		return in.evalCall(e, sc)
	case *syntax.BlockExpr:
		return in.evalBlock(e, sc)
	case *syntax.IfExpr:
		return in.evalIf(e, sc)
	case *syntax.WhileExpr:
		for {
			cond, err := in.evalCond(e.Cond, sc)
			if err != nil {
				return nil, err
			}
			if !cond {
				return UnitValue{}, nil
			}
			_, err = in.evalBlock(e.Body, sc)
			switch err.(type) {
			case nil, *continueSignal:
			case *breakSignal:
				return UnitValue{}, nil
			default:
				return nil, err
			}
		}
	case *syntax.LoopExpr:
		for {
			_, err := in.evalBlock(e.Body, sc)
			switch err := err.(type) {
			case nil, *continueSignal:
			case *breakSignal:
				return err.value, nil
			default:
				return nil, err
			}
		}
	case *syntax.BreakExpr:
		v, err := in.evalOptional(e.Value, sc)
		if err != nil {
			return nil, err
		}
		return nil, &breakSignal{value: v}
	case *syntax.ContinueExpr:
		return nil, &continueSignal{}
	case *syntax.ReturnExpr:
		v, err := in.evalOptional(e.Value, sc)
		if err != nil {
			return nil, err
		}
		return nil, &returnSignal{value: v}
	default:
		panic(fmt.Sprintf("unknown expression %T", e))
	}
}

// evalOptional evaluates e, or returns () if e is nil.
func (in *Interp) evalOptional(e syntax.Expr, sc *scope) (Value, error) {
	if e == nil {
		return UnitValue{}, nil
	}
	return in.eval(e, sc)
}

func (in *Interp) evalCond(e syntax.Expr, sc *scope) (bool, error) {
	v, err := in.eval(e, sc)
	if err != nil {
		return false, err
	}
	b, ok := v.(BoolValue)
	if !ok {
		return false, errorf(e.Position(), "condition must be Bool, got %s", v.TypeName())
	}
	return bool(b), nil
}

func (in *Interp) evalIf(e *syntax.IfExpr, sc *scope) (Value, error) {
	cond, err := in.evalCond(e.Cond, sc)
	if err != nil {
		return nil, err
	}
	if cond {
		return in.evalBlock(e.Then, sc)
	}
	return in.evalOptional(e.Else, sc)
}

func (in *Interp) evalBinary(e *syntax.BinaryExpr, sc *scope) (Value, error) {
	var x Value
	var err error
	if isSimple(e.Y) {
		x, err = in.evalRead(e.X, sc)
	} else {
		x, err = in.eval(e.X, sc)
	}
	if err != nil {
		return nil, err
	}
	if e.Op == syntax.AndAnd || e.Op == syntax.OrOr {
		b, ok := x.(BoolValue)
		if !ok {
			return nil, errorf(e.Pos, "operand of %v must be Bool, got %s", e.Op, x.TypeName())
		}
		if bool(b) == (e.Op == syntax.OrOr) {
			return b, nil
		}
		y, err := in.evalRead(e.Y, sc)
		if err != nil {
			return nil, err
		}
		if _, ok := y.(BoolValue); !ok {
			return nil, errorf(e.Pos, "operand of %v must be Bool, got %s", e.Op, y.TypeName())
		}
		return y, nil
	}
	y, err := in.evalRead(e.Y, sc)
	if err != nil {
		return nil, err
	}
	return binaryOp(e.Pos, e.Op, x, y)
}

var compoundOps = map[syntax.TokenKind]syntax.TokenKind{
	syntax.PlusAssign:    syntax.Plus,
	syntax.MinusAssign:   syntax.Minus,
	syntax.StarAssign:    syntax.Star,
	syntax.SlashAssign:   syntax.Slash,
	syntax.PercentAssign: syntax.Percent,
}

func (in *Interp) evalAssign(e *syntax.AssignExpr, sc *scope) (Value, error) {
	pl, err := in.evalPlace(e.Target, sc)
	if err != nil {
		return nil, err
	}
	if err := pl.checkWritable(e.Pos, "assign to"); err != nil {
		return nil, err
	}
	v, err := in.eval(e.Value, sc)
	if err != nil {
		return nil, err
	}
	if op, ok := compoundOps[e.Op]; ok {
		cur, err := pl.read()
		if err != nil {
			return nil, err
		}
		v, err = binaryOp(e.Pos, op, cur, v)
		if err != nil {
			return nil, err
		}
	}
	slot, err := pl.slot()
	if err != nil {
		return nil, err
	}
	*slot = v
	return UnitValue{}, nil
}

func (in *Interp) evalCall(e *syntax.CallExpr, sc *scope) (Value, error) {
	callee, err := in.eval(e.Fn, sc)
	if err != nil {
		return nil, err
	}
	var args []Value
	if _, ok := callee.(*BuiltinValue); ok {
		// Builtins do not retain their arguments.
		args, err = in.evalOperands(e.Args, sc)
	} else {
		args, err = in.evalArgs(e.Args, sc)
	}
	if err != nil {
		return nil, err
	}
	switch callee := callee.(type) {
	case *FuncValue:
		return in.callFunc(e.Pos, callee, args)
	case *BuiltinValue:
		return callee.Fn(in, e.Pos, args)
	case *VariantValue:
		variant := callee.Decl.Variants[callee.Variant]
		if len(args) != len(variant.Fields) {
			return nil, errorf(e.Pos, "%s::%s expects %d arguments, got %d", callee.Decl.Name, variant.Name, len(variant.Fields), len(args))
		}
		return &EnumValue{Decl: callee.Decl, Variant: callee.Variant, Fields: args}, nil
	default:
		return nil, errorf(e.Pos, "cannot call a value of type %s", callee.TypeName())
	}
}

func unaryOp(pos syntax.Pos, op syntax.TokenKind, x Value) (Value, error) {
	switch op {
	case syntax.Minus:
		if x, ok := x.(IntValue); ok {
			if x == math.MinInt64 {
				return nil, errorf(pos, "integer overflow")
			}
			return -x, nil
		}
	case syntax.Bang:
		if x, ok := x.(BoolValue); ok {
			return !x, nil
		}
	}
	return nil, errorf(pos, "invalid operand for unary %v: %s", op, x.TypeName())
}

func binaryOp(pos syntax.Pos, op syntax.TokenKind, x, y Value) (Value, error) {
	switch op {
	case syntax.Eq, syntax.Ne:
		eq, err := equal(pos, x, y)
		if err != nil {
			return nil, err
		}
		return BoolValue(eq == (op == syntax.Eq)), nil
	}

	switch x := x.(type) {
	case IntValue:
		if y, ok := y.(IntValue); ok {
			return intOp(pos, op, x, y)
		}
	case StringValue:
		if y, ok := y.(StringValue); ok {
			switch op {
			case syntax.Plus:
				return x + y, nil
			case syntax.Lt:
				return BoolValue(x < y), nil
			case syntax.Le:
				return BoolValue(x <= y), nil
			case syntax.Gt:
				return BoolValue(x > y), nil
			case syntax.Ge:
				return BoolValue(x >= y), nil
			}
		}
	}
	return nil, errorf(pos, "invalid operands for %v: %s and %s", op, x.TypeName(), y.TypeName())
}

func intOp(pos syntax.Pos, op syntax.TokenKind, x, y IntValue) (Value, error) {
	switch op {
	case syntax.Plus:
		r := x + y
		if (x^r)&(y^r) < 0 {
			return nil, errorf(pos, "integer overflow")
		}
		return r, nil
	case syntax.Minus:
		r := x - y
		if (x^y)&(x^r) < 0 {
			return nil, errorf(pos, "integer overflow")
		}
		return r, nil
	case syntax.Star:
		r := x * y
		if x != 0 && (r/x != y || (x == -1 && y == math.MinInt64)) {
			return nil, errorf(pos, "integer overflow")
		}
		return r, nil
	case syntax.Slash, syntax.Percent:
		if y == 0 {
			return nil, errorf(pos, "division by zero")
		}
		if x == math.MinInt64 && y == -1 {
			if op == syntax.Percent {
				return IntValue(0), nil
			}
			return nil, errorf(pos, "integer overflow")
		}
		if op == syntax.Slash {
			return x / y, nil
		}
		return x % y, nil
	case syntax.Lt:
		return BoolValue(x < y), nil
	case syntax.Le:
		return BoolValue(x <= y), nil
	case syntax.Gt:
		return BoolValue(x > y), nil
	case syntax.Ge:
		return BoolValue(x >= y), nil
	}
	return nil, errorf(pos, "invalid operands for %v: Int and Int", op)
}

// equal implements == as structural equality.
// There is deliberately no shortcut for identical composites: it must not be
// observable whether two composites share their storage.
func equal(pos syntax.Pos, x, y Value) (bool, error) {
	switch x := x.(type) {
	case *ArrayValue:
		if y, ok := y.(*ArrayValue); ok {
			if len(x.Elems) != len(y.Elems) {
				return false, nil
			}
			for i := range x.Elems {
				if eq, err := equal(pos, x.Elems[i], y.Elems[i]); err != nil || !eq {
					return false, err
				}
			}
			return true, nil
		}
	case *EnumValue:
		if y, ok := y.(*EnumValue); ok && x.Decl == y.Decl {
			if x.Variant != y.Variant {
				return false, nil
			}
			for i := range x.Fields {
				if eq, err := equal(pos, x.Fields[i], y.Fields[i]); err != nil || !eq {
					return false, err
				}
			}
			return true, nil
		}
	case *StructValue:
		if y, ok := y.(*StructValue); ok && x.Decl == y.Decl {
			for i := range x.Fields {
				if eq, err := equal(pos, x.Fields[i], y.Fields[i]); err != nil || !eq {
					return false, err
				}
			}
			return true, nil
		}
	case IntValue:
		if y, ok := y.(IntValue); ok {
			return x == y, nil
		}
	case BoolValue:
		if y, ok := y.(BoolValue); ok {
			return x == y, nil
		}
	case StringValue:
		if y, ok := y.(StringValue); ok {
			return x == y, nil
		}
	case UnitValue:
		if _, ok := y.(UnitValue); ok {
			return true, nil
		}
	}
	return false, errorf(pos, "cannot compare %s and %s", x.TypeName(), y.TypeName())
}

// funcValue returns the function that ref designates in the running
// function. For a trait method, this chooses the impl by the types that the
// type parameters of the running function stand for.
func (in *Interp) funcValue(pos syntax.Pos, ref *check.Ref) (*FuncValue, error) {
	f, env, err := in.info.Resolve(ref, in.env)
	switch {
	case err == check.ErrNoImpl:
		return nil, errorf(pos, "no implementation of %s for %v", ref.Trait.Name, check.Subst(ref.Self, in.env))
	case err != nil:
		return nil, err
	}
	return &FuncValue{Decl: f.Decl, Env: env}, nil
}

// evalPath evaluates Qual::Name.
func (in *Interp) evalPath(e *syntax.PathExpr) (Value, error) {
	ref := in.info.Refs[e]
	if ref == nil {
		return nil, errorf(e.Pos, "undefined path %s::%s", e.Qual, e.Name)
	}
	if ref.Kind != check.RefVariant {
		return in.funcValue(e.Pos, ref)
	}
	decl := ref.Enum.Decl
	if len(decl.Variants[ref.Variant].Fields) == 0 {
		return &EnumValue{Decl: decl, Variant: ref.Variant}, nil
	}
	return &VariantValue{Decl: decl, Variant: ref.Variant}, nil
}

// Package interp implements the tree-walking interpreter for vent.
package interp

import (
	"errors"
	"fmt"
	"io"
	"math"

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
	depth   int
}

// Run executes the program in src, writing program output to out.
// filename is used only for diagnostics.
func Run(filename string, src []byte, out io.Writer) error {
	err := run(src, out)
	switch err.(type) {
	case nil:
		return nil
	case *syntax.Error, *RuntimeError:
		// These already start with a line:col position.
		return fmt.Errorf("%s:%w", filename, err)
	default:
		return fmt.Errorf("%s: %w", filename, err)
	}
}

func run(src []byte, out io.Writer) error {
	prog, err := syntax.Parse(src)
	if err != nil {
		return err
	}
	in := &Interp{out: out, globals: newScope(nil)}
	in.defineBuiltins()
	for _, fn := range prog.Fns {
		if in.globals.lookup(fn.Name) != nil {
			return errorf(fn.Pos, "duplicate definition of %s", fn.Name)
		}
		in.globals.define(fn.Name, &FuncValue{Decl: fn}, false)
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
	_, err = in.callFunc(mainFn.Decl.Pos, mainFn, nil)
	return err
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
	in.depth++
	defer func() { in.depth-- }()

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
	sc := newScope(parent)
	for _, stmt := range block.Stmts {
		switch stmt := stmt.(type) {
		case *syntax.LetStmt:
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
	switch e := e.(type) {
	case *syntax.IntLit:
		return IntValue(e.Value), nil
	case *syntax.StringLit:
		return StringValue(e.Value), nil
	case *syntax.BoolLit:
		return BoolValue(e.Value), nil
	case *syntax.UnitLit:
		return UnitValue{}, nil
	case *syntax.IdentExpr:
		b := sc.lookup(e.Name)
		if b == nil {
			return nil, errorf(e.Pos, "undefined variable %s", e.Name)
		}
		return b.value, nil
	case *syntax.UnaryExpr:
		x, err := in.eval(e.X, sc)
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
	x, err := in.eval(e.X, sc)
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
		y, err := in.eval(e.Y, sc)
		if err != nil {
			return nil, err
		}
		if _, ok := y.(BoolValue); !ok {
			return nil, errorf(e.Pos, "operand of %v must be Bool, got %s", e.Op, y.TypeName())
		}
		return y, nil
	}
	y, err := in.eval(e.Y, sc)
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
	b := sc.lookup(e.Target.Name)
	if b == nil {
		return nil, errorf(e.Target.Pos, "undefined variable %s", e.Target.Name)
	}
	if !b.mutable {
		return nil, errorf(e.Pos, "cannot assign to immutable variable %s", e.Target.Name)
	}
	v, err := in.eval(e.Value, sc)
	if err != nil {
		return nil, err
	}
	if op, ok := compoundOps[e.Op]; ok {
		v, err = binaryOp(e.Pos, op, b.value, v)
		if err != nil {
			return nil, err
		}
	}
	b.value = v
	return UnitValue{}, nil
}

func (in *Interp) evalCall(e *syntax.CallExpr, sc *scope) (Value, error) {
	callee, err := in.eval(e.Fn, sc)
	if err != nil {
		return nil, err
	}
	args := make([]Value, len(e.Args))
	for i, arg := range e.Args {
		args[i], err = in.eval(arg, sc)
		if err != nil {
			return nil, err
		}
	}
	switch callee := callee.(type) {
	case *FuncValue:
		return in.callFunc(e.Pos, callee, args)
	case *BuiltinValue:
		return callee.Fn(in, e.Pos, args)
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

func equal(pos syntax.Pos, x, y Value) (bool, error) {
	switch x := x.(type) {
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

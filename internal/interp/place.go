package interp

import (
	"github.com/qnighy/vent/internal/check"
	"github.com/qnighy/vent/internal/syntax"
)

// place is an evaluated place expression: a root followed by a path of
// projections. Index operands are evaluated when the place is evaluated, but
// the path is resolved only when the place is read or written.
type place struct {
	root *binding // nil if the root is a temporary
	name string   // name of the root variable
	temp Value    // the root value if root is nil
	path []projection
}

// projection is either an index (field == "") or a field access.
type projection struct {
	pos   syntax.Pos
	index int64
	field string
}

// evalPlace evaluates a place expression without reading it.
// Any other expression is evaluated to a temporary.
func (in *Interp) evalPlace(e syntax.Expr, sc *scope) (*place, error) {
	if err := in.poison(e); err != nil {
		return nil, err
	}
	switch e := e.(type) {
	case *syntax.IdentExpr:
		b := sc.lookup(e.Name)
		if b == nil {
			return nil, errorf(e.Pos, "undefined variable %s", e.Name)
		}
		return &place{root: b, name: e.Name}, nil
	case *syntax.IndexExpr:
		pl, err := in.evalPlace(e.X, sc)
		if err != nil {
			return nil, err
		}
		v, err := in.eval(e.Index, sc)
		if err != nil {
			return nil, err
		}
		index, ok := v.(IntValue)
		if !ok {
			return nil, errorf(e.Index.Position(), "array index must be Int, got %s", v.TypeName())
		}
		pl.path = append(pl.path, projection{pos: e.Pos, index: int64(index)})
		return pl, nil
	case *syntax.FieldExpr:
		pl, err := in.evalPlace(e.X, sc)
		if err != nil {
			return nil, err
		}
		pl.path = append(pl.path, projection{pos: e.Pos, field: e.Name})
		return pl, nil
	default:
		v, err := in.eval(e, sc)
		if err != nil {
			return nil, err
		}
		return &place{temp: v}, nil
	}
}

// evalRead evaluates e for a use that does not retain the value.
// Unlike eval, reading a place does not mark the value as shared, so the
// caller must finish using the value before anything else is evaluated.
func (in *Interp) evalRead(e syntax.Expr, sc *scope) (Value, error) {
	switch e.(type) {
	case *syntax.IdentExpr, *syntax.IndexExpr, *syntax.FieldExpr:
		pl, err := in.evalPlace(e, sc)
		if err != nil {
			return nil, err
		}
		return pl.read()
	default:
		return in.eval(e, sc)
	}
}

// evalOperands evaluates es for uses that do not retain the values. Values
// followed by an expression that might mutate them are evaluated with eval.
func (in *Interp) evalOperands(es []syntax.Expr, sc *scope) ([]Value, error) {
	vs := make([]Value, len(es))
	for i, e := range es {
		restSimple := true
		for _, rest := range es[i+1:] {
			restSimple = restSimple && isSimple(rest)
		}
		var err error
		if restSimple {
			vs[i], err = in.evalRead(e, sc)
		} else {
			vs[i], err = in.eval(e, sc)
		}
		if err != nil {
			return nil, err
		}
	}
	return vs, nil
}

// evalArgs evaluates es to values that may be retained.
func (in *Interp) evalArgs(es []syntax.Expr, sc *scope) ([]Value, error) {
	vs := make([]Value, len(es))
	for i, e := range es {
		var err error
		vs[i], err = in.eval(e, sc)
		if err != nil {
			return nil, err
		}
	}
	return vs, nil
}

// isSimple reports whether evaluating e cannot mutate any variable.
func isSimple(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.IntLit, *syntax.StringLit, *syntax.BoolLit, *syntax.UnitLit, *syntax.IdentExpr, *syntax.PathExpr:
		return true
	case *syntax.UnaryExpr:
		return isSimple(e.X)
	case *syntax.BinaryExpr:
		return isSimple(e.X) && isSimple(e.Y)
	case *syntax.IndexExpr:
		return isSimple(e.X) && isSimple(e.Index)
	case *syntax.FieldExpr:
		return isSimple(e.X)
	default:
		return false
	}
}

func (pl *place) rootValue() Value {
	if pl.root == nil {
		return pl.temp
	}
	return pl.root.value
}

// read returns the current value of the place without copying it.
func (pl *place) read() (Value, error) {
	v := pl.rootValue()
	for _, proj := range pl.path {
		slot, err := project(v, proj)
		if err != nil {
			return nil, err
		}
		v = *slot
	}
	return v, nil
}

// checkWritable reports an error if the place cannot be written at all.
// verb describes the attempted operation, such as "assign to".
func (pl *place) checkWritable(pos syntax.Pos, verb string) error {
	if pl.root == nil {
		if len(pl.path) == 0 {
			return errorf(pos, "invalid assignment target")
		}
		// Writing into a temporary is allowed but has no lasting effect.
		return nil
	}
	if !pl.root.mutable {
		return errorf(pos, "cannot %s immutable variable %s", verb, pl.name)
	}
	return nil
}

// slot resolves the place for writing, unsharing every composite on the way.
// The returned pointer is valid only until the next evaluation step.
func (pl *place) slot() (*Value, error) {
	slot := &pl.temp
	if pl.root != nil {
		slot = &pl.root.value
	}
	for _, proj := range pl.path {
		*slot = unshare(*slot)
		next, err := project(*slot, proj)
		if err != nil {
			return nil, err
		}
		slot = next
	}
	return slot, nil
}

// project returns the slot within v that proj designates.
func project(v Value, proj projection) (*Value, error) {
	if proj.field != "" {
		st, ok := v.(*StructValue)
		if !ok {
			return nil, errorf(proj.pos, "cannot access field %s of %s", proj.field, v.TypeName())
		}
		for i, f := range st.Decl.Fields {
			if f.Name == proj.field {
				return &st.Fields[i], nil
			}
		}
		return nil, errorf(proj.pos, "no field %s on %s", proj.field, st.Decl.Name)
	}
	arr, ok := v.(*ArrayValue)
	if !ok {
		return nil, errorf(proj.pos, "cannot index into a value of type %s", v.TypeName())
	}
	if proj.index < 0 || proj.index >= int64(len(arr.Elems)) {
		return nil, errorf(proj.pos, "index out of bounds: the length is %d but the index is %d", len(arr.Elems), proj.index)
	}
	return &arr.Elems[proj.index], nil
}

func (in *Interp) evalStructLit(e *syntax.StructLit, sc *scope) (Value, error) {
	decl := in.structs[e.Name]
	if def := in.info.Structs[e]; def != nil {
		// The name may be Self.
		decl = def.Decl
	}
	if decl == nil {
		return nil, errorf(e.Pos, "undefined struct %s", e.Name)
	}
	if err := in.poison(decl); err != nil {
		return nil, err
	}
	fields := make([]Value, len(decl.Fields))
	for _, init := range e.Fields {
		i := fieldIndex(decl, init.Name)
		if i < 0 {
			return nil, errorf(init.Pos, "no field %s on %s", init.Name, decl.Name)
		}
		if fields[i] != nil {
			return nil, errorf(init.Pos, "field %s specified more than once", init.Name)
		}
		v, err := in.eval(init.Value, sc)
		if err != nil {
			return nil, err
		}
		fields[i] = v
	}
	for i, f := range fields {
		if f == nil {
			return nil, errorf(e.Pos, "missing field %s in %s", decl.Fields[i].Name, decl.Name)
		}
	}
	return &StructValue{Decl: decl, Fields: fields}, nil
}

func fieldIndex(decl *syntax.StructDecl, name string) int {
	for i, f := range decl.Fields {
		if f.Name == name {
			return i
		}
	}
	return -1
}

// method is a builtin method. A mutating method receives the receiver's slot
// after it has been unshared; others receive a pointer to a read-only value.
type method struct {
	mutates bool
	arity   int
	fn      func(pos syntax.Pos, recv *Value, args []Value) (Value, error)
}

// arrayMethods are the methods of Array. They stand in for library functions
// taking self or &mut self.
var arrayMethods = map[string]*method{
	"len": {arity: 0, fn: func(pos syntax.Pos, recv *Value, args []Value) (Value, error) {
		return IntValue(len((*recv).(*ArrayValue).Elems)), nil
	}},
	"push": {mutates: true, arity: 1, fn: func(pos syntax.Pos, recv *Value, args []Value) (Value, error) {
		arr := (*recv).(*ArrayValue)
		arr.Elems = append(arr.Elems, args[0])
		return UnitValue{}, nil
	}},
	"pop": {mutates: true, arity: 0, fn: func(pos syntax.Pos, recv *Value, args []Value) (Value, error) {
		arr := (*recv).(*ArrayValue)
		if len(arr.Elems) == 0 {
			return nil, errorf(pos, "pop from an empty array")
		}
		last := arr.Elems[len(arr.Elems)-1]
		arr.Elems[len(arr.Elems)-1] = nil
		arr.Elems = arr.Elems[:len(arr.Elems)-1]
		return last, nil
	}},
}

// evalMethodCall evaluates recv.name(args). The receiver is evaluated first,
// then the arguments.
func (in *Interp) evalMethodCall(e *syntax.MethodCallExpr, sc *scope) (Value, error) {
	ref := in.info.Refs[e]
	if ref == nil || ref.Kind == check.RefArrayMethod {
		return in.evalArrayMethodCall(e, sc)
	}
	recv, err := in.eval(e.Recv, sc)
	if err != nil {
		return nil, err
	}
	args, err := in.evalArgs(e.Args, sc)
	if err != nil {
		return nil, err
	}
	fn, err := in.funcValue(e.Pos, ref)
	if err != nil {
		return nil, err
	}
	return in.callFunc(e.Pos, fn, append([]Value{recv}, args...))
}

// evalArrayMethodCall evaluates a call to a builtin method of Array. The
// receiver place is evaluated first, then the arguments, and then the
// receiver is resolved again.
func (in *Interp) evalArrayMethodCall(e *syntax.MethodCallExpr, sc *scope) (Value, error) {
	pl, err := in.evalPlace(e.Recv, sc)
	if err != nil {
		return nil, err
	}
	m, err := pl.lookupMethod(e)
	if err != nil {
		return nil, err
	}
	if m.mutates {
		if err := pl.checkWritable(e.Pos, "mutate"); err != nil {
			return nil, err
		}
	}
	args, err := in.evalArgs(e.Args, sc)
	if err != nil {
		return nil, err
	}
	if len(args) != m.arity {
		return nil, errorf(e.Pos, "%s expects %d arguments, got %d", e.Name, m.arity, len(args))
	}
	// The arguments may have changed the receiver.
	if m2, err := pl.lookupMethod(e); err != nil || m2 != m {
		return nil, errorf(e.Pos, "receiver of %s changed while evaluating the arguments", e.Name)
	}
	if !m.mutates {
		recv, err := pl.read()
		if err != nil {
			return nil, err
		}
		return m.fn(e.Pos, &recv, args)
	}
	slot, err := pl.slot()
	if err != nil {
		return nil, err
	}
	*slot = unshare(*slot)
	return m.fn(e.Pos, slot, args)
}

func (pl *place) lookupMethod(e *syntax.MethodCallExpr) (*method, error) {
	recv, err := pl.read()
	if err != nil {
		return nil, err
	}
	var m *method
	if _, ok := recv.(*ArrayValue); ok {
		m = arrayMethods[e.Name]
	}
	if m == nil {
		return nil, errorf(e.Pos, "no method %s on %s", e.Name, recv.TypeName())
	}
	return m, nil
}

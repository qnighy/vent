package check

// prune follows the bindings of type variables, returning either an unbound
// variable or a type that is not a variable.
func prune(t Type) Type {
	for {
		v, ok := t.(*TypeVar)
		if !ok || v.ref == nil {
			return t
		}
		t = v.ref
	}
}

// zonk returns t with every bound variable replaced by its binding.
func zonk(t Type) Type {
	switch t := prune(t).(type) {
	case *ArrayType:
		return &ArrayType{Elem: zonk(t.Elem)}
	case *FuncType:
		ft := &FuncType{Result: zonk(t.Result)}
		for _, p := range t.Params {
			ft.Params = append(ft.Params, zonk(p))
		}
		return ft
	default:
		return t
	}
}

// unify makes a and b the same type by binding variables, and reports whether
// it succeeded. On failure no variable is bound.
func unify(a, b Type) bool {
	var trail []*TypeVar
	if unifyRec(a, b, &trail) {
		return true
	}
	for _, v := range trail {
		v.ref = nil
	}
	return false
}

func unifyRec(a, b Type, trail *[]*TypeVar) bool {
	a, b = prune(a), prune(b)
	if a == b {
		return true
	}
	switch a.(type) {
	case *InvalidType, *NeverType:
		return true
	}
	switch b.(type) {
	case *InvalidType, *NeverType:
		return true
	}
	if v, ok := a.(*TypeVar); ok {
		return bind(v, b, trail)
	}
	if v, ok := b.(*TypeVar); ok {
		return bind(v, a, trail)
	}
	switch a := a.(type) {
	case *ArrayType:
		b, ok := b.(*ArrayType)
		return ok && unifyRec(a.Elem, b.Elem, trail)
	case *FuncType:
		b, ok := b.(*FuncType)
		if !ok || len(a.Params) != len(b.Params) {
			return false
		}
		for i := range a.Params {
			if !unifyRec(a.Params[i], b.Params[i], trail) {
				return false
			}
		}
		return unifyRec(a.Result, b.Result, trail)
	case *BuiltinType:
		b, ok := b.(*BuiltinType)
		return ok && a.Name == b.Name
	default:
		// Basic and struct types are identical only if they are the same object.
		return false
	}
}

func bind(v *TypeVar, t Type, trail *[]*TypeVar) bool {
	if occurs(v, t) {
		return false
	}
	v.ref = t
	*trail = append(*trail, v)
	return true
}

// occurs reports whether v occurs in t, which would make an infinite type.
func occurs(v *TypeVar, t Type) bool {
	switch t := prune(t).(type) {
	case *TypeVar:
		return t == v
	case *ArrayType:
		return occurs(v, t.Elem)
	case *FuncType:
		for _, p := range t.Params {
			if occurs(v, p) {
				return true
			}
		}
		return occurs(v, t.Result)
	default:
		return false
	}
}

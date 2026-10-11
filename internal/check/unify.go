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
	case *StructType:
		return &StructType{Def: t.Def, Args: zonkAll(t.Args)}
	case *EnumType:
		return &EnumType{Def: t.Def, Args: zonkAll(t.Args)}
	case *FuncType:
		return &FuncType{Params: zonkAll(t.Params), Result: zonk(t.Result)}
	default:
		return t
	}
}

func zonkAll(ts []Type) []Type {
	var r []Type
	for _, t := range ts {
		r = append(r, zonk(t))
	}
	return r
}

// Env maps type parameters to types.
type Env map[*TypeParam]Type

func bindParams(params []*TypeParam, args []Type) Env {
	env := Env{}
	for i, p := range params {
		env[p] = args[i]
	}
	return env
}

// subst replaces the type parameters in t that env binds.
func subst(t Type, env Env) Type {
	if len(env) == 0 {
		return t
	}
	switch t := prune(t).(type) {
	case *TypeParam:
		if u, ok := env[t]; ok {
			return u
		}
		return t
	case *ArrayType:
		return &ArrayType{Elem: subst(t.Elem, env)}
	case *StructType:
		return &StructType{Def: t.Def, Args: substAll(t.Args, env)}
	case *EnumType:
		return &EnumType{Def: t.Def, Args: substAll(t.Args, env)}
	case *FuncType:
		return &FuncType{Params: substAll(t.Params, env), Result: subst(t.Result, env)}
	default:
		return t
	}
}

func substAll(ts []Type, env Env) []Type {
	var r []Type
	for _, t := range ts {
		r = append(r, subst(t, env))
	}
	return r
}

// Subst replaces the type parameters in t that env binds. The interpreter
// uses it to find the types that a generic function is running with.
func Subst(t Type, env Env) Type {
	return subst(t, env)
}

func fresh() Type {
	return &TypeVar{}
}

// instantiate returns fresh type variables for params.
func instantiate(params []*TypeParam) ([]Type, Env) {
	args := make([]Type, len(params))
	for i := range params {
		args[i] = fresh()
	}
	return args, bindParams(params, args)
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
	case *StructType:
		b, ok := b.(*StructType)
		return ok && a.Def == b.Def && unifyAll(a.Args, b.Args, trail)
	case *EnumType:
		b, ok := b.(*EnumType)
		return ok && a.Def == b.Def && unifyAll(a.Args, b.Args, trail)
	case *FuncType:
		b, ok := b.(*FuncType)
		return ok && len(a.Params) == len(b.Params) &&
			unifyAll(a.Params, b.Params, trail) && unifyRec(a.Result, b.Result, trail)
	case *BuiltinType:
		b, ok := b.(*BuiltinType)
		return ok && a.Name == b.Name
	default:
		// Basic types and type parameters are identical only to themselves.
		return false
	}
}

func unifyAll(as, bs []Type, trail *[]*TypeVar) bool {
	for i := range as {
		if !unifyRec(as[i], bs[i], trail) {
			return false
		}
	}
	return true
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
	case *StructType:
		return occursAny(v, t.Args)
	case *EnumType:
		return occursAny(v, t.Args)
	case *FuncType:
		return occursAny(v, t.Params) || occurs(v, t.Result)
	default:
		return false
	}
}

func occursAny(v *TypeVar, ts []Type) bool {
	for _, t := range ts {
		if occurs(v, t) {
			return true
		}
	}
	return false
}

// tri is the answer to a question that undetermined types may leave open.
type tri int

const (
	no tri = iota
	maybe
	yes
)

// and3 is the conjunction of two answers.
func and3(a, b tri) tri {
	return min(a, b)
}

// match matches t against pattern, whose type parameters in vars stand for
// any type. It binds those parameters in env. The answer is maybe if it
// depends on how variables in t are determined.
func match(pattern, t Type, vars map[*TypeParam]bool, env Env) tri {
	pattern = prune(pattern)
	if p, ok := pattern.(*TypeParam); ok && vars[p] {
		if bound, ok := env[p]; ok {
			return same(bound, t)
		}
		env[p] = t
		return yes
	}
	t = prune(t)
	if pattern == t {
		return yes
	}
	for _, x := range []Type{pattern, t} {
		switch x.(type) {
		case *InvalidType, *NeverType:
			return yes
		}
	}
	for _, x := range []Type{pattern, t} {
		if _, ok := x.(*TypeVar); ok {
			return maybe
		}
	}
	switch p := pattern.(type) {
	case *ArrayType:
		t, ok := t.(*ArrayType)
		if !ok {
			return no
		}
		return match(p.Elem, t.Elem, vars, env)
	case *StructType:
		t, ok := t.(*StructType)
		if !ok || t.Def != p.Def {
			return no
		}
		return matchAll(p.Args, t.Args, vars, env)
	case *EnumType:
		t, ok := t.(*EnumType)
		if !ok || t.Def != p.Def {
			return no
		}
		return matchAll(p.Args, t.Args, vars, env)
	case *FuncType:
		t, ok := t.(*FuncType)
		if !ok || len(t.Params) != len(p.Params) {
			return no
		}
		return and3(matchAll(p.Params, t.Params, vars, env), match(p.Result, t.Result, vars, env))
	default:
		return no
	}
}

func matchAll(ps, ts []Type, vars map[*TypeParam]bool, env Env) tri {
	r := yes
	for i := range ps {
		r = and3(r, match(ps[i], ts[i], vars, env))
	}
	return r
}

// same reports whether a and b are the same type.
func same(a, b Type) tri {
	return match(a, b, nil, nil)
}

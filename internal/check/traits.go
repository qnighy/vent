package check

import (
	"errors"
	"strings"

	"github.com/qnighy/vent/internal/syntax"
)

// obligation is a requirement that typ implement trait, made by node.
type obligation struct {
	node  any
	pos   syntax.Pos
	typ   Type
	trait *Trait
}

// require records that t must implement trait. The obligation is proven
// when the checker reaches node; see prove.
//
// Eq does not affect what the program does, so it is checked once the
// function has been checked, like the operands of ==.
func (c *checker) require(node any, pos syntax.Pos, t Type, trait *Trait) {
	if trait == Eq {
		c.deferred = append(c.deferred, func() {
			if !comparable(t) {
				c.poison(node, pos, "%v does not implement Eq", t)
			}
		})
		return
	}
	c.pending = append(c.pending, obligation{node: node, pos: pos, typ: t, trait: trait})
}

// requireBounds records the bounds of params, instantiated by args.
func (c *checker) requireBounds(node any, pos syntax.Pos, params []*TypeParam, args []Type) {
	for i, p := range params {
		for _, b := range p.Bounds {
			c.require(node, pos, args[i], b)
		}
	}
}

// prove proves the obligations recorded since mark. The types they are
// about must be determined by now, as far as needed to choose impls.
func (c *checker) prove(mark int) {
	obs := append([]obligation(nil), c.pending[mark:]...)
	c.pending = c.pending[:mark]
	for _, ob := range obs {
		switch r, t, trait := c.info.implements(ob.typ, ob.trait, 0); r {
		case no:
			c.poison(ob.node, ob.pos, "%v does not implement %s", t, trait.Name)
		case maybe:
			c.poison(ob.node, ob.pos, "cannot infer which implementation of %s to use for %v; add a type annotation", ob.trait.Name, ob.typ)
		}
	}
}

// maxImplDepth bounds the nesting of impls that a proof goes through.
const maxImplDepth = 32

// implements reports whether t implements trait. On failure it returns
// the type and trait at fault, which may be nested in t.
func (info *Info) implements(t Type, trait *Trait, depth int) (tri, Type, *Trait) {
	t = prune(t)
	switch t := t.(type) {
	case *InvalidType, *NeverType:
		return yes, nil, nil
	case *TypeParam:
		if hasBound(t, trait) {
			return yes, nil, nil
		}
	}
	if trait == Eq {
		if comparable(t) {
			return yes, nil, nil
		}
		return no, t, trait
	}
	if _, ok := t.(*TypeVar); ok {
		return maybe, t, trait
	}
	if depth > maxImplDepth {
		return no, t, trait
	}
	result := no
	for _, impl := range info.impls[trait] {
		env := Env{}
		r := match(impl.Self, t, paramSet(impl.Params), env)
		if r == yes {
			for _, p := range impl.Params {
				for _, b := range p.Bounds {
					rb, bt, btrait := info.implements(env[p], b, depth+1)
					if rb == no {
						return no, bt, btrait
					}
					r = and3(r, rb)
				}
			}
			if r == yes {
				return yes, nil, nil
			}
		}
		if r == maybe {
			result = maybe
		}
	}
	return result, t, trait
}

func paramSet(params []*TypeParam) map[*TypeParam]bool {
	set := map[*TypeParam]bool{}
	for _, p := range params {
		set[p] = true
	}
	return set
}

// findImpl returns the impl of trait that applies to t, and the types its
// type parameters stand for.
func (info *Info) findImpl(trait *Trait, t Type) (*Impl, Env) {
	for _, impl := range info.impls[trait] {
		env := Env{}
		if match(impl.Self, t, paramSet(impl.Params), env) == yes {
			return impl, env
		}
	}
	return nil, nil
}

// ErrNoImpl is returned by Resolve if no impl applies, which the checker
// rules out for refs that checked.
var ErrNoImpl = errors.New("no implementation applies")

// Resolve returns the function that ref designates when the type parameters
// in scope where it appears are bound by env, and the bindings of the type
// parameters in scope of the function's body. If the impl it belongs to
// failed to check, it returns the impl's static error.
func (info *Info) Resolve(ref *Ref, env Env) (*Func, Env, error) {
	args := substAll(ref.TypeArgs, env)
	switch ref.Kind {
	case RefFunc:
		return ref.Func, bindParams(ref.Func.AllTypeParams(), args), nil
	case RefTraitMethod:
		self := subst(ref.Self, env)
		impl, body := info.findImpl(ref.Trait, self)
		if impl == nil {
			return nil, nil, ErrNoImpl
		}
		if err := info.Poisoned[impl.Decl]; err != nil {
			return nil, nil, err
		}
		f := impl.method(ref.Func.Name)
		if f == nil {
			// The default in the trait.
			f = ref.Func
			body[ref.Trait.Self] = self
		}
		for i, p := range f.TypeParams {
			body[p] = args[i]
		}
		return f, body, nil
	default:
		panic("Resolve: not a function")
	}
}

// lookupQual resolves the qualifier of a path: a type or a trait. Generic
// types get fresh type arguments.
func (c *checker) lookupQual(e *syntax.PathExpr) (Type, *Trait) {
	if p := c.tscope[e.Qual]; p != nil {
		return p, nil
	}
	freshArgs := func(n int) []Type {
		args, _ := instantiate(make([]*TypeParam, n))
		return args
	}
	switch e.Qual {
	case "Self":
		if c.self == nil {
			c.poison(e, e.Pos, "Self is only available in traits and impls")
			return Invalid, nil
		}
		return c.self, nil
	case "Int":
		return Int, nil
	case "Bool":
		return Bool, nil
	case "String":
		return String, nil
	case "Array":
		return &ArrayType{Elem: fresh()}, nil
	}
	if def := c.structs[e.Qual]; def != nil {
		return &StructType{Def: def, Args: freshArgs(len(def.Params))}, nil
	}
	if def := c.enums[e.Qual]; def != nil {
		return &EnumType{Def: def, Args: freshArgs(len(def.Params))}, nil
	}
	if trait := c.lookupTrait(e.Qual); trait != nil {
		return nil, trait
	}
	c.poison(e, e.Pos, "undefined type or trait %s", e.Qual)
	return Invalid, nil
}

// lookupAssoc resolves the function or method name associated with type t:
// an inherent method, or a method of a trait that t may implement. It returns
// the reference and its instantiated type, or nil after poisoning node.
func (c *checker) lookupAssoc(node syntax.Expr, pos syntax.Pos, t Type, name string) (*Ref, *FuncType) {
	t = prune(t)
	if key := defOf(t); key != nil {
		for _, f := range c.inherent[key] {
			if f.Name != name {
				continue
			}
			args, env := instantiate(f.AllTypeParams())
			if !unify(subst(f.Impl.Self, env), t) {
				break
			}
			c.requireBounds(node, pos, f.AllTypeParams(), args)
			return &Ref{Kind: RefFunc, Func: f, TypeArgs: args}, subst(f.typ(), env).(*FuncType)
		}
	}

	var candidates []*Trait
	if p, ok := t.(*TypeParam); ok {
		for _, b := range p.Bounds {
			if b.method(name) != nil {
				candidates = append(candidates, b)
			}
		}
	} else {
		for _, trait := range c.traitOrder {
			if trait.method(name) == nil {
				continue
			}
			for _, impl := range c.info.impls[trait] {
				if match(impl.Self, t, paramSet(impl.Params), Env{}) != no {
					candidates = append(candidates, trait)
					break
				}
			}
		}
	}
	switch len(candidates) {
	case 0:
		c.poison(node, pos, "no method %s on %v", name, t)
		return nil, nil
	case 1:
		return c.traitMethod(node, pos, candidates[0], name, t)
	default:
		var names []string
		for _, trait := range candidates {
			names = append(names, trait.Name)
		}
		c.poison(node, pos, "method %s on %v is ambiguous: it is defined by traits %s", name, t, strings.Join(names, ", "))
		return nil, nil
	}
}

// traitMethod refers to the method name of trait, implemented for self.
// It returns the reference and the instantiated type of the method.
func (c *checker) traitMethod(node syntax.Expr, pos syntax.Pos, trait *Trait, name string, self Type) (*Ref, *FuncType) {
	m := trait.method(name)
	args, env := instantiate(m.TypeParams)
	env[trait.Self] = self
	c.require(node, pos, self, trait)
	c.requireBounds(node, pos, m.TypeParams, args)
	return &Ref{Kind: RefTraitMethod, Func: m, Trait: trait, Self: self, TypeArgs: args}, subst(m.typ(), env).(*FuncType)
}

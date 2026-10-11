package check

import (
	"fmt"
	"slices"

	"github.com/qnighy/vent/internal/syntax"
)

// declare collects the top-level declarations and resolves their signatures.
func (c *checker) declare(prog *syntax.Program) {
	// Functions, types and traits share a namespace for now.
	defined := func(name string) bool {
		return slices.Contains(Builtins, name) || slices.Contains(builtinTypes, name) ||
			c.structs[name] != nil || c.enums[name] != nil || c.traits[name] != nil || c.funcs[name] != nil
	}
	declareName := func(pos syntax.Pos, name string) bool {
		if defined(name) {
			c.fatal(pos, "duplicate definition of %s", name)
			return false
		}
		return true
	}
	uniqueNames := func(what, owner string, n int, at func(int) (syntax.Pos, string)) {
		seen := map[string]bool{}
		for i := range n {
			pos, name := at(i)
			if seen[name] {
				c.fatal(pos, "duplicate %s %s in %s", what, name, owner)
			}
			seen[name] = true
		}
	}

	var structs []*StructDef
	for _, st := range prog.Structs {
		if !declareName(st.Pos, st.Name) {
			continue
		}
		uniqueNames("field", st.Name, len(st.Fields), func(i int) (syntax.Pos, string) {
			return st.Fields[i].Pos, st.Fields[i].Name
		})
		def := &StructDef{Decl: st}
		c.structs[st.Name] = def
		structs = append(structs, def)
	}
	var enums []*EnumDef
	for _, en := range prog.Enums {
		if !declareName(en.Pos, en.Name) {
			continue
		}
		uniqueNames("variant", en.Name, len(en.Variants), func(i int) (syntax.Pos, string) {
			return en.Variants[i].Pos, en.Variants[i].Name
		})
		def := &EnumDef{Decl: en}
		c.enums[en.Name] = def
		enums = append(enums, def)
	}
	var traits []*Trait
	for _, tr := range prog.Traits {
		if !declareName(tr.Pos, tr.Name) {
			continue
		}
		uniqueNames("method", tr.Name, len(tr.Methods), func(i int) (syntax.Pos, string) {
			return tr.Methods[i].Pos, tr.Methods[i].Name
		})
		trait := &Trait{Name: tr.Name, Decl: tr}
		trait.Self = &TypeParam{Name: "Self", Bounds: []*Trait{trait}}
		c.traits[tr.Name] = trait
		traits = append(traits, trait)
	}
	var funcs []*Func
	for _, fn := range prog.Fns {
		if !declareName(fn.Pos, fn.Name) {
			continue
		}
		f := &Func{Name: fn.Name, Decl: fn}
		c.funcs[fn.Name] = f
		funcs = append(funcs, f)
	}

	// Resolve signatures once all names are known.
	for _, def := range structs {
		var scope map[string]*TypeParam
		scope, def.Params = c.declareTypeParams(def.Decl, nil, def.Decl.TypeParams, false)
		c.tscope, c.self = scope, nil
		for _, f := range def.Decl.Fields {
			def.Fields = append(def.Fields, c.resolveType(def.Decl, f.Type))
		}
	}
	for _, def := range enums {
		var scope map[string]*TypeParam
		scope, def.Params = c.declareTypeParams(def.Decl, nil, def.Decl.TypeParams, false)
		c.tscope, c.self = scope, nil
		for _, v := range def.Decl.Variants {
			var fields []Type
			for _, f := range v.Fields {
				fields = append(fields, c.resolveType(def.Decl, f))
			}
			def.Variants = append(def.Variants, fields)
		}
	}
	for _, trait := range traits {
		for _, m := range trait.Decl.Methods {
			f := &Func{Name: m.Name, Decl: m, Trait: trait}
			c.declareFunc(f, nil, trait.Self)
			trait.Methods = append(trait.Methods, f)
			if m.Body != nil {
				c.bodies = append(c.bodies, f)
			}
		}
	}
	for _, f := range funcs {
		c.declareFunc(f, nil, nil)
		c.bodies = append(c.bodies, f)
	}
	for _, decl := range prog.Impls {
		c.declareImpl(decl)
	}
	c.traitOrder = traits
	c.checkOverlap(traits)
}

// declareTypeParams creates the type parameters params and returns them
// with a scope extending outer by them.
func (c *checker) declareTypeParams(owner any, outer map[string]*TypeParam, params []*syntax.TypeParam, allowBounds bool) (map[string]*TypeParam, []*TypeParam) {
	scope := map[string]*TypeParam{}
	for name, p := range outer {
		scope[name] = p
	}
	var list []*TypeParam
	seen := map[string]bool{}
	for _, p := range params {
		if seen[p.Name] {
			c.poison(owner, p.Pos, "duplicate type parameter %s", p.Name)
		}
		seen[p.Name] = true
		tp := &TypeParam{Name: p.Name}
		if len(p.Bounds) > 0 && !allowBounds {
			c.poison(owner, p.Bounds[0].Pos, "bounds are not allowed on type parameters of structs and enums")
		}
		for _, b := range p.Bounds {
			if trait := c.lookupTrait(b.Name); trait != nil {
				tp.Bounds = append(tp.Bounds, trait)
			} else {
				c.poison(owner, b.Pos, "undefined trait %s", b.Name)
			}
		}
		scope[p.Name] = tp
		list = append(list, tp)
	}
	return scope, list
}

func (c *checker) lookupTrait(name string) *Trait {
	if name == "Eq" {
		return Eq
	}
	return c.traits[name]
}

// declareFunc resolves the signature of the function f.Decl. Errors poison
// the declaration. outer is the scope of the type parameters of the enclosing
// impl, and self the type of Self, if any.
func (c *checker) declareFunc(f *Func, outer map[string]*TypeParam, self Type) {
	decl := f.Decl
	f.Result, f.self = Unit, self
	f.scope, f.TypeParams = c.declareTypeParams(decl, outer, decl.TypeParams, true)
	c.tscope, c.self = f.scope, self
	for i, param := range decl.Params {
		isSelf := i == 0 && param.Name == "self" && self != nil
		f.HasSelf = f.HasSelf || isSelf
		switch {
		case param.Type != nil:
			f.Params = append(f.Params, c.resolveType(decl, param.Type))
		case isSelf:
			f.Params = append(f.Params, self)
		default:
			c.poison(decl, param.Pos, "missing type annotation for parameter %s", param.Name)
			f.Params = append(f.Params, Invalid)
		}
	}
	if decl.Result != nil {
		f.Result = c.resolveType(decl, decl.Result)
	}
	c.info.Funcs[decl] = f
}

func (c *checker) declareImpl(decl *syntax.ImplDecl) {
	scope, params := c.declareTypeParams(decl, nil, decl.TypeParams, true)
	c.tscope, c.self = scope, nil
	self := c.resolveType(decl, decl.Self)
	if isInvalid(self) {
		return
	}
	impl := &Impl{Decl: decl, Params: params, Self: self}
	used := map[*TypeParam]bool{}
	paramsIn(self, used)
	for i, p := range params {
		if !used[p] {
			c.poison(decl, decl.TypeParams[i].Pos, "type parameter %s is not used in the type %v", p.Name, self)
			return
		}
	}

	if decl.Trait != nil {
		impl.Trait = c.lookupTrait(decl.Trait.Name)
		switch impl.Trait {
		case nil:
			c.poison(decl, decl.Trait.Pos, "undefined trait %s", decl.Trait.Name)
			return
		case Eq:
			c.poison(decl, decl.Trait.Pos, "Eq is implemented by the language and cannot be implemented")
			return
		}
	} else {
		switch self.(type) {
		case *StructType, *EnumType:
		default:
			c.poison(decl, decl.Self.Position(), "inherent methods can only be defined on structs and enums, not %v", self)
			return
		}
	}

	for i, m := range decl.Methods {
		if slices.ContainsFunc(decl.Methods[:i], func(m2 *syntax.FnDecl) bool { return m2.Name == m.Name }) {
			c.fatal(m.Pos, "duplicate definition of method %s", m.Name)
			continue
		}
		f := &Func{Name: m.Name, Decl: m, Impl: impl}
		c.declareFunc(f, scope, self)
		impl.Methods = append(impl.Methods, f)
		c.bodies = append(c.bodies, f)
	}

	if impl.Trait == nil {
		key := defOf(self)
		for _, f := range impl.Methods {
			if slices.ContainsFunc(c.inherent[key], func(g *Func) bool { return g.Name == f.Name }) {
				c.fatal(f.Decl.Pos, "duplicate definition of method %s for %v", f.Name, self)
				continue
			}
			c.inherent[key] = append(c.inherent[key], f)
		}
		return
	}
	c.checkImplMethods(impl)
	c.info.impls[impl.Trait] = append(c.info.impls[impl.Trait], impl)
}

// paramsIn adds the type parameters occurring in t to set.
func paramsIn(t Type, set map[*TypeParam]bool) {
	switch t := prune(t).(type) {
	case *TypeParam:
		set[t] = true
	case *ArrayType:
		paramsIn(t.Elem, set)
	case *StructType:
		for _, a := range t.Args {
			paramsIn(a, set)
		}
	case *EnumType:
		for _, a := range t.Args {
			paramsIn(a, set)
		}
	case *FuncType:
		for _, p := range t.Params {
			paramsIn(p, set)
		}
		paramsIn(t.Result, set)
	}
}

// defOf returns the definition of a struct or enum type.
func defOf(t Type) any {
	switch t := prune(t).(type) {
	case *StructType:
		return t.Def
	case *EnumType:
		return t.Def
	}
	return nil
}

// checkImplMethods checks that the methods of a trait impl match the trait.
// Mismatches poison the impl: calls that it would serve abort.
func (c *checker) checkImplMethods(impl *Impl) {
	trait := impl.Trait
	for _, f := range impl.Methods {
		if trait.method(f.Name) == nil {
			c.poison(impl.Decl, f.Decl.Pos, "method %s is not a member of trait %s", f.Name, trait.Name)
		}
	}
	for _, tm := range trait.Methods {
		f := impl.method(tm.Name)
		if f == nil {
			if tm.Decl.Body == nil {
				c.poison(impl.Decl, impl.Decl.Pos, "missing method %s in the implementation of %s for %v", tm.Name, trait.Name, impl.Self)
			}
			continue
		}
		if len(f.TypeParams) != len(tm.TypeParams) || f.HasSelf != tm.HasSelf {
			c.poison(impl.Decl, f.Decl.Pos, "method %s does not match its declaration in trait %s", f.Name, trait.Name)
			continue
		}
		env := Env{trait.Self: impl.Self}
		for i, p := range tm.TypeParams {
			env[p] = f.TypeParams[i]
			if !sameBounds(p.Bounds, f.TypeParams[i].Bounds) {
				c.poison(impl.Decl, f.Decl.TypeParams[i].Pos, "bounds of %s do not match its declaration in trait %s", p.Name, trait.Name)
			}
		}
		want := subst(tm.typ(), env)
		if same(want, f.typ()) != yes {
			c.poison(impl.Decl, f.Decl.Pos, "method %s has type %v, but trait %s requires %v", f.Name, f.typ(), trait.Name, want)
		}
	}
}

func sameBounds(a, b []*Trait) bool {
	for _, t := range a {
		if !slices.Contains(b, t) {
			return false
		}
	}
	for _, t := range b {
		if !slices.Contains(a, t) {
			return false
		}
	}
	return true
}

// checkOverlap reports impls of the same trait that apply to the same type,
// as the implementation to use would be ambiguous.
func (c *checker) checkOverlap(traits []*Trait) {
	for _, trait := range traits {
		impls := c.info.impls[trait]
		for j, b := range impls {
			for _, a := range impls[:j] {
				_, envA := instantiate(a.Params)
				_, envB := instantiate(b.Params)
				if unify(subst(a.Self, envA), subst(b.Self, envB)) {
					c.fatal(b.Decl.Pos, "conflicting implementations of %s for %v and %v", trait.Name, a.Self, b.Self)
					break
				}
			}
		}
	}
}

// resolveType resolves a type annotation, poisoning owner on failure.
// Type parameters are looked up in c.tscope and Self is c.self.
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
		var args []Type
		for _, a := range t.Args {
			at := c.resolveType(owner, a)
			if isInvalid(at) {
				return Invalid
			}
			args = append(args, at)
		}
		arity := func(n int) bool {
			switch {
			case len(args) == n:
				return true
			case n == 0:
				c.poison(owner, t.Pos, "%s does not take type arguments", t.Name)
			case n == 1:
				c.poison(owner, t.Pos, "%s expects 1 type argument, got %d", t.Name, len(args))
			default:
				c.poison(owner, t.Pos, "%s expects %d type arguments, got %d", t.Name, n, len(args))
			}
			return false
		}
		if p := c.tscope[t.Name]; p != nil {
			if !arity(0) {
				return Invalid
			}
			return p
		}
		var typ Type
		switch t.Name {
		case "Array":
			if !arity(1) {
				return Invalid
			}
			return &ArrayType{Elem: args[0]}
		case "Int":
			typ = Int
		case "Bool":
			typ = Bool
		case "String":
			typ = String
		case "Self":
			if c.self == nil {
				return c.poison(owner, t.Pos, "Self is only available in traits and impls")
			}
			typ = c.self
		default:
			if def := c.structs[t.Name]; def != nil {
				if !arity(len(def.Params)) {
					return Invalid
				}
				return &StructType{Def: def, Args: args}
			}
			if def := c.enums[t.Name]; def != nil {
				if !arity(len(def.Params)) {
					return Invalid
				}
				return &EnumType{Def: def, Args: args}
			}
			if c.lookupTrait(t.Name) != nil {
				return c.poison(owner, t.Pos, "%s is a trait, not a type", t.Name)
			}
			return c.poison(owner, t.Pos, "undefined type %s", t.Name)
		}
		if !arity(0) {
			return Invalid
		}
		return typ
	default:
		panic(fmt.Sprintf("unknown type expression %T", t))
	}
}

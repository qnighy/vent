package check

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/qnighy/vent/internal/syntax"
)

func (c *checker) checkMatch(e *syntax.MatchExpr, want Type) Type {
	x := c.infer(e.X)
	var result Type
	var rows [][]*dpat
	for _, arm := range e.Arms {
		c.scope = &scope{parent: c.scope, vars: map[string]*variable{}}
		ok := c.checkPattern(arm, arm.Pattern, x)
		if arm.Guard != nil {
			c.check(arm.Guard, Bool)
		}
		hint := want
		if hint == nil {
			hint = result
		}
		t := c.inferWant(arm.Body, hint)
		c.scope = c.scope.parent

		switch {
		case result == nil:
			if prune(t) != Never {
				result = t
			}
		case !unify(t, result):
			c.mismatch(arm.Body, result, t)
		}

		switch {
		case !ok:
			// Treated as matching anything, so as not to report missing
			// patterns caused by the error.
			rows = append(rows, []*dpat{{}})
		case arm.Guard == nil:
			rows = append(rows, []*dpat{c.deconstruct(arm.Pattern, x)})
		}
	}
	if prune(x) != Never && !isInvalid(x) {
		if w, ok := missing(rows, []Type{x}); ok {
			c.poison(e, e.Pos, "non-exhaustive patterns: %s not covered", formatPat(w[0], x))
		}
	}
	if result == nil {
		return Never
	}
	return result
}

// checkPattern checks that p matches values of type t, and declares the
// variables it binds in the current scope. Errors poison arm.
func (c *checker) checkPattern(arm *syntax.MatchArm, p syntax.Pattern, t Type) bool {
	fail := func(format string, args ...any) bool {
		c.poison(arm, p.Position(), format, args...)
		c.bindInvalid(arm, p)
		return false
	}
	switch p := p.(type) {
	case *syntax.WildcardPat:
		return true
	case *syntax.BindPat:
		if c.scope.vars[p.Name] != nil {
			c.poison(arm, p.Pos, "variable %s is bound more than once in the same pattern", p.Name)
			return false
		}
		c.scope.vars[p.Name] = &variable{typ: t}
		return true
	case *syntax.LitPat:
		lt := c.infer(p.Value)
		if !unify(t, lt) {
			return fail("mismatched types: expected %v, found %v", t, lt)
		}
		return true
	case *syntax.VariantPat:
		et := c.lookupEnum(p.Enum)
		if et == nil {
			return fail("undefined enum %s", p.Enum)
		}
		i, fields, ok := et.variant(p.Variant)
		if !ok {
			return fail("no variant %s in %s", p.Variant, p.Enum)
		}
		if !unify(t, et) {
			return fail("mismatched types: expected %v, found %v", t, et)
		}
		if len(p.Elems) != len(fields) {
			return fail("%s::%s has %s, but the pattern has %d", p.Enum, p.Variant, plural(len(fields), "field"), len(p.Elems))
		}
		c.info.Refs[p] = &Ref{Kind: RefVariant, Enum: et.Def, Variant: i, TypeArgs: et.Args}
		ok = true
		for i, elem := range p.Elems {
			ok = c.checkPattern(arm, elem, fields[i]) && ok
		}
		return ok
	case *syntax.StructPat:
		st := c.lookupStruct(p.Name)
		if st == nil {
			return fail("undefined struct %s", p.Name)
		}
		if !unify(t, st) {
			return fail("mismatched types: expected %v, found %v", t, st)
		}
		seen := map[string]bool{}
		ok := true
		for _, fp := range p.Fields {
			ft, found := st.field(fp.Name)
			switch {
			case !found:
				c.poison(arm, fp.Pos, "no field %s on %s", fp.Name, p.Name)
				c.bindInvalid(arm, fp.Pattern)
				ok = false
			case seen[fp.Name]:
				c.poison(arm, fp.Pos, "field %s specified more than once", fp.Name)
				c.bindInvalid(arm, fp.Pattern)
				ok = false
			default:
				seen[fp.Name] = true
				ok = c.checkPattern(arm, fp.Pattern, ft) && ok
			}
		}
		if !p.Rest {
			for _, f := range st.Def.Decl.Fields {
				if !seen[f.Name] {
					c.poison(arm, p.Pos, "pattern does not mention field %s; add it or `..`", f.Name)
					ok = false
				}
			}
		}
		c.info.Structs[p] = st.Def
		return ok
	case *syntax.OrPat:
		return c.checkOrPattern(arm, p, t)
	default:
		panic(fmt.Sprintf("unknown pattern %T", p))
	}
}

// checkOrPattern checks the alternatives of p, which must bind variables
// of the same names and types.
func (c *checker) checkOrPattern(arm *syntax.MatchArm, p *syntax.OrPat, t Type) bool {
	outer := c.scope.vars
	var first map[string]*variable
	ok := true
	for i, alt := range p.Alts {
		c.scope.vars = maps.Clone(outer)
		ok = c.checkPattern(arm, alt, t) && ok
		bound := map[string]*variable{}
		for name, v := range c.scope.vars {
			if outer[name] == nil {
				bound[name] = v
			}
		}
		if i == 0 {
			first = bound
			continue
		}
		for name, v := range bound {
			if first[name] == nil {
				c.poison(arm, alt.Position(), "variable %s is not bound in every alternative", name)
				ok = false
			} else if !unify(v.typ, first[name].typ) {
				c.poison(arm, alt.Position(), "mismatched types: variable %s is %v here, but %v in the first alternative", name, v.typ, first[name].typ)
				ok = false
			}
		}
		for name := range first {
			if bound[name] == nil {
				c.poison(arm, alt.Position(), "variable %s is not bound in every alternative", name)
				ok = false
			}
		}
	}
	c.scope.vars = outer
	for name, v := range first {
		outer[name] = v
	}
	return ok
}

// plural formats n things.
func plural(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// bindInvalid declares the variables of a pattern that failed to check, so
// that their uses are not reported as undefined.
func (c *checker) bindInvalid(arm *syntax.MatchArm, p syntax.Pattern) {
	switch p := p.(type) {
	case *syntax.BindPat:
		if c.scope.vars[p.Name] == nil {
			c.scope.vars[p.Name] = &variable{typ: Invalid}
		}
	case *syntax.VariantPat:
		for _, elem := range p.Elems {
			c.bindInvalid(arm, elem)
		}
	case *syntax.StructPat:
		for _, fp := range p.Fields {
			c.bindInvalid(arm, fp.Pattern)
		}
	case *syntax.OrPat:
		c.bindInvalid(arm, p.Alts[0])
	}
}

// lookupEnum resolves the name of an enum in a pattern. Generic enums get
// fresh type arguments.
func (c *checker) lookupEnum(name string) *EnumType {
	if name == "Self" {
		et, _ := prune(c.self).(*EnumType)
		return et
	}
	if p := c.tscope[name]; p != nil {
		return nil
	}
	def := c.enums[name]
	if def == nil {
		return nil
	}
	args, _ := instantiate(def.Params)
	return &EnumType{Def: def, Args: args}
}

// Exhaustiveness is checked by the usefulness algorithm of Maranget,
// "Warnings for pattern matching" (2007), on deconstructed patterns.

// dpat is a deconstructed pattern: a constructor applied to patterns for its
// fields, alternatives if alts is not nil, or else a wildcard if ctor is nil.
// Constructors are compared by key: the variant index of an enum, the value
// of a Bool, Int or String literal, or single{} for structs and ().
type dpat struct {
	ctor any
	args []*dpat
	alts []*dpat
}

type single struct{}

// ctorInfo is a constructor of a type and the types of its fields.
type ctorInfo struct {
	key  any
	args []Type
}

// ctorsOf lists the constructors of t, or reports false if they are too many
// to list, as for Int, or unknown, as for a type parameter.
func ctorsOf(t Type) ([]ctorInfo, bool) {
	switch t := prune(t).(type) {
	case *Basic:
		switch t {
		case Bool:
			return []ctorInfo{{key: true}, {key: false}}, true
		case Unit:
			return []ctorInfo{{key: single{}}}, true
		}
	case *StructType:
		var fields []Type
		for _, f := range t.Def.Decl.Fields {
			ft, _ := t.field(f.Name)
			fields = append(fields, ft)
		}
		return []ctorInfo{{key: single{}, args: fields}}, true
	case *EnumType:
		var ctors []ctorInfo
		for i := range t.Def.Variants {
			ctors = append(ctors, ctorInfo{key: i, args: t.variantFields(i)})
		}
		return ctors, true
	}
	return nil, false
}

// deconstruct converts a pattern that checked against t.
func (c *checker) deconstruct(p syntax.Pattern, t Type) *dpat {
	switch p := p.(type) {
	case *syntax.LitPat:
		switch v := p.Value.(type) {
		case *syntax.IntLit:
			return &dpat{ctor: v.Value}
		case *syntax.StringLit:
			return &dpat{ctor: v.Value}
		case *syntax.BoolLit:
			return &dpat{ctor: v.Value}
		default:
			return &dpat{ctor: single{}}
		}
	case *syntax.VariantPat:
		et, ok := prune(t).(*EnumType)
		if !ok {
			return &dpat{}
		}
		i, fields, _ := et.variant(p.Variant)
		d := &dpat{ctor: i}
		for i, elem := range p.Elems {
			d.args = append(d.args, c.deconstruct(elem, fields[i]))
		}
		return d
	case *syntax.StructPat:
		st, ok := prune(t).(*StructType)
		if !ok {
			return &dpat{}
		}
		d := &dpat{ctor: single{}}
		for _, f := range st.Def.Decl.Fields {
			ft, _ := st.field(f.Name)
			arg := &dpat{}
			for _, fp := range p.Fields {
				if fp.Name == f.Name {
					arg = c.deconstruct(fp.Pattern, ft)
				}
			}
			d.args = append(d.args, arg)
		}
		return d
	case *syntax.OrPat:
		d := &dpat{}
		for _, alt := range p.Alts {
			d.alts = append(d.alts, c.deconstruct(alt, t))
		}
		return d
	default:
		return &dpat{}
	}
}

// expandOr replaces each row starting with alternatives by a row for each.
func expandOr(rows [][]*dpat) [][]*dpat {
	var r [][]*dpat
	for _, row := range rows {
		if row[0].alts == nil {
			r = append(r, row)
			continue
		}
		for _, alt := range row[0].alts {
			r = append(r, expandOr([][]*dpat{slices.Concat([]*dpat{alt}, row[1:])})...)
		}
	}
	return r
}

// missing returns values, one per column of types, that no row matches. It
// reports false if every value is matched.
func missing(rows [][]*dpat, types []Type) ([]*dpat, bool) {
	if len(types) == 0 {
		return nil, len(rows) == 0
	}
	rows = expandOr(rows)
	ctors, finite := ctorsOf(types[0])
	used := map[any]bool{}
	for _, row := range rows {
		if row[0].ctor != nil {
			used[row[0].ctor] = true
		}
	}
	complete := finite && !slices.ContainsFunc(ctors, func(ci ctorInfo) bool { return !used[ci.key] })
	if complete {
		for _, ci := range ctors {
			w, ok := missing(specialize(rows, ci.key, len(ci.args)), slices.Concat(ci.args, types[1:]))
			if ok {
				n := len(ci.args)
				return append([]*dpat{{ctor: ci.key, args: w[:n]}}, w[n:]...), true
			}
		}
		return nil, false
	}
	var rest [][]*dpat
	for _, row := range rows {
		if row[0].ctor == nil {
			rest = append(rest, row[1:])
		}
	}
	w, ok := missing(rest, types[1:])
	if !ok {
		return nil, false
	}
	head := &dpat{}
	if finite && len(used) > 0 {
		for _, ci := range ctors {
			if !used[ci.key] {
				head = &dpat{ctor: ci.key, args: wildcards(len(ci.args))}
				break
			}
		}
	}
	return append([]*dpat{head}, w...), true
}

// specialize keeps the rows that match the constructor key, replacing their
// first pattern by its n fields.
func specialize(rows [][]*dpat, key any, n int) [][]*dpat {
	var r [][]*dpat
	for _, row := range rows {
		switch row[0].ctor {
		case nil:
			r = append(r, slices.Concat(wildcards(n), row[1:]))
		case key:
			r = append(r, slices.Concat(row[0].args, row[1:]))
		}
	}
	return r
}

func wildcards(n int) []*dpat {
	ws := make([]*dpat, n)
	for i := range ws {
		ws[i] = &dpat{}
	}
	return ws
}

// formatPat formats a pattern of type t as source code.
func formatPat(d *dpat, t Type) string {
	if d.ctor == nil {
		return "_"
	}
	switch t := prune(t).(type) {
	case *EnumType:
		i := d.ctor.(int)
		s := t.Def.Decl.Name + "::" + t.Def.Decl.Variants[i].Name
		if len(d.args) == 0 {
			return s
		}
		fields := t.variantFields(i)
		var args []string
		for j, a := range d.args {
			args = append(args, formatPat(a, fields[j]))
		}
		return s + "(" + strings.Join(args, ", ") + ")"
	case *StructType:
		var fields []string
		for j, f := range t.Def.Decl.Fields {
			ft, _ := t.field(f.Name)
			fields = append(fields, f.Name+": "+formatPat(d.args[j], ft))
		}
		if len(fields) == 0 {
			return t.Def.Decl.Name + " {}"
		}
		return t.Def.Decl.Name + " { " + strings.Join(fields, ", ") + " }"
	case *Basic:
		if t == Unit {
			return "()"
		}
	}
	return fmt.Sprint(d.ctor)
}

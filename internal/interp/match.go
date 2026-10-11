package interp

import (
	"fmt"

	"github.com/qnighy/vent/internal/syntax"
)

// evalMatch evaluates a match. The arms are tried in order; an arm whose
// pattern or guard failed to check aborts when it is tried. If the pattern
// of an arm matches in several ways through alternatives, the guard is tried
// for each way in order.
func (in *Interp) evalMatch(e *syntax.MatchExpr, sc *scope) (Value, error) {
	x, err := in.eval(e.X, sc)
	if err != nil {
		return nil, err
	}
	for _, arm := range e.Arms {
		if err := in.poison(arm); err != nil {
			return nil, err
		}
		ways, err := in.matchPattern(arm.Pattern, x)
		if err != nil {
			return nil, err
		}
		for _, bindings := range ways {
			armScope := newScope(sc)
			for _, b := range bindings {
				armScope.define(b.name, share(b.value), false)
			}
			if arm.Guard != nil {
				ok, err := in.evalCond(arm.Guard, armScope)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
			}
			return in.eval(arm.Body, armScope)
		}
	}
	return nil, errorf(e.Pos, "no arm matches %s", debug(x))
}

// patBinding is a variable bound by a pattern.
type patBinding struct {
	name  string
	value Value
}

// matchPattern returns the ways in which v matches p, in order, as the
// variables bound by each.
func (in *Interp) matchPattern(p syntax.Pattern, v Value) ([][]patBinding, error) {
	one := [][]patBinding{nil}
	switch p := p.(type) {
	case *syntax.WildcardPat:
		return one, nil
	case *syntax.BindPat:
		return [][]patBinding{{{name: p.Name, value: v}}}, nil
	case *syntax.LitPat:
		lit, err := in.eval(p.Value, nil)
		if err != nil {
			return nil, err
		}
		if eq, err := equal(p.Pos, v, lit); err != nil || !eq {
			return nil, err
		}
		return one, nil
	case *syntax.VariantPat:
		ref := in.info.Refs[p]
		ev, ok := v.(*EnumValue)
		if ref == nil || !ok || ev.Decl != ref.Enum.Decl {
			return nil, errorf(p.Pos, "cannot match %s against %s::%s", v.TypeName(), p.Enum, p.Variant)
		}
		if ev.Variant != ref.Variant {
			return nil, nil
		}
		return in.matchAll(p.Elems, ev.Fields)
	case *syntax.StructPat:
		st, ok := v.(*StructValue)
		if !ok {
			return nil, errorf(p.Pos, "cannot match %s against %s", v.TypeName(), p.Name)
		}
		var pats []syntax.Pattern
		var vals []Value
		for _, fp := range p.Fields {
			i := fieldIndex(st.Decl, fp.Name)
			if i < 0 {
				return nil, errorf(fp.Pos, "no field %s on %s", fp.Name, st.Decl.Name)
			}
			pats = append(pats, fp.Pattern)
			vals = append(vals, st.Fields[i])
		}
		return in.matchAll(pats, vals)
	case *syntax.OrPat:
		var ways [][]patBinding
		for _, alt := range p.Alts {
			w, err := in.matchPattern(alt, v)
			if err != nil {
				return nil, err
			}
			ways = append(ways, w...)
		}
		return ways, nil
	default:
		panic(fmt.Sprintf("unknown pattern %T", p))
	}
}

// matchAll returns the ways in which each of vs matches the pattern at the
// same index, combined in order.
func (in *Interp) matchAll(ps []syntax.Pattern, vs []Value) ([][]patBinding, error) {
	ways := [][]patBinding{nil}
	for i, p := range ps {
		w, err := in.matchPattern(p, vs[i])
		if err != nil || len(w) == 0 {
			return nil, err
		}
		var next [][]patBinding
		for _, prefix := range ways {
			for _, suffix := range w {
				next = append(next, append(append([]patBinding(nil), prefix...), suffix...))
			}
		}
		ways = next
	}
	return ways, nil
}

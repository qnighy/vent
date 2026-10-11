package syntax

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse parses a whole source file.
func Parse(src []byte) (prog *Program, err error) {
	toks, err := Tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*Error)
			if !ok {
				panic(r)
			}
			prog, err = nil, e
		}
	}()
	return p.parseProgram(), nil
}

type parser struct {
	toks []Token
	i    int
	// loops is the stack of enclosing loops within the current function.
	// An element is true for `loop`, where break may carry a value.
	loops []bool
	// noStructLit forbids struct literals, as in `if x == S {} {}` where the
	// first `{` must start the block. Nested brackets lift the restriction.
	noStructLit bool
}

func (p *parser) tok() Token {
	return p.toks[p.i]
}

func (p *parser) at(kind TokenKind) bool {
	return p.tok().Kind == kind
}

func (p *parser) advance() Token {
	t := p.toks[p.i]
	if t.Kind != EOF {
		p.i++
	}
	return t
}

func (p *parser) errorf(pos Pos, format string, args ...any) {
	panic(&Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

func (p *parser) unexpected(what string) {
	p.errorf(p.tok().Pos, "expected %s, found %s", what, p.tok().describe())
}

func (p *parser) expect(kind TokenKind) Token {
	if !p.at(kind) {
		p.unexpected(fmt.Sprintf("%q", kind.String()))
	}
	return p.advance()
}

func (p *parser) expectIdent() Token {
	if !p.at(Ident) {
		p.unexpected("identifier")
	}
	return p.advance()
}

func (p *parser) parseProgram() *Program {
	prog := &Program{}
	for !p.at(EOF) {
		switch p.tok().Kind {
		case KwFn:
			prog.Fns = append(prog.Fns, p.parseFn(false))
		case KwStruct:
			prog.Structs = append(prog.Structs, p.parseStruct())
		case KwEnum:
			prog.Enums = append(prog.Enums, p.parseEnum())
		case KwTrait:
			prog.Traits = append(prog.Traits, p.parseTrait())
		case KwImpl:
			prog.Impls = append(prog.Impls, p.parseImpl())
		default:
			p.unexpected(`"fn", "struct", "enum", "trait" or "impl"`)
		}
	}
	return prog
}

// parseTypeParams parses an optional `<T: Bound + Bound, U>`.
func (p *parser) parseTypeParams() []*TypeParam {
	if !p.at(Lt) {
		return nil
	}
	p.advance()
	var params []*TypeParam
	for !p.at(Gt) {
		name := p.expectIdent()
		param := &TypeParam{Pos: name.Pos, Name: name.Text}
		if p.at(Colon) {
			p.advance()
			for {
				bound := p.expectIdent()
				param.Bounds = append(param.Bounds, &NamedType{Pos: bound.Pos, Name: bound.Text})
				if !p.at(Plus) {
					break
				}
				p.advance()
			}
		}
		params = append(params, param)
		if !p.at(Comma) {
			break
		}
		p.advance()
	}
	p.expect(Gt)
	return params
}

func (p *parser) parseEnum() *EnumDecl {
	pos := p.expect(KwEnum).Pos
	name := p.expectIdent()
	decl := &EnumDecl{Pos: pos, Name: name.Text, TypeParams: p.parseTypeParams()}
	p.expect(LBrace)
	for !p.at(RBrace) {
		vname := p.expectIdent()
		v := &VariantDecl{Pos: vname.Pos, Name: vname.Text}
		if p.at(LParen) {
			p.advance()
			for !p.at(RParen) {
				v.Fields = append(v.Fields, p.parseType())
				if !p.at(Comma) {
					break
				}
				p.advance()
			}
			p.expect(RParen)
		}
		decl.Variants = append(decl.Variants, v)
		if !p.at(Comma) {
			break
		}
		p.advance()
	}
	p.expect(RBrace)
	return decl
}

func (p *parser) parseTrait() *TraitDecl {
	pos := p.expect(KwTrait).Pos
	name := p.expectIdent()
	decl := &TraitDecl{Pos: pos, Name: name.Text}
	p.expect(LBrace)
	for !p.at(RBrace) {
		if !p.at(KwFn) {
			p.unexpected(`"fn" or "}"`)
		}
		decl.Methods = append(decl.Methods, p.parseFn(true))
	}
	p.expect(RBrace)
	return decl
}

func (p *parser) parseImpl() *ImplDecl {
	pos := p.expect(KwImpl).Pos
	decl := &ImplDecl{Pos: pos, TypeParams: p.parseTypeParams()}
	decl.Self = p.parseType()
	if p.at(KwFor) {
		trait, ok := decl.Self.(*NamedType)
		if !ok || len(trait.Args) != 0 {
			p.errorf(decl.Self.Position(), "expected a trait name")
		}
		p.advance()
		decl.Trait = trait
		decl.Self = p.parseType()
	}
	p.expect(LBrace)
	for !p.at(RBrace) {
		if !p.at(KwFn) {
			p.unexpected(`"fn" or "}"`)
		}
		decl.Methods = append(decl.Methods, p.parseFn(false))
	}
	p.expect(RBrace)
	return decl
}

func (p *parser) parseStruct() *StructDecl {
	pos := p.expect(KwStruct).Pos
	name := p.expectIdent()
	decl := &StructDecl{Pos: pos, Name: name.Text, TypeParams: p.parseTypeParams()}
	p.expect(LBrace)
	for !p.at(RBrace) {
		fname := p.expectIdent()
		p.expect(Colon)
		decl.Fields = append(decl.Fields, &FieldDecl{Pos: fname.Pos, Name: fname.Text, Type: p.parseType()})
		if !p.at(Comma) {
			break
		}
		p.advance()
	}
	p.expect(RBrace)
	return decl
}

// parseFn parses a function. If bodyOptional is set, `;` may replace the body.
func (p *parser) parseFn(bodyOptional bool) *FnDecl {
	pos := p.expect(KwFn).Pos
	name := p.expectIdent()
	fn := &FnDecl{Pos: pos, Name: name.Text, TypeParams: p.parseTypeParams()}

	p.expect(LParen)
	for !p.at(RParen) {
		pname := p.expectIdent()
		param := &Param{Pos: pname.Pos, Name: pname.Text}
		if p.at(Colon) {
			p.advance()
			param.Type = p.parseType()
		}
		fn.Params = append(fn.Params, param)
		if !p.at(Comma) {
			break
		}
		p.advance()
	}
	p.expect(RParen)

	if p.at(Arrow) {
		p.advance()
		fn.Result = p.parseType()
	}

	if bodyOptional && p.at(Semicolon) {
		p.advance()
		return fn
	}
	outerLoops := p.loops
	p.loops = nil
	fn.Body = p.parseBlock()
	p.loops = outerLoops
	return fn
}

func (p *parser) parseType() TypeExpr {
	switch p.tok().Kind {
	case Ident:
		t := p.advance()
		typ := &NamedType{Pos: t.Pos, Name: t.Text}
		if p.at(Lt) {
			p.advance()
			for {
				typ.Args = append(typ.Args, p.parseType())
				if !p.at(Comma) {
					break
				}
				p.advance()
			}
			p.expect(Gt)
		}
		return typ
	case LParen:
		pos := p.advance().Pos
		p.expect(RParen)
		return &UnitType{Pos: pos}
	case KwFn:
		typ := &FuncType{Pos: p.advance().Pos}
		p.expect(LParen)
		for !p.at(RParen) {
			typ.Params = append(typ.Params, p.parseType())
			if !p.at(Comma) {
				break
			}
			p.advance()
		}
		p.expect(RParen)
		if p.at(Arrow) {
			p.advance()
			typ.Result = p.parseType()
		}
		return typ
	default:
		p.unexpected("type")
		panic("unreachable")
	}
}

func (p *parser) parseBlock() *BlockExpr {
	defer p.allowStructLit()()
	block := &BlockExpr{Pos: p.expect(LBrace).Pos}
	for !p.at(RBrace) {
		switch p.tok().Kind {
		case Semicolon:
			p.advance()
		case KwLet, KwVar:
			block.Stmts = append(block.Stmts, p.parseLet())
		case LBrace, KwIf, KwWhile, KwLoop, KwMatch:
			// A block-like expression ends the statement without a semicolon.
			x := p.parseBlockLike()
			if p.at(RBrace) {
				block.Tail = x
			} else {
				if p.at(Semicolon) {
					p.advance()
				}
				block.Stmts = append(block.Stmts, &ExprStmt{X: x})
			}
		default:
			x := p.parseExpr()
			switch {
			case p.at(Semicolon):
				p.advance()
				block.Stmts = append(block.Stmts, &ExprStmt{X: x})
			case p.at(RBrace):
				block.Tail = x
			default:
				p.unexpected(`";" or "}"`)
			}
		}
	}
	p.expect(RBrace)
	return block
}

func (p *parser) parseLet() *LetStmt {
	kw := p.advance()
	name := p.expectIdent()
	s := &LetStmt{Pos: kw.Pos, Mutable: kw.Kind == KwVar, Name: name.Text}
	if p.at(Colon) {
		p.advance()
		s.Type = p.parseType()
	}
	p.expect(Assign)
	s.Value = p.parseExpr()
	p.expect(Semicolon)
	return s
}

func (p *parser) parseExpr() Expr {
	x := p.parseBinary(precLowest)
	switch op := p.tok(); op.Kind {
	case Assign, PlusAssign, MinusAssign, StarAssign, SlashAssign, PercentAssign:
		if !isPlace(x) {
			p.errorf(op.Pos, "invalid assignment target")
		}
		p.advance()
		return &AssignExpr{Pos: op.Pos, Op: op.Kind, Target: x, Value: p.parseExpr()}
	}
	return x
}

func isPlace(x Expr) bool {
	switch x.(type) {
	case *IdentExpr, *IndexExpr, *FieldExpr:
		return true
	default:
		return false
	}
}

// allowStructLit lifts the noStructLit restriction until the returned function is called.
func (p *parser) allowStructLit() (restore func()) {
	old := p.noStructLit
	p.noStructLit = false
	return func() { p.noStructLit = old }
}

// parseCondition parses the condition of if or while, where struct literals are not allowed.
func (p *parser) parseCondition() Expr {
	old := p.noStructLit
	p.noStructLit = true
	defer func() { p.noStructLit = old }()
	return p.parseExpr()
}

// parseExprList parses comma-separated expressions up to (and including) the closing token.
// A trailing comma is allowed.
func (p *parser) parseExprList(end TokenKind) []Expr {
	defer p.allowStructLit()()
	var xs []Expr
	for !p.at(end) {
		xs = append(xs, p.parseExpr())
		if !p.at(Comma) {
			break
		}
		p.advance()
	}
	p.expect(end)
	return xs
}

const (
	precNone = iota
	precLowest
	precOr
	precAnd
	precCompare
	precAdd
	precMul
)

func binaryPrec(kind TokenKind) int {
	switch kind {
	case OrOr:
		return precOr
	case AndAnd:
		return precAnd
	case Eq, Ne, Lt, Le, Gt, Ge:
		return precCompare
	case Plus, Minus:
		return precAdd
	case Star, Slash, Percent:
		return precMul
	default:
		return precNone
	}
}

func (p *parser) parseBinary(minPrec int) Expr {
	x := p.parseUnary()
	compared := false
	for {
		op := p.tok()
		prec := binaryPrec(op.Kind)
		if prec == precNone || prec < minPrec {
			return x
		}
		if prec == precCompare {
			if compared {
				p.errorf(op.Pos, "comparison operators cannot be chained")
			}
			compared = true
		}
		p.advance()
		y := p.parseBinary(prec + 1)
		x = &BinaryExpr{Pos: op.Pos, Op: op.Kind, X: x, Y: y}
	}
}

func (p *parser) parseUnary() Expr {
	switch op := p.tok(); op.Kind {
	case Minus, Bang:
		p.advance()
		return &UnaryExpr{Pos: op.Pos, Op: op.Kind, X: p.parseUnary()}
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() Expr {
	x := p.parsePrimary()
	for {
		switch t := p.tok(); t.Kind {
		case LParen:
			p.advance()
			x = &CallExpr{Pos: t.Pos, Fn: x, Args: p.parseExprList(RParen)}
		case LBracket:
			p.advance()
			restore := p.allowStructLit()
			index := p.parseExpr()
			restore()
			p.expect(RBracket)
			x = &IndexExpr{Pos: t.Pos, X: x, Index: index}
		case Dot:
			p.advance()
			name := p.expectIdent()
			if p.at(LParen) {
				p.advance()
				x = &MethodCallExpr{Pos: name.Pos, Recv: x, Name: name.Text, Args: p.parseExprList(RParen)}
			} else {
				x = &FieldExpr{Pos: name.Pos, X: x, Name: name.Text}
			}
		default:
			return x
		}
	}
}

func (p *parser) parseStructLit(name Token) *StructLit {
	p.expect(LBrace)
	defer p.allowStructLit()()
	lit := &StructLit{Pos: name.Pos, Name: name.Text}
	for !p.at(RBrace) {
		fname := p.expectIdent()
		init := &FieldInit{Pos: fname.Pos, Name: fname.Text}
		if p.at(Colon) {
			p.advance()
			init.Value = p.parseExpr()
		} else {
			init.Value = &IdentExpr{Pos: fname.Pos, Name: fname.Text}
		}
		lit.Fields = append(lit.Fields, init)
		if !p.at(Comma) {
			break
		}
		p.advance()
	}
	p.expect(RBrace)
	return lit
}

func (p *parser) parsePrimary() Expr {
	t := p.tok()
	switch t.Kind {
	case Int:
		p.advance()
		v, err := strconv.ParseInt(strings.ReplaceAll(t.Text, "_", ""), 10, 64)
		if err != nil {
			p.errorf(t.Pos, "integer literal out of range")
		}
		return &IntLit{Pos: t.Pos, Value: v}
	case String:
		p.advance()
		return &StringLit{Pos: t.Pos, Value: t.Text}
	case KwTrue, KwFalse:
		p.advance()
		return &BoolLit{Pos: t.Pos, Value: t.Kind == KwTrue}
	case Ident:
		p.advance()
		if p.at(ColonColon) {
			p.advance()
			name := p.expectIdent()
			return &PathExpr{Pos: t.Pos, Qual: t.Text, Name: name.Text}
		}
		if p.at(LBrace) && !p.noStructLit {
			return p.parseStructLit(t)
		}
		return &IdentExpr{Pos: t.Pos, Name: t.Text}
	case LParen:
		p.advance()
		if p.at(RParen) {
			p.advance()
			return &UnitLit{Pos: t.Pos}
		}
		restore := p.allowStructLit()
		x := p.parseExpr()
		restore()
		p.expect(RParen)
		return x
	case LBracket:
		p.advance()
		return &ArrayLit{Pos: t.Pos, Elems: p.parseExprList(RBracket)}
	case LBrace, KwIf, KwWhile, KwLoop, KwMatch:
		return p.parseBlockLike()
	case KwBreak:
		p.advance()
		if len(p.loops) == 0 {
			p.errorf(t.Pos, "break outside of a loop")
		}
		e := &BreakExpr{Pos: t.Pos}
		if p.canStartExpr() {
			if !p.loops[len(p.loops)-1] {
				p.errorf(t.Pos, "break with a value is only allowed in loop")
			}
			e.Value = p.parseExpr()
		}
		return e
	case KwContinue:
		p.advance()
		if len(p.loops) == 0 {
			p.errorf(t.Pos, "continue outside of a loop")
		}
		return &ContinueExpr{Pos: t.Pos}
	case KwReturn:
		p.advance()
		e := &ReturnExpr{Pos: t.Pos}
		if p.canStartExpr() {
			e.Value = p.parseExpr()
		}
		return e
	default:
		p.unexpected("expression")
		panic("unreachable")
	}
}

func (p *parser) canStartExpr() bool {
	switch p.tok().Kind {
	case Int, String, KwTrue, KwFalse, Ident, LParen, LBracket, LBrace,
		KwIf, KwWhile, KwLoop, KwMatch, KwBreak, KwContinue, KwReturn, Minus, Bang:
		return true
	default:
		return false
	}
}

// parseBlockLike parses a block, if, while, loop or match expression.
func (p *parser) parseBlockLike() Expr {
	t := p.tok()
	switch t.Kind {
	case LBrace:
		return p.parseBlock()
	case KwIf:
		return p.parseIf()
	case KwWhile:
		p.advance()
		cond := p.parseCondition()
		p.loops = append(p.loops, false)
		body := p.parseBlock()
		p.loops = p.loops[:len(p.loops)-1]
		return &WhileExpr{Pos: t.Pos, Cond: cond, Body: body}
	case KwLoop:
		p.advance()
		p.loops = append(p.loops, true)
		body := p.parseBlock()
		p.loops = p.loops[:len(p.loops)-1]
		return &LoopExpr{Pos: t.Pos, Body: body}
	case KwMatch:
		return p.parseMatch()
	default:
		p.unexpected("block")
		panic("unreachable")
	}
}

func (p *parser) parseIf() *IfExpr {
	pos := p.expect(KwIf).Pos
	e := &IfExpr{Pos: pos, Cond: p.parseCondition(), Then: p.parseBlock()}
	if p.at(KwElse) {
		p.advance()
		if p.at(KwIf) {
			e.Else = p.parseIf()
		} else {
			e.Else = p.parseBlock()
		}
	}
	return e
}

func (p *parser) parseMatch() *MatchExpr {
	pos := p.expect(KwMatch).Pos
	e := &MatchExpr{Pos: pos, X: p.parseCondition()}
	p.expect(LBrace)
	defer p.allowStructLit()()
	for !p.at(RBrace) {
		pat := p.parsePattern()
		arm := &MatchArm{Pos: pat.Position(), Pattern: pat}
		if p.at(KwIf) {
			p.advance()
			arm.Guard = p.parseExpr()
		}
		p.expect(FatArrow)
		switch p.tok().Kind {
		case LBrace, KwIf, KwWhile, KwLoop, KwMatch:
			// A block-like body needs no comma.
			arm.Body = p.parseBlockLike()
			if p.at(Comma) {
				p.advance()
			}
		default:
			arm.Body = p.parseExpr()
			if !p.at(RBrace) {
				if !p.at(Comma) {
					p.unexpected(`"," or "}"`)
				}
				p.advance()
			}
		}
		e.Arms = append(e.Arms, arm)
	}
	p.expect(RBrace)
	return e
}

func (p *parser) parsePattern() Pattern {
	pat := p.parsePrimaryPattern()
	if !p.at(Pipe) {
		return pat
	}
	or := &OrPat{Pos: pat.Position(), Alts: []Pattern{pat}}
	for p.at(Pipe) {
		p.advance()
		or.Alts = append(or.Alts, p.parsePrimaryPattern())
	}
	return or
}

func (p *parser) parsePrimaryPattern() Pattern {
	t := p.tok()
	switch t.Kind {
	case Ident:
		p.advance()
		switch {
		case t.Text == "_":
			return &WildcardPat{Pos: t.Pos}
		case p.at(ColonColon):
			p.advance()
			name := p.expectIdent()
			pat := &VariantPat{Pos: t.Pos, Enum: t.Text, Variant: name.Text}
			if p.at(LParen) {
				p.advance()
				for !p.at(RParen) {
					pat.Elems = append(pat.Elems, p.parsePattern())
					if !p.at(Comma) {
						break
					}
					p.advance()
				}
				p.expect(RParen)
			}
			return pat
		case p.at(LBrace):
			return p.parseStructPat(t)
		default:
			return &BindPat{Pos: t.Pos, Name: t.Text}
		}
	case Int, String, KwTrue, KwFalse:
		return &LitPat{Pos: t.Pos, Value: p.parsePrimary()}
	case Minus:
		p.advance()
		n := p.expect(Int)
		v, err := strconv.ParseInt("-"+strings.ReplaceAll(n.Text, "_", ""), 10, 64)
		if err != nil {
			p.errorf(n.Pos, "integer literal out of range")
		}
		return &LitPat{Pos: t.Pos, Value: &IntLit{Pos: t.Pos, Value: v}}
	case LParen:
		p.advance()
		p.expect(RParen)
		return &LitPat{Pos: t.Pos, Value: &UnitLit{Pos: t.Pos}}
	default:
		p.unexpected("pattern")
		panic("unreachable")
	}
}

func (p *parser) parseStructPat(name Token) *StructPat {
	p.expect(LBrace)
	pat := &StructPat{Pos: name.Pos, Name: name.Text}
	for !p.at(RBrace) {
		if p.at(DotDot) {
			p.advance()
			pat.Rest = true
			break
		}
		fname := p.expectIdent()
		fp := &FieldPat{Pos: fname.Pos, Name: fname.Text}
		if p.at(Colon) {
			p.advance()
			fp.Pattern = p.parsePattern()
		} else {
			fp.Pattern = &BindPat{Pos: fname.Pos, Name: fname.Text}
		}
		pat.Fields = append(pat.Fields, fp)
		if !p.at(Comma) {
			break
		}
		p.advance()
	}
	p.expect(RBrace)
	return pat
}

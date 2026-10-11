package syntax

// Program is a whole source file. Each list is in source order.
type Program struct {
	Fns     []*FnDecl
	Structs []*StructDecl
	Enums   []*EnumDecl
	Traits  []*TraitDecl
	Impls   []*ImplDecl
}

// TypeParam is a type parameter with optional trait bounds: `T: A + B`.
type TypeParam struct {
	Pos    Pos
	Name   string
	Bounds []*NamedType
}

// StructDecl is a top-level struct declaration: struct Name<T> { field: Type, ... }
type StructDecl struct {
	Pos        Pos
	Name       string
	TypeParams []*TypeParam
	Fields     []*FieldDecl
}

// EnumDecl is a top-level enum declaration: enum Name<T> { A, B(Type, ...), ... }
type EnumDecl struct {
	Pos        Pos
	Name       string
	TypeParams []*TypeParam
	Variants   []*VariantDecl
}

// VariantDecl is a variant of an enum. Fields are positional.
type VariantDecl struct {
	Pos    Pos
	Name   string
	Fields []TypeExpr
}

// TraitDecl is `trait Name { fn method(self, ...) -> T; ... }`.
// A method with a body is a default implementation.
type TraitDecl struct {
	Pos     Pos
	Name    string
	Methods []*FnDecl
}

// ImplDecl is `impl<T> Trait for Type { ... }`, or `impl<T> Type { ... }`
// for inherent methods (Trait is nil).
type ImplDecl struct {
	Pos        Pos
	TypeParams []*TypeParam
	Trait      *NamedType // nil for inherent impls
	Self       TypeExpr
	Methods    []*FnDecl
}

type FieldDecl struct {
	Pos  Pos
	Name string
	Type TypeExpr
}

// FnDecl is a function declaration: fn name<T>(params) -> Result { Body }
// In a trait or impl, a first parameter named self without a type is the
// receiver, of type Self.
type FnDecl struct {
	Pos        Pos
	Name       string
	TypeParams []*TypeParam
	Params     []*Param
	Result     TypeExpr   // nil if omitted
	Body       *BlockExpr // nil for a trait method without a default
}

type Param struct {
	Pos  Pos
	Name string
	Type TypeExpr // nil if omitted
}

// TypeExpr is a type annotation.
type TypeExpr interface {
	typeNode()
	Position() Pos
}

// NamedType is a type name with optional type arguments, such as Int or Array<Int>.
type NamedType struct {
	Pos  Pos
	Name string
	Args []TypeExpr
}

// UnitType is the type ().
type UnitType struct {
	Pos Pos
}

// FuncType is `fn(Params) -> Result`.
type FuncType struct {
	Pos    Pos
	Params []TypeExpr
	Result TypeExpr // nil if omitted
}

func (*NamedType) typeNode() {}
func (*UnitType) typeNode()  {}
func (*FuncType) typeNode()  {}

func (t *NamedType) Position() Pos { return t.Pos }
func (t *UnitType) Position() Pos  { return t.Pos }
func (t *FuncType) Position() Pos  { return t.Pos }

// Stmt is a statement within a block.
type Stmt interface {
	stmtNode()
	Position() Pos
}

// LetStmt is `let name = value;` or `var name = value;`.
type LetStmt struct {
	Pos     Pos
	Mutable bool // var
	Name    string
	Type    TypeExpr // nil if omitted
	Value   Expr
}

// ExprStmt is an expression evaluated for its effects.
type ExprStmt struct {
	X Expr
}

func (*LetStmt) stmtNode()  {}
func (*ExprStmt) stmtNode() {}

func (s *LetStmt) Position() Pos  { return s.Pos }
func (s *ExprStmt) Position() Pos { return s.X.Position() }

// Expr is an expression.
type Expr interface {
	exprNode()
	Position() Pos
}

type IntLit struct {
	Pos   Pos
	Value int64
}

type StringLit struct {
	Pos   Pos
	Value string
}

type BoolLit struct {
	Pos   Pos
	Value bool
}

// UnitLit is ().
type UnitLit struct {
	Pos Pos
}

type IdentExpr struct {
	Pos  Pos
	Name string
}

type UnaryExpr struct {
	Pos Pos
	Op  TokenKind // Minus, Bang
	X   Expr
}

type BinaryExpr struct {
	Pos  Pos // position of the operator
	Op   TokenKind
	X, Y Expr
}

// AssignExpr is `target = value` or a compound assignment like `target += value`.
// Target is a place expression: an *IdentExpr, *IndexExpr or *FieldExpr.
type AssignExpr struct {
	Pos    Pos       // position of the operator
	Op     TokenKind // Assign, PlusAssign, ...
	Target Expr
	Value  Expr
}

// ArrayLit is `[elems, ...]`.
type ArrayLit struct {
	Pos   Pos
	Elems []Expr
}

// StructLit is `Name { field: value, ... }`.
type StructLit struct {
	Pos    Pos
	Name   string
	Fields []*FieldInit
}

// FieldInit is `name: value` in a struct literal. The shorthand `name` sets Value to an *IdentExpr.
type FieldInit struct {
	Pos   Pos
	Name  string
	Value Expr
}

// IndexExpr is `x[index]`.
type IndexExpr struct {
	Pos   Pos // position of '['
	X     Expr
	Index Expr
}

// FieldExpr is `x.name`.
type FieldExpr struct {
	Pos  Pos // position of the field name
	X    Expr
	Name string
}

// MethodCallExpr is `recv.name(args)`.
type MethodCallExpr struct {
	Pos  Pos // position of the method name
	Recv Expr
	Name string
	Args []Expr
}

type CallExpr struct {
	Pos  Pos // position of '('
	Fn   Expr
	Args []Expr
}

// BlockExpr is `{ stmts; tail }`. Its value is Tail, or () if Tail is nil.
type BlockExpr struct {
	Pos   Pos
	Stmts []Stmt
	Tail  Expr
}

type IfExpr struct {
	Pos  Pos
	Cond Expr
	Then *BlockExpr
	Else Expr // nil, *BlockExpr or *IfExpr
}

type WhileExpr struct {
	Pos  Pos
	Cond Expr
	Body *BlockExpr
}

// LoopExpr is an infinite loop whose value is given by `break value`.
type LoopExpr struct {
	Pos  Pos
	Body *BlockExpr
}

// PathExpr is `Qual::Name`, naming an enum variant, an associated function
// of a type, or a method of a trait.
type PathExpr struct {
	Pos  Pos
	Qual string
	Name string
}

// MatchExpr is `match x { pattern if guard => value, ... }`.
type MatchExpr struct {
	Pos  Pos
	X    Expr
	Arms []*MatchArm
}

type MatchArm struct {
	Pos     Pos
	Pattern Pattern
	Guard   Expr // nil if omitted
	Body    Expr
}

type BreakExpr struct {
	Pos   Pos
	Value Expr // nil if omitted
}

type ContinueExpr struct {
	Pos Pos
}

type ReturnExpr struct {
	Pos   Pos
	Value Expr // nil if omitted
}

func (*PathExpr) exprNode()       {}
func (*MatchExpr) exprNode()      {}
func (*IntLit) exprNode()         {}
func (*StringLit) exprNode()      {}
func (*BoolLit) exprNode()        {}
func (*UnitLit) exprNode()        {}
func (*IdentExpr) exprNode()      {}
func (*UnaryExpr) exprNode()      {}
func (*BinaryExpr) exprNode()     {}
func (*AssignExpr) exprNode()     {}
func (*CallExpr) exprNode()       {}
func (*ArrayLit) exprNode()       {}
func (*StructLit) exprNode()      {}
func (*IndexExpr) exprNode()      {}
func (*FieldExpr) exprNode()      {}
func (*MethodCallExpr) exprNode() {}
func (*BlockExpr) exprNode()      {}
func (*IfExpr) exprNode()         {}
func (*WhileExpr) exprNode()      {}
func (*LoopExpr) exprNode()       {}
func (*BreakExpr) exprNode()      {}
func (*ContinueExpr) exprNode()   {}
func (*ReturnExpr) exprNode()     {}

func (e *PathExpr) Position() Pos       { return e.Pos }
func (e *MatchExpr) Position() Pos      { return e.Pos }
func (e *IntLit) Position() Pos         { return e.Pos }
func (e *StringLit) Position() Pos      { return e.Pos }
func (e *BoolLit) Position() Pos        { return e.Pos }
func (e *UnitLit) Position() Pos        { return e.Pos }
func (e *IdentExpr) Position() Pos      { return e.Pos }
func (e *UnaryExpr) Position() Pos      { return e.Pos }
func (e *BinaryExpr) Position() Pos     { return e.Pos }
func (e *AssignExpr) Position() Pos     { return e.Pos }
func (e *CallExpr) Position() Pos       { return e.Pos }
func (e *ArrayLit) Position() Pos       { return e.Pos }
func (e *StructLit) Position() Pos      { return e.Pos }
func (e *IndexExpr) Position() Pos      { return e.Pos }
func (e *FieldExpr) Position() Pos      { return e.Pos }
func (e *MethodCallExpr) Position() Pos { return e.Pos }
func (e *BlockExpr) Position() Pos      { return e.Pos }
func (e *IfExpr) Position() Pos         { return e.Pos }
func (e *WhileExpr) Position() Pos      { return e.Pos }
func (e *LoopExpr) Position() Pos       { return e.Pos }
func (e *BreakExpr) Position() Pos      { return e.Pos }
func (e *ContinueExpr) Position() Pos   { return e.Pos }
func (e *ReturnExpr) Position() Pos     { return e.Pos }

// Pattern is a pattern in a match arm.
type Pattern interface {
	patternNode()
	Position() Pos
}

// WildcardPat is `_`.
type WildcardPat struct {
	Pos Pos
}

// BindPat binds the matched value to a new immutable variable.
type BindPat struct {
	Pos  Pos
	Name string
}

// LitPat matches a literal. Value is an *IntLit, *StringLit, *BoolLit or *UnitLit.
type LitPat struct {
	Pos   Pos
	Value Expr
}

// VariantPat is `Enum::Variant` or `Enum::Variant(patterns, ...)`.
type VariantPat struct {
	Pos     Pos
	Enum    string
	Variant string
	Elems   []Pattern
}

// StructPat is `Name { field: pattern, field, .. }`. The shorthand `field`
// sets Pattern to a *BindPat. Rest is set if `..` ends the field list.
type StructPat struct {
	Pos    Pos
	Name   string
	Fields []*FieldPat
	Rest   bool
}

// OrPat matches if any of Alts does. Every alternative binds the same
// variables.
type OrPat struct {
	Pos  Pos
	Alts []Pattern
}

type FieldPat struct {
	Pos     Pos
	Name    string
	Pattern Pattern
}

func (*WildcardPat) patternNode() {}
func (*BindPat) patternNode()     {}
func (*LitPat) patternNode()      {}
func (*VariantPat) patternNode()  {}
func (*StructPat) patternNode()   {}
func (*OrPat) patternNode()       {}

func (p *WildcardPat) Position() Pos { return p.Pos }
func (p *BindPat) Position() Pos     { return p.Pos }
func (p *LitPat) Position() Pos      { return p.Pos }
func (p *VariantPat) Position() Pos  { return p.Pos }
func (p *StructPat) Position() Pos   { return p.Pos }
func (p *OrPat) Position() Pos       { return p.Pos }

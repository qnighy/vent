package syntax

// Program is a whole source file.
type Program struct {
	Fns []*FnDecl
}

// FnDecl is a top-level function declaration: fn name(params) -> Result { Body }
type FnDecl struct {
	Pos    Pos
	Name   string
	Params []*Param
	Result TypeExpr // nil if omitted
	Body   *BlockExpr
}

type Param struct {
	Pos  Pos
	Name string
	Type TypeExpr // nil if omitted
}

// TypeExpr is a type annotation. Annotations are parsed but not yet checked.
type TypeExpr interface {
	typeNode()
	Position() Pos
}

type NamedType struct {
	Pos  Pos
	Name string
}

// UnitType is the type ().
type UnitType struct {
	Pos Pos
}

func (*NamedType) typeNode() {}
func (*UnitType) typeNode()  {}

func (t *NamedType) Position() Pos { return t.Pos }
func (t *UnitType) Position() Pos  { return t.Pos }

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
type AssignExpr struct {
	Pos    Pos       // position of the operator
	Op     TokenKind // Assign, PlusAssign, ...
	Target *IdentExpr
	Value  Expr
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

func (*IntLit) exprNode()       {}
func (*StringLit) exprNode()    {}
func (*BoolLit) exprNode()      {}
func (*UnitLit) exprNode()      {}
func (*IdentExpr) exprNode()    {}
func (*UnaryExpr) exprNode()    {}
func (*BinaryExpr) exprNode()   {}
func (*AssignExpr) exprNode()   {}
func (*CallExpr) exprNode()     {}
func (*BlockExpr) exprNode()    {}
func (*IfExpr) exprNode()       {}
func (*WhileExpr) exprNode()    {}
func (*LoopExpr) exprNode()     {}
func (*BreakExpr) exprNode()    {}
func (*ContinueExpr) exprNode() {}
func (*ReturnExpr) exprNode()   {}

func (e *IntLit) Position() Pos       { return e.Pos }
func (e *StringLit) Position() Pos    { return e.Pos }
func (e *BoolLit) Position() Pos      { return e.Pos }
func (e *UnitLit) Position() Pos      { return e.Pos }
func (e *IdentExpr) Position() Pos    { return e.Pos }
func (e *UnaryExpr) Position() Pos    { return e.Pos }
func (e *BinaryExpr) Position() Pos   { return e.Pos }
func (e *AssignExpr) Position() Pos   { return e.Pos }
func (e *CallExpr) Position() Pos     { return e.Pos }
func (e *BlockExpr) Position() Pos    { return e.Pos }
func (e *IfExpr) Position() Pos       { return e.Pos }
func (e *WhileExpr) Position() Pos    { return e.Pos }
func (e *LoopExpr) Position() Pos     { return e.Pos }
func (e *BreakExpr) Position() Pos    { return e.Pos }
func (e *ContinueExpr) Position() Pos { return e.Pos }
func (e *ReturnExpr) Position() Pos   { return e.Pos }

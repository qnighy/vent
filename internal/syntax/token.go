// Package syntax implements the lexer, AST and parser for vent.
package syntax

import "fmt"

// Pos is a 1-based source position. Col counts runes, not bytes.
type Pos struct {
	Line, Col int
}

func (p Pos) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Col)
}

// Error is a lexical or syntactic error.
type Error struct {
	Pos Pos
	Msg string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v: syntax error: %s", e.Pos, e.Msg)
}

type TokenKind int

const (
	EOF TokenKind = iota
	Ident
	Int
	String

	// Keywords
	KwFn
	KwLet
	KwVar
	KwIf
	KwElse
	KwWhile
	KwLoop
	KwBreak
	KwContinue
	KwReturn
	KwTrue
	KwFalse
	KwStruct

	// Punctuation
	LParen
	RParen
	LBrace
	RBrace
	LBracket
	RBracket
	Comma
	Semicolon
	Colon
	Dot
	Arrow

	// Operators
	Plus
	Minus
	Star
	Slash
	Percent
	Bang
	AndAnd
	OrOr
	Eq
	Ne
	Lt
	Le
	Gt
	Ge
	Assign
	PlusAssign
	MinusAssign
	StarAssign
	SlashAssign
	PercentAssign
)

var tokenNames = [...]string{
	EOF:    "end of file",
	Ident:  "identifier",
	Int:    "integer literal",
	String: "string literal",

	KwFn:       "fn",
	KwLet:      "let",
	KwVar:      "var",
	KwIf:       "if",
	KwElse:     "else",
	KwWhile:    "while",
	KwLoop:     "loop",
	KwBreak:    "break",
	KwContinue: "continue",
	KwReturn:   "return",
	KwTrue:     "true",
	KwFalse:    "false",
	KwStruct:   "struct",

	LParen:    "(",
	RParen:    ")",
	LBrace:    "{",
	RBrace:    "}",
	LBracket:  "[",
	RBracket:  "]",
	Comma:     ",",
	Semicolon: ";",
	Colon:     ":",
	Dot:       ".",
	Arrow:     "->",

	Plus:          "+",
	Minus:         "-",
	Star:          "*",
	Slash:         "/",
	Percent:       "%",
	Bang:          "!",
	AndAnd:        "&&",
	OrOr:          "||",
	Eq:            "==",
	Ne:            "!=",
	Lt:            "<",
	Le:            "<=",
	Gt:            ">",
	Ge:            ">=",
	Assign:        "=",
	PlusAssign:    "+=",
	MinusAssign:   "-=",
	StarAssign:    "*=",
	SlashAssign:   "/=",
	PercentAssign: "%=",
}

func (k TokenKind) String() string {
	if int(k) < len(tokenNames) && tokenNames[k] != "" {
		return tokenNames[k]
	}
	return fmt.Sprintf("TokenKind(%d)", int(k))
}

var keywords = map[string]TokenKind{
	"fn":       KwFn,
	"let":      KwLet,
	"var":      KwVar,
	"if":       KwIf,
	"else":     KwElse,
	"while":    KwWhile,
	"loop":     KwLoop,
	"break":    KwBreak,
	"continue": KwContinue,
	"return":   KwReturn,
	"true":     KwTrue,
	"false":    KwFalse,
	"struct":   KwStruct,
}

// Token is a lexical token.
type Token struct {
	Kind TokenKind
	Pos  Pos
	// Text is the source text for Ident and Int, and the decoded value for String.
	Text string
}

func (t Token) describe() string {
	switch t.Kind {
	case Ident, Int:
		return fmt.Sprintf("%v %q", t.Kind, t.Text)
	case String, EOF:
		return t.Kind.String()
	default:
		return fmt.Sprintf("%q", t.Kind.String())
	}
}

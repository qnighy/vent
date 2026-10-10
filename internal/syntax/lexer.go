package syntax

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Tokenize splits src into tokens. The result always ends with an EOF token.
func Tokenize(src []byte) ([]Token, error) {
	l := &lexer{src: src, pos: Pos{Line: 1, Col: 1}}
	var toks []Token
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		toks = append(toks, tok)
		if tok.Kind == EOF {
			return toks, nil
		}
	}
}

type lexer struct {
	src []byte
	off int
	pos Pos
}

// peek returns the rune at the current offset, or -1 at end of input.
func (l *lexer) peek() rune {
	if l.off >= len(l.src) {
		return -1
	}
	r, _ := utf8.DecodeRune(l.src[l.off:])
	return r
}

// peekByte returns the byte n bytes ahead, or 0 if out of range.
func (l *lexer) peekByte(n int) byte {
	if l.off+n >= len(l.src) {
		return 0
	}
	return l.src[l.off+n]
}

func (l *lexer) advance() {
	r, size := utf8.DecodeRune(l.src[l.off:])
	l.off += size
	if r == '\n' {
		l.pos.Line++
		l.pos.Col = 1
	} else {
		l.pos.Col++
	}
}

func (l *lexer) errorf(pos Pos, format string, args ...any) error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

func (l *lexer) skipSpaceAndComments() error {
	for {
		switch l.peek() {
		case ' ', '\t', '\r', '\n':
			l.advance()
		case '/':
			switch l.peekByte(1) {
			case '/':
				for r := l.peek(); r != -1 && r != '\n'; r = l.peek() {
					l.advance()
				}
			case '*':
				if err := l.skipBlockComment(); err != nil {
					return err
				}
			default:
				return nil
			}
		default:
			return nil
		}
	}
}

// skipBlockComment skips a (possibly nested) /* ... */ comment.
func (l *lexer) skipBlockComment() error {
	start := l.pos
	depth := 0
	for {
		switch {
		case l.peek() == -1:
			return l.errorf(start, "unterminated block comment")
		case l.peek() == '/' && l.peekByte(1) == '*':
			l.advance()
			l.advance()
			depth++
		case l.peek() == '*' && l.peekByte(1) == '/':
			l.advance()
			l.advance()
			depth--
			if depth == 0 {
				return nil
			}
		default:
			l.advance()
		}
	}
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r)
}

func (l *lexer) next() (Token, error) {
	if err := l.skipSpaceAndComments(); err != nil {
		return Token{}, err
	}
	start := l.pos
	startOff := l.off
	r := l.peek()
	switch {
	case r == -1:
		return Token{Kind: EOF, Pos: start}, nil
	case r == utf8.RuneError:
		if _, size := utf8.DecodeRune(l.src[l.off:]); size == 1 {
			return Token{}, l.errorf(start, "invalid UTF-8 encoding")
		}
	case isIdentStart(r):
		for isIdentPart(l.peek()) {
			l.advance()
		}
		text := string(l.src[startOff:l.off])
		if kw, ok := keywords[text]; ok {
			return Token{Kind: kw, Pos: start, Text: text}, nil
		}
		return Token{Kind: Ident, Pos: start, Text: text}, nil
	case '0' <= r && r <= '9':
		for r := l.peek(); ('0' <= r && r <= '9') || r == '_'; r = l.peek() {
			l.advance()
		}
		if isIdentPart(l.peek()) {
			return Token{}, l.errorf(l.pos, "unexpected character %q after number", l.peek())
		}
		text := string(l.src[startOff:l.off])
		if len(text) > 1 && text[0] == '0' {
			return Token{}, l.errorf(start, "integer literal must not have leading zeros")
		}
		return Token{Kind: Int, Pos: start, Text: text}, nil
	case r == '"':
		return l.lexString()
	}

	two := func(second byte, double, single TokenKind) Token {
		l.advance()
		if l.peekByte(0) == second {
			l.advance()
			return Token{Kind: double, Pos: start}
		}
		return Token{Kind: single, Pos: start}
	}
	one := func(kind TokenKind) Token {
		l.advance()
		return Token{Kind: kind, Pos: start}
	}
	switch r {
	case '(':
		return one(LParen), nil
	case ')':
		return one(RParen), nil
	case '{':
		return one(LBrace), nil
	case '}':
		return one(RBrace), nil
	case ',':
		return one(Comma), nil
	case ';':
		return one(Semicolon), nil
	case ':':
		return one(Colon), nil
	case '+':
		return two('=', PlusAssign, Plus), nil
	case '-':
		if l.peekByte(1) == '>' {
			l.advance()
			return one(Arrow), nil
		}
		return two('=', MinusAssign, Minus), nil
	case '*':
		return two('=', StarAssign, Star), nil
	case '/':
		return two('=', SlashAssign, Slash), nil
	case '%':
		return two('=', PercentAssign, Percent), nil
	case '!':
		return two('=', Ne, Bang), nil
	case '=':
		return two('=', Eq, Assign), nil
	case '<':
		return two('=', Le, Lt), nil
	case '>':
		return two('=', Ge, Gt), nil
	case '&':
		if l.peekByte(1) == '&' {
			l.advance()
			return one(AndAnd), nil
		}
	case '|':
		if l.peekByte(1) == '|' {
			l.advance()
			return one(OrOr), nil
		}
	}
	return Token{}, l.errorf(start, "unexpected character %q", r)
}

func (l *lexer) lexString() (Token, error) {
	start := l.pos
	l.advance() // opening quote
	var sb strings.Builder
	for {
		escPos := l.pos
		r := l.peek()
		switch r {
		case -1, '\n':
			return Token{}, l.errorf(start, "unterminated string literal")
		case '"':
			l.advance()
			return Token{Kind: String, Pos: start, Text: sb.String()}, nil
		case '\\':
			l.advance()
			e := l.peek()
			switch e {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '0':
				sb.WriteByte(0)
			case '\\', '"', '\'':
				sb.WriteRune(e)
			case 'u':
				l.advance()
				c, err := l.lexUnicodeEscape(escPos)
				if err != nil {
					return Token{}, err
				}
				sb.WriteRune(c)
				continue
			default:
				return Token{}, l.errorf(escPos, "unknown escape sequence")
			}
			l.advance()
		default:
			if _, size := utf8.DecodeRune(l.src[l.off:]); r == utf8.RuneError && size == 1 {
				return Token{}, l.errorf(l.pos, "invalid UTF-8 encoding")
			}
			sb.WriteRune(r)
			l.advance()
		}
	}
}

// lexUnicodeEscape lexes the {XXXX} part of a \u{XXXX} escape.
func (l *lexer) lexUnicodeEscape(escPos Pos) (rune, error) {
	if l.peek() != '{' {
		return 0, l.errorf(escPos, "expected '{' in unicode escape")
	}
	l.advance()
	var c rune
	digits := 0
	for {
		r := l.peek()
		var d rune
		switch {
		case '0' <= r && r <= '9':
			d = r - '0'
		case 'a' <= r && r <= 'f':
			d = r - 'a' + 10
		case 'A' <= r && r <= 'F':
			d = r - 'A' + 10
		case r == '}':
			l.advance()
			if digits == 0 || c > unicode.MaxRune || (0xD800 <= c && c <= 0xDFFF) {
				return 0, l.errorf(escPos, "invalid unicode escape")
			}
			return c, nil
		default:
			return 0, l.errorf(escPos, "invalid unicode escape")
		}
		l.advance()
		digits++
		if digits > 6 {
			return 0, l.errorf(escPos, "invalid unicode escape")
		}
		c = c*16 + d
	}
}

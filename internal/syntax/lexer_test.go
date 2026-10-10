package syntax

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestTokenize(t *testing.T) {
	src := "fn f(x) -> Int {\n  x+=1_000; // comment\n  \"a\\n\" /* */ <= ->\n}"
	toks, err := Tokenize([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []Token{
		{Kind: KwFn, Pos: Pos{1, 1}, Text: "fn"},
		{Kind: Ident, Pos: Pos{1, 4}, Text: "f"},
		{Kind: LParen, Pos: Pos{1, 5}},
		{Kind: Ident, Pos: Pos{1, 6}, Text: "x"},
		{Kind: RParen, Pos: Pos{1, 7}},
		{Kind: Arrow, Pos: Pos{1, 9}},
		{Kind: Ident, Pos: Pos{1, 12}, Text: "Int"},
		{Kind: LBrace, Pos: Pos{1, 16}},
		{Kind: Ident, Pos: Pos{2, 3}, Text: "x"},
		{Kind: PlusAssign, Pos: Pos{2, 4}},
		{Kind: Int, Pos: Pos{2, 6}, Text: "1_000"},
		{Kind: Semicolon, Pos: Pos{2, 11}},
		{Kind: String, Pos: Pos{3, 3}, Text: "a\n"},
		{Kind: Le, Pos: Pos{3, 15}},
		{Kind: Arrow, Pos: Pos{3, 18}},
		{Kind: RBrace, Pos: Pos{4, 1}},
		{Kind: EOF, Pos: Pos{4, 2}},
	}
	if diff := cmp.Diff(want, toks); diff != "" {
		t.Errorf("Tokenize mismatch (-want +got):\n%s", diff)
	}
}

func TestTokenizeErrors(t *testing.T) {
	for _, src := range []string{
		`"abc`,
		`/* unterminated`,
		`0123`,
		`12ab`,
		`"\q"`,
		`"\u{110000}"`,
		`a & b`,
		"\xff",
	} {
		if _, err := Tokenize([]byte(src)); err == nil {
			t.Errorf("Tokenize(%q): expected an error", src)
		}
	}
}

package views

import (
	"testing"

	"audiobook-reader/internal/bundle"
)

func TestRuns(t *testing.T) {
	ws := make([]bundle.Word, 6)
	for i := range ws {
		ws[i] = bundle.Word{T: string(rune('a' + i)), S: float64(i)}
	}
	glossary := []bundle.Entry{{ID: 0, Term: "x", Text: "x"}, {ID: 1, Term: "y", Text: "y"}}
	marks := []bundle.Mark{{B: 0, W: 0, N: 1, G: 1}, {B: 2, W: 1, N: 2, G: 0}, {B: 2, W: 3, N: 1, G: 1}}
	got := runs(ws, 2, marks, glossary) // block 0's mark is skipped
	type want struct {
		first, n int
		entry    string
	}
	exp := []want{{0, 1, ""}, {1, 2, "x"}, {3, 1, "y"}, {4, 2, ""}}
	if len(got) != len(exp) {
		t.Fatalf("got %d runs, want %d: %+v", len(got), len(exp), got)
	}
	for i, r := range got {
		e := exp[i]
		term := ""
		if r.Entry != nil {
			term = r.Entry.Term
		}
		if r.Words[0] != ws[e.first] || len(r.Words) != e.n || term != e.entry {
			t.Errorf("run %d = %+v, want %+v", i, r, e)
		}
	}
	if plain := runs(ws, 1, marks, glossary); len(plain) != 1 || len(plain[0].Words) != 6 {
		t.Errorf("block without marks: %+v", plain)
	}
}

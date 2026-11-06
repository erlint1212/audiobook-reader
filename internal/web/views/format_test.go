package views

import (
	"context"
	"slices"
	"strings"
	"testing"

	"audiobook-reader/internal/bundle"
)

func ws(ts ...string) []bundle.Word {
	out := make([]bundle.Word, len(ts))
	for i, t := range ts {
		out[i] = bundle.Word{T: t, S: float64(i)}
	}
	return out
}

func TestCodeParts(t *testing.T) {
	words := ws("if", "x:", "y()")
	parts := codeParts("  if x:\n\tfoo y()  ", words)
	var gaps []string
	for _, p := range parts {
		gaps = append(gaps, p.Gap)
	}
	if want := []string{"  ", " ", "\n\tfoo ", "  "}; !slices.Equal(gaps, want) {
		t.Errorf("gaps = %q, want %q", gaps, want)
	}
	if parts[3].Word != nil || parts[2].Word.T != "y()" {
		t.Errorf("words misplaced: %+v", parts)
	}
	// A word missing from the text is still shown (never for a valid bundle).
	if got := codeParts("a", ws("a", "zz")); len(got) != 2 || got[1].Word.T != "zz" {
		t.Errorf("missing word dropped: %+v", got)
	}
}

func TestCodeListingEscapes(t *testing.T) {
	blk := bundle.Block{Kind: bundle.KindCode, Text: "a<b &&\n  c", Words: ws("a<b", "&&", "c")}
	var sb strings.Builder
	if err := codeListing(blk).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	want := `<code><span data-s="0.000">a&lt;b</span> <span data-s="1.000">&amp;&amp;</span>` +
		"\n  " + `<span data-s="2.000">c</span></code></pre>`
	if !strings.HasSuffix(sb.String(), want) {
		t.Errorf("listing = %q, want suffix %q", sb.String(), want)
	}
}

func TestTableRows(t *testing.T) {
	blk := bundle.Block{
		Kind:  bundle.KindTable,
		Words: ws("A", "B", "c", "d"),
		Rows:  [][]bundle.Cell{{{N: 1, TH: true}, {N: 1, TH: true}}, {{N: 2}, {N: 0}}},
	}
	rows := tableRows(blk)
	if len(rows) != 2 || !rows[0][0].Head || rows[1][0].Head {
		t.Fatalf("rows = %+v", rows)
	}
	if got := len(rows[1][0].Words); got != 2 {
		t.Errorf("cell words = %d, want 2", got)
	}
	if got := len(rows[1][1].Words); got != 0 {
		t.Errorf("empty cell has %d words", got)
	}
}

func TestBlockRuns(t *testing.T) {
	k := func(kinds ...bundle.BlockKind) []bundle.Block {
		out := make([]bundle.Block, len(kinds))
		for i, kind := range kinds {
			out[i] = bundle.Block{Kind: kind}
		}
		return out
	}
	li, p := bundle.KindListItem, bundle.KindParagraph
	type run struct {
		start, n int
		list     bool
	}
	var got []run
	for _, r := range blockRuns(k(p, li, li, p, li)) {
		got = append(got, run{r.Start, len(r.Blocks), r.List})
	}
	want := []run{{0, 1, false}, {1, 2, true}, {3, 1, false}, {4, 1, true}}
	if !slices.Equal(got, want) {
		t.Errorf("runs = %v, want %v", got, want)
	}
}

func TestIndentAndSpoken(t *testing.T) {
	d := func(n int) *int { return &n }
	if indent(nil) != "" || indent(d(0)) != "" || indent(d(1)) != "pl-6" || indent(d(99)) != "pl-24" {
		t.Error("indent wrong")
	}
	say := func(s string) *string { return &s }
	cases := []struct {
		w    bundle.Word
		want string
	}{
		{bundle.Word{T: "a"}, ""},
		{bundle.Word{T: "a", Say: say("a")}, ""},
		{bundle.Word{T: "$5", Say: say("five dollars")}, "five dollars"},
		{bundle.Word{T: "*", Say: say("")}, "(silent)"},
	}
	for _, c := range cases {
		if got := spoken(c.w); got != c.want {
			t.Errorf("spoken(%+v) = %q, want %q", c.w, got, c.want)
		}
	}
}

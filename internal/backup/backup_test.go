package backup

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"audiobook-reader/internal/notes"
	"audiobook-reader/internal/progress"
)

func stores(t *testing.T) (progress.Store, notes.Store) {
	t.Helper()
	ps, err := progress.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ns, err := notes.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ps, ns
}

var t0 = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func TestRoundTrip(t *testing.T) {
	ps, ns := stores(t)
	if err := ps.Save("demo", progress.Position{Chapter: 2, Time: 9.5, Updated: t0}); err != nil {
		t.Fatal(err)
	}
	if _, err := ns.Add("demo", notes.Note{Chapter: 1, Start: 1, End: 2, Quote: "q", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ns.Add("notes-only", notes.Note{Chapter: 1, Quote: "b"}); err != nil {
		t.Fatal(err)
	}
	e, err := Build(ps, ns, t0)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Encode(&buf, e); err != nil {
		t.Fatal(err)
	}
	got, err := Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Books) != 2 || got.Books["notes-only"].Progress != nil {
		t.Fatalf("decoded = %+v", got)
	}

	ps2, ns2 := stores(t)
	if err := Apply(got, ps2, ns2); err != nil {
		t.Fatal(err)
	}
	pos, ok, err := ps2.Get("demo")
	if err != nil || !ok || pos.Chapter != 2 || pos.Time != 9.5 {
		t.Fatalf("progress after apply = %+v ok %v err %v", pos, ok, err)
	}
	for id, d := range e.Books {
		n, err := ns2.List(id)
		if err != nil || len(n) != len(d.Notes) || n[0].ID != d.Notes[0].ID {
			t.Fatalf("notes %s after apply = %+v, %v", id, n, err)
		}
	}
}

func TestApplyKeepsNewerProgress(t *testing.T) {
	ps, ns := stores(t)
	newer := progress.Position{Chapter: 5, Updated: t0.Add(time.Hour)}
	if err := ps.Save("demo", newer); err != nil {
		t.Fatal(err)
	}
	e := Export{Version: Version, Books: map[string]BookData{
		"demo": {Progress: &progress.Position{Chapter: 1, Updated: t0}},
	}}
	if err := Apply(e, ps, ns); err != nil {
		t.Fatal(err)
	}
	if pos, _, err := ps.Get("demo"); err != nil || pos.Chapter != 5 {
		t.Fatalf("old backup overwrote newer progress: %+v, %v", pos, err)
	}
}

func TestDecodeRejects(t *testing.T) {
	for name, doc := range map[string]string{
		"version":  `{"version": 3, "books": {}}`,
		"zero":     `{"version": 0, "books": {}}`,
		"author":   `{"version": 2, "books": {"demo": {"progress": null, "notes": [{"id": "00000000000000aa", "chapter": 1, "start": 0, "end": 0, "quote": "q", "text": "", "author": "a\nb", "created": "2026-01-01T00:00:00Z", "updated": "2026-01-01T00:00:00Z"}]}}}`,
		"slug":     `{"version": 1, "books": {"../x": {"progress": null, "notes": []}}}`,
		"unknown":  `{"version": 1, "books": {}, "extra": 1}`,
		"trailing": `{"version": 1, "books": {}} {}`,
		"garbage":  `not json`,
	} {
		if _, err := Decode(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: Decode accepted %s", name, doc)
		}
	}
}

func TestDecodeAcceptsV1(t *testing.T) {
	doc := `{"version": 1, "exported": "2026-01-01T00:00:00Z", "books": {"demo": {"progress": null, "notes": []}}}`
	if _, err := Decode(strings.NewReader(doc)); err != nil {
		t.Fatalf("v1 rejected: %v", err)
	}
}

func TestValidAuthor(t *testing.T) {
	for name, want := range map[string]bool{
		"":                      true,
		"Kari Nordmann":         true,
		"Åse ✎":                 true,
		"tab\there":             false,
		strings.Repeat("x", 60): true,
		strings.Repeat("x", 61): false,
		strings.Repeat("æ", 60): true,
		"bad\xffutf8":           false,
	} {
		if got := ValidAuthor(name); got != want {
			t.Errorf("ValidAuthor(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBuildBook(t *testing.T) {
	ps, ns := stores(t)
	if err := ps.Save("demo", progress.Position{Chapter: 2, Updated: t0}); err != nil {
		t.Fatal(err)
	}
	if _, err := ns.Add("demo", notes.Note{Chapter: 1, Quote: "mine", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	fwd := notes.Note{ID: "00000000000000cc", Chapter: 1, Start: 5, End: 5, Quote: "fwd", Author: "Ola", Updated: t0}
	if err := ns.Merge("demo", []notes.Note{fwd}); err != nil {
		t.Fatal(err)
	}
	if _, err := ns.Add("other", notes.Note{Chapter: 1, Quote: "other"}); err != nil {
		t.Fatal(err)
	}
	e, err := BuildBook(ns, "demo", "Kari", t0)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := e.Books["demo"]
	if len(e.Books) != 1 || !ok || d.Progress != nil || len(d.Notes) != 2 {
		t.Fatalf("BuildBook = %+v", e)
	}
	if d.Notes[0].Author != "Kari" || d.Notes[1].Author != "Ola" {
		t.Errorf("authors = %q, %q; want Kari, Ola", d.Notes[0].Author, d.Notes[1].Author)
	}
	if mine, err := ns.List("demo"); err != nil || mine[0].Author != "" {
		t.Errorf("BuildBook changed stored notes: %+v, %v", mine, err)
	}
}

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
		"version":  `{"version": 2, "books": {}}`,
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

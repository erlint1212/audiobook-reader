package notes

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func open(t *testing.T) Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAddListSorted(t *testing.T) {
	s := open(t)
	if ns, err := s.List("demo"); err != nil || len(ns) != 0 {
		t.Fatalf("empty List = %v, %v", ns, err)
	}
	for _, n := range []Note{
		{Chapter: 2, Start: 1, End: 2, Quote: "c"},
		{Chapter: 1, Start: 5, End: 6, Quote: "b"},
		{Chapter: 1, Start: 1, End: 1, Quote: "a"},
	} {
		got, err := s.Add("demo", n)
		if err != nil {
			t.Fatal(err)
		}
		if !ValidID(got.ID) || got.Created.IsZero() {
			t.Errorf("Add returned %+v", got)
		}
	}
	ns, err := s.List("demo")
	if err != nil {
		t.Fatal(err)
	}
	var order string
	for _, n := range ns {
		order += n.Quote
	}
	if order != "abc" {
		t.Errorf("order = %q, want abc", order)
	}
	if !ns[0].Bookmark() {
		t.Error("note without text should be a bookmark")
	}
	if other, err := s.List("other"); err != nil || len(other) != 0 {
		t.Errorf("other book sees notes: %v %v", other, err)
	}
}

func TestUpdateDelete(t *testing.T) {
	s := open(t)
	n, err := s.Add("demo", Note{Chapter: 1, Quote: "q"})
	if err != nil {
		t.Fatal(err)
	}
	up, err := s.Update("demo", n.ID, "hello")
	if err != nil || up.Text != "hello" || up.Bookmark() {
		t.Fatalf("Update = %+v, %v", up, err)
	}
	ns, err := s.List("demo")
	if err != nil || len(ns) != 1 || ns[0].Text != "hello" {
		t.Fatalf("after update List = %+v, %v", ns, err)
	}
	if _, err := s.Update("demo", "0000000000000000", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update unknown = %v", err)
	}
	if err := s.Delete("demo", "0000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete unknown = %v", err)
	}
	if err := s.Delete("demo", n.ID); err != nil {
		t.Fatal(err)
	}
	if ns, err := s.List("demo"); err != nil || len(ns) != 0 {
		t.Fatalf("after delete List = %+v, %v", ns, err)
	}
}

func TestCorruptFile(t *testing.T) {
	s := open(t)
	if err := os.WriteFile(filepath.Join(s.dir, "demo.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List("demo"); err == nil {
		t.Error("want error for corrupt file")
	}
}

func TestValidID(t *testing.T) {
	cases := map[string]bool{
		"0123456789abcdef":  true,
		"0123456789ABCDEF":  false,
		"0123456789abcde":   false,
		"0123456789abcdef0": false,
		"../../etc/passwdx": false,
		"":                  false,
	}
	for id, want := range cases {
		if got := ValidID(id); got != want {
			t.Errorf("ValidID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestMerge(t *testing.T) {
	s := open(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	kept, err := s.Add("demo", Note{Chapter: 1, Quote: "kept", Text: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	in := []Note{
		{ID: kept.ID, Chapter: 1, Quote: "kept", Text: "stale", Updated: old},
		{ID: "00000000000000aa", Chapter: 2, Quote: "new", Updated: old},
	}
	for range 2 { // merging twice changes nothing more
		if err := s.Merge("demo", in); err != nil {
			t.Fatal(err)
		}
	}
	ns, err := s.List("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 2 || ns[0].Text != "mine" || ns[1].ID != "00000000000000aa" {
		t.Fatalf("after merge = %+v", ns)
	}
	newer := []Note{{ID: kept.ID, Chapter: 1, Quote: "kept", Text: "theirs", Updated: kept.Updated.Add(time.Second)}}
	if err := s.Merge("demo", newer); err != nil {
		t.Fatal(err)
	}
	if ns, err := s.List("demo"); err != nil || ns[0].Text != "theirs" {
		t.Fatalf("newer note did not win: %+v, %v", ns, err)
	}
	if err := s.Merge("demo", []Note{{ID: "../evil"}}); err == nil {
		t.Fatal("Merge accepted a bad id")
	}
}

func TestBooks(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"zeta", "alpha"} {
		if _, err := s.Add(id, Note{Chapter: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "Bad Name.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Books()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"alpha", "zeta"}; !slices.Equal(got, want) {
		t.Fatalf("Books = %v, want %v", got, want)
	}
}

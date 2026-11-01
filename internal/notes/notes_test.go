package notes

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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

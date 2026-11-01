package atomicfile

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestStems(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.json", "a.json", "a.json.123.tmp", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "d.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Stems(dir, ".json")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("Stems = %v, want %v", got, want)
	}
	if got, err := Stems(filepath.Join(dir, "missing"), ".json"); err != nil || got != nil {
		t.Fatalf("missing dir: Stems = %v, %v", got, err)
	}
}

func TestWriteOverwritesWithoutLeftovers(t *testing.T) {
	dir := t.TempDir()
	for _, want := range []string{"one", "two"} {
		if err := Write(dir, "f.json", []byte(want)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "f.json"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want 1 (no temp leftovers)", len(entries))
	}
}

func TestWriteMissingDir(t *testing.T) {
	if err := Write(filepath.Join(t.TempDir(), "nope"), "f", []byte("x")); err == nil {
		t.Fatal("want error for missing dir")
	}
}

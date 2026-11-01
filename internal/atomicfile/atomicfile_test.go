package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

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

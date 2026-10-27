package library

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

const golden = "../../testdata/golden-bundle"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestScanGolden(t *testing.T) {
	lib, err := Scan(golden, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(lib.Books()); n != 2 {
		t.Fatalf("got %d books, want 2", n)
	}
	for _, id := range []string{"demo", "figures"} { // schema v1 and v2
		if _, ok := lib.Book(id); !ok {
			t.Fatalf("%s not found", id)
		}
	}
	if _, ok := lib.Book("nope"); ok {
		t.Fatal("found unknown book")
	}
}

func TestScanSkipsInvalidAndRejectsDuplicates(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.CopyFS(filepath.Join(root, name), os.DirFS(filepath.Join(golden, "demo"))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Scan(root, quiet); err == nil {
		t.Fatal("want duplicate id error")
	}

	if err := os.WriteFile(filepath.Join(root, "b", "manifest.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := Scan(root, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(lib.Books()); n != 1 {
		t.Fatalf("got %d books, want 1 (invalid skipped)", n)
	}
}

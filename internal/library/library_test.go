package library

import (
	"encoding/json"
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

// truncateChapters rewrites the manifest in dir to keep only the first n
// chapters, like forge mid-run.
func truncateChapters(t *testing.T, dir string, n int) {
	t.Helper()
	path := filepath.Join(dir, "manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["chapters"] = m["chapters"].([]any)[:n]
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestShelfRefreshPicksUpChaptersAndKeepsLastGood(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(golden, "demo"))); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	truncateChapters(t, dir, 1)

	shelf, err := Open(root, quiet)
	if err != nil {
		t.Fatal(err)
	}
	chapters := func() int {
		b, ok := shelf.Get().Book("demo")
		if !ok {
			t.Fatal("demo missing")
		}
		return len(b.Manifest.Chapters)
	}
	if n := chapters(); n != 1 {
		t.Fatalf("got %d chapters, want 1", n)
	}

	// Forge writes chapter 2.
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), full, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := shelf.Refresh(); err != nil {
		t.Fatal(err)
	}
	if n := chapters(); n != 2 {
		t.Fatalf("after refresh got %d chapters, want 2", n)
	}

	// Half-written manifest: keep the last good book, do not drop it.
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := shelf.Refresh(); err != nil {
		t.Fatal(err)
	}
	if n := chapters(); n != 2 {
		t.Fatalf("broken manifest: got %d chapters, want last good 2", n)
	}

	// A new book shows up too.
	if err := os.CopyFS(filepath.Join(root, "figures"), os.DirFS(filepath.Join(golden, "figures"))); err != nil {
		t.Fatal(err)
	}
	if err := shelf.Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, ok := shelf.Get().Book("figures"); !ok {
		t.Fatal("new book not picked up")
	}
}

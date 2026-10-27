// Package library indexes the bundles found under one root directory.
//
// A Library is built once by Scan and never mutated afterwards, so handlers
// can share it without locks. Restart the server to pick up new books.
package library

import (
	"cmp"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"audiobook-reader/internal/bundle"
)

// Library is an immutable, sorted set of books.
type Library struct {
	books []bundle.Book
	byID  map[string]bundle.Book
}

// Scan loads every direct child of root that holds a manifest. Invalid
// bundles are logged and skipped so one bad book never hides the rest.
// Duplicate ids are an error.
func Scan(root string, log *slog.Logger) (Library, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return Library{}, fmt.Errorf("library: %w", err)
	}
	lib := Library{byID: map[string]bundle.Book{}}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, bundle.ManifestFile)); err != nil {
			continue
		}
		b, err := bundle.Load(dir)
		if err != nil {
			log.Warn("skipping invalid bundle", "dir", dir, "err", err)
			continue
		}
		id := b.Manifest.ID
		if prev, dup := lib.byID[id]; dup {
			return Library{}, fmt.Errorf("library: id %q used by both %s and %s", id, prev.Dir, dir)
		}
		lib.byID[id] = b
		lib.books = append(lib.books, b)
	}
	slices.SortFunc(lib.books, func(a, b bundle.Book) int {
		return cmp.Or(cmp.Compare(a.Manifest.Title, b.Manifest.Title), cmp.Compare(a.Manifest.ID, b.Manifest.ID))
	})
	return lib, nil
}

// Books returns all books sorted by title. The slice is a copy.
func (l Library) Books() []bundle.Book { return slices.Clone(l.books) }

// Book looks a book up by manifest id.
func (l Library) Book(id string) (bundle.Book, bool) {
	b, ok := l.byID[id]
	return b, ok
}

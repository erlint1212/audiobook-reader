// Package library indexes the bundles found under one root directory.
//
// A Library is built once by Scan and never mutated afterwards. A Shelf
// holds the current Library and swaps in a fresh one on Refresh, so new
// books and chapters show up without a restart.
package library

import (
	"cmp"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"

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
	return scan(root, Library{}, log)
}

// scan is Scan, but a bundle that fails to load keeps its version from prev.
// Forge may be mid-write when we look; a half-written book must not vanish.
func scan(root string, prev Library, log *slog.Logger) (Library, error) {
	byDir := map[string]bundle.Book{}
	for _, b := range prev.books {
		byDir[b.Dir] = b
	}
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
			old, had := byDir[dir]
			if !had {
				log.Warn("skipping invalid bundle", "dir", dir, "err", err)
				continue
			}
			log.Warn("bundle invalid, keeping last good version", "dir", dir, "err", err)
			b = old
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

// Shelf holds the current Library. Get is lock-free; Refresh builds a new
// Library and swaps it in whole, so a request never sees a half-built one.
type Shelf struct {
	root string
	log  *slog.Logger
	mu   sync.Mutex // serialises Refresh so an older scan never wins
	cur  atomic.Pointer[Library]
}

// Open scans root once and returns a Shelf holding the result.
func Open(root string, log *slog.Logger) (*Shelf, error) {
	lib, err := Scan(root, log)
	if err != nil {
		return nil, err
	}
	s := &Shelf{root: root, log: log}
	s.cur.Store(&lib)
	return s, nil
}

// Get returns the current Library.
func (s *Shelf) Get() Library { return *s.cur.Load() }

// Refresh rescans root. On error the current Library stays in place.
func (s *Shelf) Refresh() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lib, err := scan(s.root, s.Get(), s.log)
	if err != nil {
		return err
	}
	s.cur.Store(&lib)
	return nil
}

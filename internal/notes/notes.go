// Package notes stores highlights and bookmarks per book, one JSON file per
// book holding every note, replaced atomically on each change.
package notes

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"audiobook-reader/internal/atomicfile"
)

// ErrNotFound is returned for an unknown note id.
var ErrNotFound = errors.New("notes: no such note")

// idLen is the length of a note id: 8 random bytes as hex.
const idLen = 16

// Note marks a passage: words from Start to End (seconds) in Chapter.
// Quote is the passage text, built by the server from the alignment.
// An empty Text makes it a bookmark.
type Note struct {
	ID      string    `json:"id"`
	Chapter int       `json:"chapter"`
	Start   float64   `json:"start"`
	End     float64   `json:"end"`
	Quote   string    `json:"quote"`
	Text    string    `json:"text"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Bookmark reports whether the note has no text of its own.
func (n Note) Bookmark() bool { return n.Text == "" }

// Store keeps notes in dir. Book ids must already be validated slugs (the
// library guarantees this), so they are safe file names. Each change reads,
// edits and rewrites a whole file, so the mutex serialises changes.
type Store struct {
	dir string
	mu  *sync.Mutex
}

// Open creates dir if needed and returns a Store.
func Open(dir string) (Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Store{}, fmt.Errorf("notes: %w", err)
	}
	return Store{dir: dir, mu: &sync.Mutex{}}, nil
}

// List returns the book's notes in reading order (chapter, then start).
func (s Store) List(bookID string) ([]Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(bookID)
}

// Add stores n with a fresh id and creation time and returns it.
func (s Store) Add(bookID string, n Note) (Note, error) {
	id, err := newID()
	if err != nil {
		return Note{}, err
	}
	now := time.Now().UTC()
	n.ID, n.Created, n.Updated = id, now, now
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.read(bookID)
	if err != nil {
		return Note{}, err
	}
	if err := s.write(bookID, append(ns, n)); err != nil {
		return Note{}, err
	}
	return n, nil
}

// Update replaces the text of note id.
func (s Store) Update(bookID, id, text string) (Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.read(bookID)
	if err != nil {
		return Note{}, err
	}
	i := slices.IndexFunc(ns, func(n Note) bool { return n.ID == id })
	if i < 0 {
		return Note{}, ErrNotFound
	}
	ns[i].Text = text
	ns[i].Updated = time.Now().UTC()
	if err := s.write(bookID, ns); err != nil {
		return Note{}, err
	}
	return ns[i], nil
}

// Delete removes note id.
func (s Store) Delete(bookID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.read(bookID)
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(ns, func(n Note) bool { return n.ID == id })
	if len(kept) == len(ns) {
		return ErrNotFound
	}
	return s.write(bookID, kept)
}

// ValidID reports whether id looks like an id from Add: lowercase hex of idLen.
func ValidID(id string) bool {
	if len(id) != idLen {
		return false
	}
	for _, c := range id {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// read loads and sorts the notes; a missing file is no notes. Caller holds mu.
func (s Store) read(bookID string) ([]Note, error) {
	raw, err := os.ReadFile(s.file(bookID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("notes: %w", err)
	}
	var ns []Note
	if err := json.Unmarshal(raw, &ns); err != nil {
		return nil, fmt.Errorf("notes %s: %w", bookID, err)
	}
	slices.SortStableFunc(ns, func(a, b Note) int {
		return cmp.Or(cmp.Compare(a.Chapter, b.Chapter), cmp.Compare(a.Start, b.Start), a.Created.Compare(b.Created))
	})
	return ns, nil
}

// write saves ns atomically. Caller holds mu.
func (s Store) write(bookID string, ns []Note) error {
	raw, err := json.MarshalIndent(ns, "", "  ")
	if err != nil {
		return fmt.Errorf("notes: %w", err)
	}
	if err := atomicfile.Write(s.dir, bookID+".json", raw); err != nil {
		return fmt.Errorf("notes: %w", err)
	}
	return nil
}

func (s Store) file(bookID string) string { return filepath.Join(s.dir, bookID+".json") }

func newID() (string, error) {
	b := make([]byte, idLen/2)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("notes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

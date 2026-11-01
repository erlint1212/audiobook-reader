// Package progress stores the listening position per book as small JSON
// files, one per book, replaced atomically on every save.
package progress

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"audiobook-reader/internal/atomicfile"
)

// Position is where the listener stopped.
type Position struct {
	Chapter int       `json:"chapter"`
	Time    float64   `json:"time"`
	Updated time.Time `json:"updated"`
}

// Store keeps positions in Dir. Book ids must already be validated slugs
// (the library guarantees this), so they are safe file names.
type Store struct {
	dir string
}

// Open creates dir if needed and returns a Store.
func Open(dir string) (Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Store{}, fmt.Errorf("progress: %w", err)
	}
	return Store{dir: dir}, nil
}

// Get returns the saved position; ok is false when none exists yet.
func (s Store) Get(bookID string) (pos Position, ok bool, err error) {
	raw, err := os.ReadFile(s.file(bookID))
	if errors.Is(err, os.ErrNotExist) {
		return Position{}, false, nil
	}
	if err != nil {
		return Position{}, false, fmt.Errorf("progress: %w", err)
	}
	if err := json.Unmarshal(raw, &pos); err != nil {
		return Position{}, false, fmt.Errorf("progress %s: %w", bookID, err)
	}
	return pos, true, nil
}

// Save writes pos atomically, so a crash never leaves a torn file.
func (s Store) Save(bookID string, pos Position) error {
	raw, err := json.Marshal(pos)
	if err != nil {
		return fmt.Errorf("progress: %w", err)
	}
	if err := atomicfile.Write(s.dir, bookID+".json", raw); err != nil {
		return fmt.Errorf("progress: %w", err)
	}
	return nil
}

func (s Store) file(bookID string) string { return filepath.Join(s.dir, bookID+".json") }

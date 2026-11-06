// Package progress stores the listening position per book as small JSON
// files, one per book, replaced atomically on every save. Besides the resume
// position it keeps a Mark per chapter: how far it was heard and whether it
// was finished.
package progress

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"audiobook-reader/internal/atomicfile"
	"audiobook-reader/internal/bundle"
)

// Rules for chapter marks. A save below StartedMin seconds leaves the chapter
// unstarted: opening it, or a blink of play, does not count. A save within
// DoneSlack seconds of the end finishes it.
const (
	StartedMin = 1.0
	DoneSlack  = 2.0
)

// Position is where the listener stopped, plus a Mark per chapter index.
type Position struct {
	Chapter  int          `json:"chapter"`
	Time     float64      `json:"time"`
	Updated  time.Time    `json:"updated"`
	Chapters map[int]Mark `json:"chapters,omitempty"`
}

// Mark is one chapter's state. Furthest only grows and Done stays set, so
// listening again or seeking back never loses what was heard.
type Mark struct {
	Furthest float64 `json:"furthest"`
	Done     bool    `json:"done"`
}

// Fraction is how much of a chapter of length dur was heard, 0..1.
func (m Mark) Fraction(dur float64) float64 {
	if m.Done {
		return 1
	}
	if dur <= 0 {
		return 0
	}
	return min(1, max(0, m.Furthest/dur))
}

// Started reports whether the chapter was heard at all.
func (m Mark) Started() bool { return m.Done || m.Furthest > 0 }

// merge combines two marks of the same chapter without losing either.
func (m Mark) merge(o Mark) Mark {
	return Mark{Furthest: max(m.Furthest, o.Furthest), Done: m.Done || o.Done}
}

// Store keeps positions in Dir. Book ids must already be validated slugs
// (the library guarantees this), so they are safe file names. Changes that
// read the old file first hold mu, so concurrent saves cannot drop a mark.
type Store struct {
	dir string
	mu  *sync.Mutex
}

// Open creates dir if needed and returns a Store.
func Open(dir string) (Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Store{}, fmt.Errorf("progress: %w", err)
	}
	return Store{dir: dir, mu: &sync.Mutex{}}, nil
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

// Save writes pos atomically, so a crash never leaves a torn file. It
// replaces everything, marks included; use Record or Merge to keep them.
func (s Store) Save(bookID string, pos Position) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(bookID, pos)
}

// Record saves the listener at time t of chapter ch (length dur) and updates
// that chapter's mark by the StartedMin and DoneSlack rules.
func (s Store) Record(bookID string, ch int, t, dur float64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pos, _, err := s.Get(bookID)
	if err != nil {
		return err
	}
	pos.Chapter, pos.Time, pos.Updated = ch, t, now.UTC()
	if t >= StartedMin {
		m := Mark{Furthest: t, Done: t >= dur-DoneSlack}
		pos.Chapters = withMark(pos.Chapters, ch, m)
	}
	return s.save(bookID, pos)
}

// Merge folds an imported position in: marks always merge, and the resume
// position is taken only when in is newer than what is stored.
func (s Store) Merge(bookID string, in Position) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok, err := s.Get(bookID)
	if err != nil {
		return err
	}
	out := cur
	if !ok || in.Updated.After(cur.Updated) {
		out.Chapter, out.Time, out.Updated = in.Chapter, in.Time, in.Updated
	}
	for ch, m := range in.Chapters {
		out.Chapters = withMark(out.Chapters, ch, m)
	}
	return s.save(bookID, out)
}

// withMark returns a copy of marks with m merged into chapter ch, so a
// Position read by someone else is never changed in place.
func withMark(marks map[int]Mark, ch int, m Mark) map[int]Mark {
	out := maps.Clone(marks)
	if out == nil {
		out = map[int]Mark{}
	}
	out[ch] = out[ch].merge(m)
	return out
}

func (s Store) save(bookID string, pos Position) error {
	raw, err := json.Marshal(pos)
	if err != nil {
		return fmt.Errorf("progress: %w", err)
	}
	if err := atomicfile.Write(s.dir, bookID+".json", raw); err != nil {
		return fmt.Errorf("progress: %w", err)
	}
	return nil
}

// Books lists the ids of books with a saved position, sorted. Files whose
// name is not a slug are not ours and are skipped.
func (s Store) Books() ([]string, error) {
	stems, err := atomicfile.Stems(s.dir, ".json")
	if err != nil {
		return nil, fmt.Errorf("progress: %w", err)
	}
	return slices.DeleteFunc(stems, func(id string) bool { return !bundle.IsSlug(id) }), nil
}

func (s Store) file(bookID string) string { return filepath.Join(s.dir, bookID+".json") }

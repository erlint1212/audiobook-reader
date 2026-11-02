// Package backup turns all progress and notes into one JSON document and
// merges such a document back. It is the user's way to export and keep
// their data outside the -data dir.
package backup

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"audiobook-reader/internal/bundle"
	"audiobook-reader/internal/notes"
	"audiobook-reader/internal/progress"
)

// Version is the export format version. Bump it on a breaking change and
// keep Decode reading the old one. Version 2 added notes' "author";
// a version 1 file is a version 2 file without authors.
const (
	Version    = 2
	minVersion = 1
)

// MaxAuthorRunes caps the sender name stamped on shared notes.
const MaxAuthorRunes = 60

// ValidAuthor reports whether name is fit to stamp on notes: at most
// MaxAuthorRunes runes, no control characters. Empty means "mine".
func ValidAuthor(name string) bool {
	if utf8.RuneCountInString(name) > MaxAuthorRunes || !utf8.ValidString(name) {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}

// Export is the whole document. Books holds every book with data on disk,
// in the library or not.
type Export struct {
	Version  int                 `json:"version"`
	Exported time.Time           `json:"exported"`
	Books    map[string]BookData `json:"books"`
}

// BookData is one book's state. Progress is nil when none was saved.
type BookData struct {
	Progress *progress.Position `json:"progress"`
	Notes    []notes.Note       `json:"notes"`
}

// Build gathers everything from both stores.
func Build(ps progress.Store, ns notes.Store, now time.Time) (Export, error) {
	pIDs, err := ps.Books()
	if err != nil {
		return Export{}, fmt.Errorf("backup: %w", err)
	}
	nIDs, err := ns.Books()
	if err != nil {
		return Export{}, fmt.Errorf("backup: %w", err)
	}
	e := Export{Version: Version, Exported: now.UTC(), Books: map[string]BookData{}}
	ids := append(pIDs, nIDs...)
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		var d BookData
		pos, ok, err := ps.Get(id)
		if err != nil {
			return Export{}, fmt.Errorf("backup: %w", err)
		}
		if ok {
			d.Progress = &pos
		}
		if d.Notes, err = ns.List(id); err != nil {
			return Export{}, fmt.Errorf("backup: %w", err)
		}
		e.Books[id] = d
	}
	return e, nil
}

// Encode writes e as indented JSON.
func Encode(w io.Writer, e Export) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// Decode reads and checks one document. Unknown fields, trailing data, a
// foreign version or a book id that is not a slug are errors.
func Decode(r io.Reader) (Export, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var e Export
	if err := dec.Decode(&e); err != nil {
		return Export{}, fmt.Errorf("backup: %w", err)
	}
	if dec.More() {
		return Export{}, fmt.Errorf("backup: trailing data after document")
	}
	if e.Version < minVersion || e.Version > Version {
		return Export{}, fmt.Errorf("backup: unsupported version %d", e.Version)
	}
	for id, d := range e.Books {
		if !bundle.IsSlug(id) {
			return Export{}, fmt.Errorf("backup: invalid book id %q", id)
		}
		for _, n := range d.Notes {
			if !ValidAuthor(n.Author) {
				return Export{}, fmt.Errorf("backup: invalid author on note %q", n.ID)
			}
		}
	}
	return e, nil
}

// BuildBook gathers one book's notes for sending to someone else: no
// progress, and the user's own notes (empty Author) stamped with author.
// Notes that already have an author keep it, so forwarding keeps credit.
// author must pass ValidAuthor.
func BuildBook(ns notes.Store, bookID, author string, now time.Time) (Export, error) {
	list, err := ns.List(bookID)
	if err != nil {
		return Export{}, fmt.Errorf("backup: %w", err)
	}
	for i := range list {
		if list[i].Author == "" {
			list[i].Author = author
		}
	}
	return Export{
		Version:  Version,
		Exported: now.UTC(),
		Books:    map[string]BookData{bookID: {Notes: list}},
	}, nil
}

// Apply merges e into the stores and never deletes: a position is replaced
// only by a later one, and notes merge by id (see notes.Store.Merge). So
// importing an old backup cannot lose newer data. e must come from Decode.
func Apply(e Export, ps progress.Store, ns notes.Store) error {
	for _, id := range slices.Sorted(maps.Keys(e.Books)) {
		d := e.Books[id]
		if err := ns.Merge(id, d.Notes); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		if d.Progress == nil {
			continue
		}
		cur, ok, err := ps.Get(id)
		if err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		if ok && !d.Progress.Updated.After(cur.Updated) {
			continue
		}
		if err := ps.Save(id, *d.Progress); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
	}
	return nil
}

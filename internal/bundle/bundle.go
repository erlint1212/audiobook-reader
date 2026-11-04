// Package bundle reads and validates the on-disk audiobook bundle format
// (v1; v2 adds picture blocks; v3 adds the glossary from the book's index
// and per-chapter marks of where its terms occur).
//
// The format is the contract with audiobook-forge. Its JSON Schemas live in
// schema/bundle.v{1,2,3}.schema.json; this package mirrors them with Go types and
// adds the semantic rules a schema cannot express (ordering, file existence).
package bundle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersion is the newest bundle version this reader understands.
// MinSchemaVersion is the oldest it still loads.
const (
	SchemaVersion    = 3
	MinSchemaVersion = 1
	// glossaryVersion is the first version with a glossary and marks.
	glossaryVersion = 3
)

// ManifestFile is the manifest's file name inside a bundle directory.
const ManifestFile = "manifest.json"

// durationSlack is how far past duration_s a word may end, to absorb encoder
// padding.
const durationSlack = 0.5

// BlockKind tells the reader how to render a block.
type BlockKind string

const (
	KindParagraph BlockKind = "p"
	KindHeading   BlockKind = "h"
	// KindFigure is a picture (schema v2). Its words are the caption, else the
	// alt text, and may be empty.
	KindFigure BlockKind = "fig"
)

// Manifest describes one book.
type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Author        string    `json:"author"`
	Language      string    `json:"language"`
	Voice         string    `json:"voice"`
	Created       time.Time `json:"created"`
	Cover         string    `json:"cover,omitempty"`
	Glossary      string    `json:"glossary,omitempty"` // v3
	Chapters      []Chapter `json:"chapters"`
}

// Chapter points at one chapter's audio and alignment files, and (v3) at
// its glossary marks.
type Chapter struct {
	Index     int     `json:"index"`
	Title     string  `json:"title"`
	Audio     string  `json:"audio"`
	Alignment string  `json:"alignment"`
	DurationS float64 `json:"duration_s"`
	Marks     string  `json:"marks,omitempty"`
}

// Glossary holds the hover entries forge made from the book's index.
type Glossary struct {
	SchemaVersion int     `json:"schema_version"`
	Entries       []Entry `json:"entries"`
}

// Entry is one hover box: the index term, its explanation (a definition, or
// the sentence the index points at), that sentence's section, sub-entries.
type Entry struct {
	ID     int      `json:"id"`
	Term   string   `json:"term"`
	Text   string   `json:"text"`
	Source string   `json:"source,omitempty"`
	See    []string `json:"see,omitempty"`
}

// Marks lists where glossary terms occur in one chapter.
type Marks struct {
	SchemaVersion int    `json:"schema_version"`
	ChapterIndex  int    `json:"chapter_index"`
	Marks         []Mark `json:"marks"`
}

// Mark says words W..W+N-1 of paragraph block B are an occurrence of
// glossary entry G.
type Mark struct {
	B int `json:"b"`
	W int `json:"w"`
	N int `json:"n"`
	G int `json:"g"`
}

// Alignment holds word timings for one chapter.
type Alignment struct {
	SchemaVersion int     `json:"schema_version"`
	ChapterIndex  int     `json:"chapter_index"`
	Blocks        []Block `json:"blocks"`
}

// Block is a paragraph, heading or picture. Image is set only on pictures.
type Block struct {
	Kind  BlockKind `json:"kind"`
	Image string    `json:"image,omitempty"`
	Words []Word    `json:"words"`
}

// Word is one displayed word with start/end time in seconds.
type Word struct {
	T string  `json:"t"`
	S float64 `json:"s"`
	E float64 `json:"e"`
}

// Book is a validated bundle rooted at Dir. Glossary is empty unless the
// manifest names one; treat it as read-only.
type Book struct {
	Dir      string
	Manifest Manifest
	Glossary []Entry
}

// ErrNoChapter is returned for an unknown chapter index.
var ErrNoChapter = errors.New("bundle: no such chapter")

// Load reads and validates the bundle in dir. Alignments are checked lazily
// by LoadAlignment because they can be large.
func Load(dir string) (Book, error) {
	var m Manifest
	if err := decodeStrict(filepath.Join(dir, ManifestFile), &m); err != nil {
		return Book{}, err
	}
	if err := m.Validate(); err != nil {
		return Book{}, fmt.Errorf("bundle %s: %w", dir, err)
	}
	b := Book{Dir: dir, Manifest: m}
	for _, ch := range m.Chapters {
		rels := []string{ch.Audio, ch.Alignment}
		if ch.Marks != "" {
			rels = append(rels, ch.Marks)
		}
		for _, rel := range rels {
			if _, err := os.Stat(b.path(rel)); err != nil {
				return Book{}, fmt.Errorf("bundle %s: chapter %d: %w", dir, ch.Index, err)
			}
		}
	}
	if m.Glossary != "" {
		var g Glossary
		if err := decodeStrict(b.path(m.Glossary), &g); err != nil {
			return Book{}, fmt.Errorf("bundle %s: glossary: %w", dir, err)
		}
		if err := g.Validate(m.SchemaVersion); err != nil {
			return Book{}, fmt.Errorf("bundle %s: glossary: %w", dir, err)
		}
		b.Glossary = g.Entries
	}
	if m.Cover != "" {
		if _, err := os.Stat(b.path(m.Cover)); err != nil {
			return Book{}, fmt.Errorf("bundle %s: cover: %w", dir, err)
		}
	}
	return b, nil
}

// Chapter returns the chapter with the given 1-based index.
func (b Book) Chapter(index int) (Chapter, error) {
	if index < 1 || index > len(b.Manifest.Chapters) {
		return Chapter{}, ErrNoChapter
	}
	return b.Manifest.Chapters[index-1], nil
}

// AudioPath returns the absolute path of a chapter's audio file.
func (b Book) AudioPath(ch Chapter) string { return b.path(ch.Audio) }

// CoverPath returns the cover's path, or "" when the book has none.
func (b Book) CoverPath() string {
	if b.Manifest.Cover == "" {
		return ""
	}
	return b.path(b.Manifest.Cover)
}

// ImagePath returns the absolute path of a picture block's image, or "" for
// other blocks. blk must come from a validated alignment.
func (b Book) ImagePath(blk Block) string {
	if blk.Kind != KindFigure {
		return ""
	}
	return b.path(blk.Image)
}

// LoadAlignment reads and validates a chapter's alignment, including that
// every picture's image file exists.
func (b Book) LoadAlignment(ch Chapter) (Alignment, error) {
	var a Alignment
	if err := decodeStrict(b.path(ch.Alignment), &a); err != nil {
		return Alignment{}, err
	}
	fail := func(err error) (Alignment, error) {
		return Alignment{}, fmt.Errorf("bundle %s: chapter %d: %w", b.Dir, ch.Index, err)
	}
	if a.SchemaVersion != b.Manifest.SchemaVersion {
		return fail(fmt.Errorf("schema_version %d, manifest has %d", a.SchemaVersion, b.Manifest.SchemaVersion))
	}
	if err := a.Validate(ch); err != nil {
		return fail(err)
	}
	for bi, blk := range a.Blocks {
		if blk.Kind != KindFigure {
			continue
		}
		if _, err := os.Stat(b.ImagePath(blk)); err != nil {
			return fail(fmt.Errorf("block %d: %w", bi, err))
		}
	}
	return a, nil
}

// LoadMarks reads a chapter's glossary marks and checks them against its
// validated alignment a. A chapter without marks (v1/v2 books, or no index)
// gives nil.
func (b Book) LoadMarks(ch Chapter, a Alignment) ([]Mark, error) {
	if ch.Marks == "" {
		return nil, nil
	}
	var m Marks
	if err := decodeStrict(b.path(ch.Marks), &m); err != nil {
		return nil, err
	}
	if err := m.Validate(ch, a, len(b.Glossary)); err != nil {
		return nil, fmt.Errorf("bundle %s: chapter %d: marks: %w", b.Dir, ch.Index, err)
	}
	return m.Marks, nil
}

// Validate checks that the glossary has the manifest's version and that each
// entry's id is its position.
func (g Glossary) Validate(version int) error {
	if g.SchemaVersion != version {
		return fmt.Errorf("schema_version %d, manifest has %d", g.SchemaVersion, version)
	}
	for i, e := range g.Entries {
		switch {
		case e.ID != i:
			return fmt.Errorf("entry %d has id %d", i, e.ID)
		case e.Term == "" || e.Text == "":
			return fmt.Errorf("entry %d: empty term or text", i)
		}
	}
	return nil
}

// Validate checks marks rules: inside paragraph blocks of a, pointing at one
// of entries glossary entries, sorted and never overlapping.
func (m Marks) Validate(ch Chapter, a Alignment, entries int) error {
	if m.SchemaVersion != a.SchemaVersion {
		return fmt.Errorf("schema_version %d, alignment has %d", m.SchemaVersion, a.SchemaVersion)
	}
	if m.ChapterIndex != ch.Index {
		return fmt.Errorf("chapter_index %d, want %d", m.ChapterIndex, ch.Index)
	}
	endB, endW := -1, 0 // first free word after the previous mark
	for i, mk := range m.Marks {
		switch {
		case mk.B < 0 || mk.B >= len(a.Blocks) || a.Blocks[mk.B].Kind != KindParagraph:
			return fmt.Errorf("mark %d: block %d is not a paragraph", i, mk.B)
		case mk.W < 0 || mk.N < 1 || mk.W+mk.N > len(a.Blocks[mk.B].Words):
			return fmt.Errorf("mark %d: words %d+%d outside the block", i, mk.W, mk.N)
		case mk.G < 0 || mk.G >= entries:
			return fmt.Errorf("mark %d: no glossary entry %d", i, mk.G)
		case mk.B < endB || (mk.B == endB && mk.W < endW):
			return fmt.Errorf("mark %d: overlaps or comes before the previous mark", i)
		}
		endB, endW = mk.B, mk.W+mk.N
	}
	return nil
}

// path joins a manifest-relative path. Validate has already ensured rel is
// local, so the result never escapes Dir.
func (b Book) path(rel string) string { return filepath.Join(b.Dir, filepath.FromSlash(rel)) }

// Validate checks manifest rules beyond JSON shape.
func (m Manifest) Validate() error {
	if !supported(m.SchemaVersion) {
		return fmt.Errorf("unsupported schema_version %d", m.SchemaVersion)
	}
	if !IsSlug(m.ID) {
		return fmt.Errorf("invalid id %q", m.ID)
	}
	if m.Title == "" {
		return errors.New("empty title")
	}
	if len(m.Language) < 2 {
		return fmt.Errorf("invalid language %q", m.Language)
	}
	if len(m.Chapters) == 0 {
		return errors.New("no chapters")
	}
	if m.Cover != "" && !filepath.IsLocal(filepath.FromSlash(m.Cover)) {
		return fmt.Errorf("cover path %q escapes bundle", m.Cover)
	}
	if m.Glossary != "" {
		if m.SchemaVersion < glossaryVersion {
			return fmt.Errorf("glossary needs schema_version %d", glossaryVersion)
		}
		if !filepath.IsLocal(filepath.FromSlash(m.Glossary)) {
			return fmt.Errorf("glossary path %q escapes bundle", m.Glossary)
		}
	}
	for i, ch := range m.Chapters {
		if ch.Index != i+1 {
			return fmt.Errorf("chapter %d has index %d, want %d", i, ch.Index, i+1)
		}
		if ch.DurationS < 0 {
			return fmt.Errorf("chapter %d: negative duration", ch.Index)
		}
		for _, rel := range []string{ch.Audio, ch.Alignment} {
			if rel == "" || !filepath.IsLocal(filepath.FromSlash(rel)) {
				return fmt.Errorf("chapter %d: path %q escapes bundle", ch.Index, rel)
			}
		}
		if ch.Marks != "" {
			if m.Glossary == "" {
				return fmt.Errorf("chapter %d: marks without a glossary", ch.Index)
			}
			if !filepath.IsLocal(filepath.FromSlash(ch.Marks)) {
				return fmt.Errorf("chapter %d: path %q escapes bundle", ch.Index, ch.Marks)
			}
		}
	}
	return nil
}

// Validate checks block rules (pictures need v2 and a local image; text
// blocks need words and no image) and timing rules: e >= s, starts never go
// backwards, and no word ends far past the chapter's duration.
func (a Alignment) Validate(ch Chapter) error {
	if !supported(a.SchemaVersion) {
		return fmt.Errorf("unsupported schema_version %d", a.SchemaVersion)
	}
	if a.ChapterIndex != ch.Index {
		return fmt.Errorf("chapter_index %d, want %d", a.ChapterIndex, ch.Index)
	}
	prev := 0.0
	for bi, blk := range a.Blocks {
		switch blk.Kind {
		case KindParagraph, KindHeading:
			if blk.Image != "" {
				return fmt.Errorf("block %d: %q block with image", bi, blk.Kind)
			}
			if len(blk.Words) == 0 {
				return fmt.Errorf("block %d: no words", bi)
			}
		case KindFigure:
			if a.SchemaVersion < 2 {
				return fmt.Errorf("block %d: picture needs schema_version 2", bi)
			}
			if blk.Image == "" || !filepath.IsLocal(filepath.FromSlash(blk.Image)) {
				return fmt.Errorf("block %d: image path %q escapes bundle", bi, blk.Image)
			}
		default:
			return fmt.Errorf("block %d: unknown kind %q", bi, blk.Kind)
		}
		for wi, w := range blk.Words {
			switch {
			case w.T == "":
				return fmt.Errorf("block %d word %d: empty text", bi, wi)
			case w.S < prev:
				return fmt.Errorf("block %d word %d: start %.3f before previous %.3f", bi, wi, w.S, prev)
			case w.E < w.S:
				return fmt.Errorf("block %d word %d: end %.3f before start %.3f", bi, wi, w.E, w.S)
			case w.E > ch.DurationS+durationSlack:
				return fmt.Errorf("block %d word %d: end %.3f past duration %.3f", bi, wi, w.E, ch.DurationS)
			}
			prev = w.S
		}
	}
	return nil
}

// WordCount returns the number of words across all blocks.
func (a Alignment) WordCount() int {
	n := 0
	for _, b := range a.Blocks {
		n += len(b.Words)
	}
	return n
}

func decodeStrict(path string, v any) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode %s: trailing data", path)
	}
	return nil
}

func supported(v int) bool { return v >= MinSchemaVersion && v <= SchemaVersion }

// IsSlug reports whether s matches ^[a-z0-9][a-z0-9-]*$ without regex.
func IsSlug(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

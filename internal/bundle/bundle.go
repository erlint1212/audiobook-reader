// Package bundle reads and validates the on-disk audiobook bundle format
// (v1; v2 adds picture blocks; v3 adds spoken forms and code, table, list
// item and quote blocks).
//
// The format is the contract with audiobook-forge. Its JSON Schemas live in
// schema/bundle.v{1,2}.schema.json; this package mirrors them with Go types and
// adds the semantic rules a schema cannot express (ordering, file existence).
package bundle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SchemaVersion is the newest bundle version this reader understands.
// MinSchemaVersion is the oldest it still loads.
const (
	SchemaVersion    = 3
	MinSchemaVersion = 1
)

// ManifestFile is the manifest's file name inside a bundle directory.
const ManifestFile = "manifest.json"

// durationSlack is how far past duration_s a word may end, to absorb encoder
// padding.
const durationSlack = 0.5

// MaxDepth bounds list nesting, so a bad bundle cannot ask for huge indents.
const MaxDepth = 16

// BlockKind tells the reader how to render a block.
type BlockKind string

const (
	KindParagraph BlockKind = "p"
	KindHeading   BlockKind = "h"
	// KindFigure is a picture (schema v2). Its words are the caption, else the
	// alt text, and may be empty.
	KindFigure BlockKind = "fig"
	// The rest are schema v3. KindCode keeps its verbatim Text; KindTable
	// splits its words into Rows of cells; KindListItem has Marker and Depth.
	KindCode     BlockKind = "code"
	KindTable    BlockKind = "table"
	KindListItem BlockKind = "li"
	KindQuote    BlockKind = "quote"
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
	Chapters      []Chapter `json:"chapters"`
}

// Chapter points at one chapter's audio and alignment files.
type Chapter struct {
	Index     int     `json:"index"`
	Title     string  `json:"title"`
	Audio     string  `json:"audio"`
	Alignment string  `json:"alignment"`
	DurationS float64 `json:"duration_s"`
}

// Alignment holds word timings for one chapter.
type Alignment struct {
	SchemaVersion int     `json:"schema_version"`
	ChapterIndex  int     `json:"chapter_index"`
	Blocks        []Block `json:"blocks"`
}

// Block is one piece of a chapter. Words are always flat, in reading order;
// the other fields only lay them out, and each is set only on its own kind:
// Image on pictures, Text on code, Rows on tables, Marker and Depth on list
// items.
type Block struct {
	Kind   BlockKind `json:"kind"`
	Image  string    `json:"image,omitempty"`
	Words  []Word    `json:"words"`
	Text   string    `json:"text,omitempty"`
	Rows   [][]Cell  `json:"rows,omitempty"`
	Marker string    `json:"marker,omitempty"`
	Depth  *int      `json:"depth,omitempty"`
}

// Cell is one table cell: the next N words of its block.
type Cell struct {
	N  int  `json:"n"`
	TH bool `json:"th"`
}

// Word is one displayed word with start/end time in seconds. Say (v3) is
// what was spoken for it when that differs from T, "" when silent.
type Word struct {
	T   string  `json:"t"`
	S   float64 `json:"s"`
	E   float64 `json:"e"`
	Say *string `json:"say,omitempty"`
}

// Book is a validated bundle rooted at Dir.
type Book struct {
	Dir      string
	Manifest Manifest
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
		for _, rel := range []string{ch.Audio, ch.Alignment} {
			if _, err := os.Stat(b.path(rel)); err != nil {
				return Book{}, fmt.Errorf("bundle %s: chapter %d: %w", dir, ch.Index, err)
			}
		}
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
		if err := blk.validate(a.SchemaVersion); err != nil {
			return fmt.Errorf("block %d: %w", bi, err)
		}
		for wi, w := range blk.Words {
			switch {
			case w.Say != nil && a.SchemaVersion < 3:
				return fmt.Errorf("block %d word %d: say needs schema_version 3", bi, wi)
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

// minVersion is the first schema version that has kind k, or 0 if none does.
func minVersion(k BlockKind) int {
	switch k {
	case KindParagraph, KindHeading:
		return 1
	case KindFigure:
		return 2
	case KindCode, KindTable, KindListItem, KindQuote:
		return 3
	}
	return 0
}

// validate checks one block's kind rules: the kind exists in version, only
// its own layout fields are set, and they match its words.
func (b Block) validate(version int) error {
	since := minVersion(b.Kind)
	switch {
	case since == 0:
		return fmt.Errorf("unknown kind %q", b.Kind)
	case version < since:
		return fmt.Errorf("%q block needs schema_version %d", b.Kind, since)
	}
	own := []struct {
		name      string
		set, want bool
	}{
		{"image", b.Image != "", b.Kind == KindFigure},
		{"text", b.Text != "", b.Kind == KindCode},
		{"rows", b.Rows != nil, b.Kind == KindTable},
		{"marker", b.Marker != "", b.Kind == KindListItem},
		{"depth", b.Depth != nil, b.Kind == KindListItem},
	}
	for _, f := range own {
		switch {
		case f.set && !f.want:
			return fmt.Errorf("%q block with %s", b.Kind, f.name)
		case !f.set && f.want:
			return fmt.Errorf("%q block without %s", b.Kind, f.name)
		}
	}
	if b.Kind != KindFigure && len(b.Words) == 0 {
		return errors.New("no words")
	}
	switch b.Kind {
	case KindFigure:
		if !filepath.IsLocal(filepath.FromSlash(b.Image)) {
			return fmt.Errorf("image path %q escapes bundle", b.Image)
		}
	case KindCode:
		if !wordsInText(b.Text, b.Words) {
			return errors.New("code words not found in its text in order")
		}
	case KindTable:
		n := 0
		for _, row := range b.Rows {
			for _, c := range row {
				if c.N < 0 {
					return errors.New("table cell with negative word count")
				}
				n += c.N
			}
		}
		if n != len(b.Words) {
			return fmt.Errorf("table cells hold %d words, block has %d", n, len(b.Words))
		}
	case KindListItem:
		if *b.Depth < 0 || *b.Depth > MaxDepth {
			return fmt.Errorf("list depth %d outside 0..%d", *b.Depth, MaxDepth)
		}
	}
	return nil
}

// wordsInText reports whether every word occurs in text, in order, without
// overlapping: what lets the reader wrap each word of a listing in place.
func wordsInText(text string, ws []Word) bool {
	rest := text
	for _, w := range ws {
		i := strings.Index(rest, w.T)
		if i < 0 {
			return false
		}
		rest = rest[i+len(w.T):]
	}
	return true
}

// WordCount returns the number of words across all blocks.
func (a Alignment) WordCount() int {
	n := 0
	for _, b := range a.Blocks {
		n += len(b.Words)
	}
	return n
}

func decodeStrict(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
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

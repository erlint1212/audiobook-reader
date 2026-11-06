package views

import (
	"context"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"audiobook-reader/internal/bundle"
	"audiobook-reader/internal/notes"
	"audiobook-reader/internal/progress"
)

func itoa(i int) string { return strconv.Itoa(i) }

// secs formats seconds for data attributes; millisecond precision is plenty.
func secs(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

// clock formats seconds as h:mm:ss or m:ss.
func clock(f float64) string {
	t := int(f)
	h, m, s := t/3600, t/60%60, t%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// Book ids are validated slugs, so they need no escaping in paths.
func bookURL(id string) templ.SafeURL   { return templ.URL("/books/" + id) }
func coverURL(id string) string         { return "/books/" + id + "/cover" }
func progressURL(id string) string      { return "/books/" + id + "/progress" }
func audioURL(id string, ch int) string { return fmt.Sprintf("/books/%s/audio/%d", id, ch) }
func chapterURL(id string, ch int) templ.SafeURL {
	return templ.URL(fmt.Sprintf("/books/%s/ch/%d", id, ch))
}

func imageURL(id string, ch, block int) string {
	return fmt.Sprintf("/books/%s/ch/%d/img/%d", id, ch, block)
}

func resumeURL(id string, p progress.Position) templ.SafeURL {
	return templ.URL(fmt.Sprintf("/books/%s/ch/%d?t=%s", id, p.Chapter, secs(p.Time)))
}

func refreshURL(id string) string { return "/books/" + id + "/refresh" }

func nextURL(id string, ch int) string { return fmt.Sprintf("/books/%s/ch/%d/next", id, ch) }

// viewQuery tells note handlers which list to render back: links that swap
// #reader, or plain links for the book page.
func viewQuery(inReader bool) string {
	if inReader {
		return "?view=reader"
	}
	return ""
}

func notesURL(id string, inReader bool) string {
	return "/books/" + id + "/notes" + viewQuery(inReader)
}

func noteItemURL(id, noteID string, inReader bool) string {
	return "/books/" + id + "/notes/" + noteID + viewQuery(inReader)
}

// noteURL opens the reader at the note's first word.
func noteURL(id string, n notes.Note) templ.SafeURL {
	return templ.URL(fmt.Sprintf("/books/%s/ch/%d?t=%s", id, n.Chapter, secs(n.Start)))
}

// chapterTitle is the title of chapter i, or "" if the book lost it.
func chapterTitle(b bundle.Book, i int) string {
	ch, err := b.Chapter(i)
	if err != nil {
		return ""
	}
	return ch.Title
}

func noteDate(n notes.Note) string { return n.Created.Local().Format("2 Jan 2006") }

func notesExportURL(id string) string { return "/books/" + id + "/notes/export" }
func notesImportURL(id string) string { return "/books/" + id + "/notes/import" }

// chapterNotes counts one chapter's notes with text and its bookmarks.
type chapterNotes struct{ Notes, Marks int }

// countNotes tallies notes per chapter index.
func countNotes(ns []notes.Note) map[int]chapterNotes {
	out := map[int]chapterNotes{}
	for _, n := range ns {
		c := out[n.Chapter]
		if n.Bookmark() {
			c.Marks++
		} else {
			c.Notes++
		}
		out[n.Chapter] = c
	}
	return out
}

func noteCountID(ch int) string { return "note-count-" + itoa(ch) }

// notesPreview is how many notes the book page shows before "Show all".
const notesPreview = 3

// prefGroup is one row of the reader's settings dropdown. Key and option
// values must match PREFS in static/sync.js; sync.js ignores anything else.
type prefGroup struct {
	Key     string
	Label   string
	Options []prefOption
}

type prefOption struct {
	Value string
	Label string
}

var prefGroups = []prefGroup{
	{"size", "Text size", []prefOption{{"sm", "S"}, {"md", "M"}, {"lg", "L"}, {"xl", "XL"}}},
	{"font", "Font", []prefOption{{"sans", "Sans"}, {"serif", "Serif"}, {"mono", "Mono"}}},
	{"leading", "Line spacing", []prefOption{{"tight", "Tight"}, {"normal", "Normal"}, {"loose", "Loose"}}},
	{"width", "Text width", []prefOption{{"narrow", "Narrow"}, {"normal", "Normal"}, {"wide", "Wide"}}},
	{"theme", "Theme", []prefOption{{"system", "System"}, {"light", "Light"}, {"dark", "Dark"}}},
}

// Keys and values are the constants above, never request input, so plain
// concatenation into the Alpine expressions is safe.
func prefCall(key, value string) string    { return "setPref('" + key + "', '" + value + "')" }
func prefChecked(key, value string) string { return "prefs." + key + " === '" + value + "'" }

// codePart is one piece of a code listing: the verbatim text before a word
// (spaces, line breaks, indentation), then the word. The last part may have
// only Gap, the text after the last word.
type codePart struct {
	Gap  string
	Word *bundle.Word
}

// codeParts cuts a listing so each word can be its own span while every
// other character is kept as written. Validate has checked that the words
// occur in text in order; a word that does not (never for a valid bundle)
// is put after the text, so no word is lost.
func codeParts(text string, ws []bundle.Word) []codePart {
	out := make([]codePart, 0, len(ws)+1)
	rest := text
	for i := range ws {
		w := &ws[i]
		at := strings.Index(rest, w.T)
		if at < 0 {
			out = append(out, codePart{Gap: rest + " ", Word: w})
			rest = ""
			continue
		}
		out = append(out, codePart{Gap: rest[:at], Word: w})
		rest = rest[at+len(w.T):]
	}
	if rest != "" {
		out = append(out, codePart{Gap: rest})
	}
	return out
}

// tableCell is one cell of a table block with its words.
type tableCell struct {
	Head  bool
	Words []bundle.Word
}

// tableRows slices a table block's flat words into its rows of cells.
// Validate has checked that the counts add up; extra counts get no words.
func tableRows(blk bundle.Block) [][]tableCell {
	out := make([][]tableCell, 0, len(blk.Rows))
	pos := 0
	for _, row := range blk.Rows {
		cells := make([]tableCell, 0, len(row))
		for _, c := range row {
			end := min(pos+max(c.N, 0), len(blk.Words))
			cells = append(cells, tableCell{Head: c.TH, Words: blk.Words[pos:end]})
			pos = end
		}
		out = append(out, cells)
	}
	return out
}

// blockRun is a stretch of a chapter's blocks rendered together: one block,
// or consecutive list items that share one <ul>. Start is the index of the
// first block in the chapter (picture URLs are by block index).
type blockRun struct {
	Start  int
	Blocks []bundle.Block
	List   bool
}

// blockRuns groups consecutive list items so they render as one list.
func blockRuns(blocks []bundle.Block) []blockRun {
	var out []blockRun
	for i, blk := range blocks {
		li := blk.Kind == bundle.KindListItem
		if li && len(out) > 0 && out[len(out)-1].List {
			last := &out[len(out)-1]
			last.Blocks = blocks[last.Start : i+1]
			continue
		}
		out = append(out, blockRun{Start: i, Blocks: blocks[i : i+1], List: li})
	}
	return out
}

// indents are whole class names so Tailwind finds them in this file.
var indents = [...]string{"", "pl-6", "pl-12", "pl-18", "pl-24"}

// indent is the left padding for a list item at depth; deeper lists stop
// moving right so text keeps room on a phone.
func indent(depth *int) string {
	if depth == nil {
		return ""
	}
	return indents[min(max(*depth, 0), len(indents)-1)]
}

// spoken is a word's spoken form for its title tooltip, "" when the word is
// read as shown.
func spoken(w bundle.Word) string {
	if w.Say == nil || *w.Say == w.T {
		return ""
	}
	if *w.Say == "" {
		return "(silent)"
	}
	return *w.Say
}

// codeClass styles a listing. Words lose the prose padding so columns line up.
const codeClass = "overflow-x-auto rounded-lg bg-stone-100 p-4 font-mono text-sm leading-normal dark:bg-stone-800 [&_span]:px-0! [&_span]:rounded-none"

// codeListing renders a code block as <pre><code> with one span per word and
// every other character (spaces, line breaks) exactly as in the listing.
func codeListing(blk bundle.Block) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		var sb strings.Builder
		sb.WriteString(`<pre data-hscroll class="` + codeClass + `"><code>`)
		for _, part := range codeParts(blk.Text, blk.Words) {
			sb.WriteString(html.EscapeString(part.Gap))
			if part.Word == nil {
				continue
			}
			sb.WriteString(`<span data-s="` + secs(part.Word.S) + `"`)
			if say := spoken(*part.Word); say != "" {
				sb.WriteString(` title="` + html.EscapeString(say) + `"`)
			}
			sb.WriteString(">" + html.EscapeString(part.Word.T) + "</span>")
		}
		sb.WriteString("</code></pre>")
		if _, err := io.WriteString(w, sb.String()); err != nil {
			return fmt.Errorf("write code listing: %w", err)
		}
		return nil
	})
}

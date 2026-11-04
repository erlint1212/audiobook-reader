package views

import (
	"fmt"
	"strconv"

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

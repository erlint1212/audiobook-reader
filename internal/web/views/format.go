package views

import (
	"fmt"
	"strconv"

	"github.com/a-h/templ"

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

func resumeURL(id string, p progress.Position) templ.SafeURL {
	return templ.URL(fmt.Sprintf("/books/%s/ch/%d?t=%s", id, p.Chapter, secs(p.Time)))
}

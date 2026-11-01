// Package web wires HTTP routes to the library, progress store and views.
package web

import (
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"

	"audiobook-reader/internal/bundle"
	"audiobook-reader/internal/library"
	"audiobook-reader/internal/notes"
	"audiobook-reader/internal/progress"
	"audiobook-reader/internal/web/views"
)

//go:embed static
var staticFS embed.FS

// Server is immutable after New; all handlers are safe for concurrent use.
// The Shelf it holds swaps in a new Library on refresh.
type Server struct {
	lib   *library.Shelf
	store progress.Store
	notes notes.Store
	log   *slog.Logger
	mux   *http.ServeMux
}

// New builds the router.
func New(lib *library.Shelf, store progress.Store, ns notes.Store, log *slog.Logger) *Server {
	s := &Server{lib: lib, store: store, notes: ns, log: log, mux: http.NewServeMux()}
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embed path is fixed at compile time
	}
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	s.mux.HandleFunc("GET /{$}", s.handleLibrary)
	s.mux.HandleFunc("GET /books/{id}", s.handleBook)
	s.mux.HandleFunc("GET /books/{id}/cover", s.handleCover)
	s.mux.HandleFunc("GET /books/{id}/ch/{n}", s.handleChapter)
	s.mux.HandleFunc("GET /books/{id}/ch/{n}/img/{b}", s.handleImage)
	s.mux.HandleFunc("GET /books/{id}/audio/{n}", s.handleAudio)
	s.mux.HandleFunc("POST /books/{id}/progress", s.handleProgress)
	s.mux.HandleFunc("POST /refresh", s.handleRefreshLibrary)
	s.mux.HandleFunc("POST /books/{id}/refresh", s.handleRefreshBook)
	s.mux.HandleFunc("GET /books/{id}/ch/{n}/next", s.handleNextLink)
	s.mux.HandleFunc("POST /books/{id}/notes", s.handleNoteAdd)
	s.mux.HandleFunc("POST /books/{id}/notes/{nid}", s.handleNoteUpdate)
	s.mux.HandleFunc("DELETE /books/{id}/notes/{nid}", s.handleNoteDelete)
	return s
}

// handleRefreshLibrary rescans the library, then goes back to the grid.
func (s *Server) handleRefreshLibrary(w http.ResponseWriter, r *http.Request) {
	if err := s.lib.Refresh(); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleRefreshBook rescans the library, then goes back to the chapter list.
// The redirect uses the manifest id, never the raw path value.
func (s *Server) handleRefreshBook(w http.ResponseWriter, r *http.Request) {
	if err := s.lib.Refresh(); err != nil {
		s.fail(w, err)
		return
	}
	b, ok := s.book(w, r)
	if !ok {
		return
	}
	http.Redirect(w, r, "/books/"+b.Manifest.ID, http.StatusSeeOther)
}

// handleNextLink is polled by the reader on the last known chapter. It
// rescans and returns the "Next →" link, live once forge has written the
// next chapter, else the same polling placeholder again. A failed rescan
// keeps the current library: an error body must not land in the top bar.
func (s *Server) handleNextLink(w http.ResponseWriter, r *http.Request) {
	if err := s.lib.Refresh(); err != nil {
		s.log.Warn("refresh", "err", err)
	}
	b, ch, ok := s.chapter(w, r)
	if !ok {
		return
	}
	s.render(w, r, views.NextLink(b, ch.Index))
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, views.LibraryPage(s.lib.Get().Books()))
}

func (s *Server) handleBook(w http.ResponseWriter, r *http.Request) {
	b, ok := s.book(w, r)
	if !ok {
		return
	}
	pos, has, err := s.store.Get(b.Manifest.ID)
	if err != nil {
		s.log.Warn("read progress", "book", b.Manifest.ID, "err", err)
	}
	s.render(w, r, views.BookPage(b, pos, has && err == nil, s.bookNotes(b)))
}

func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	b, ok := s.book(w, r)
	if !ok {
		return
	}
	p := b.CoverPath()
	if p == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, p)
}

func (s *Server) handleChapter(w http.ResponseWriter, r *http.Request) {
	b, ch, ok := s.chapter(w, r)
	if !ok {
		return
	}
	a, err := b.LoadAlignment(ch)
	if err != nil {
		s.fail(w, err)
		return
	}
	start := s.startTime(r, b, ch)
	ns := s.bookNotes(b)
	// htmx chapter switches only need the reader section.
	if r.Header.Get("HX-Request") == "true" {
		s.render(w, r, views.Reader(b, ch, a, start, ns))
		return
	}
	s.render(w, r, views.ReaderPage(b, ch, a, start, ns))
}

// bookNotes lists the book's notes. A broken notes file must not block
// reading, so it is logged and shown as no notes.
func (s *Server) bookNotes(b bundle.Book) []notes.Note {
	ns, err := s.notes.List(b.Manifest.ID)
	if err != nil {
		s.log.Warn("read notes", "book", b.Manifest.ID, "err", err)
		return nil
	}
	return ns
}

// maxNoteRunes caps a note's own text.
const maxNoteRunes = 5000

// handleNoteAdd saves a note on the words from start to end (both word
// start times). The client sends only times; the quote comes from the
// alignment, so stored text is always text from the book.
func (s *Server) handleNoteAdd(w http.ResponseWriter, r *http.Request) {
	b, ok := s.book(w, r)
	if !ok {
		return
	}
	chIdx, err1 := strconv.Atoi(r.FormValue("chapter"))
	start, err2 := strconv.ParseFloat(r.FormValue("start"), 64)
	end, err3 := strconv.ParseFloat(r.FormValue("end"), 64)
	text, okText := noteText(r)
	if err1 != nil || err2 != nil || err3 != nil || !validTime(start) || !validTime(end) || start > end || !okText {
		http.Error(w, "bad note", http.StatusBadRequest)
		return
	}
	ch, err := b.Chapter(chIdx)
	if err != nil || end > ch.DurationS {
		http.Error(w, "bad chapter", http.StatusBadRequest)
		return
	}
	a, err := b.LoadAlignment(ch)
	if err != nil {
		s.fail(w, err)
		return
	}
	quote := quoteFor(a, start, end)
	if quote == "" {
		http.Error(w, "no words in range", http.StatusBadRequest)
		return
	}
	n := notes.Note{Chapter: chIdx, Start: start, End: end, Quote: quote, Text: text}
	if _, err := s.notes.Add(b.Manifest.ID, n); err != nil {
		s.fail(w, err)
		return
	}
	s.renderNotes(w, r, b)
}

func (s *Server) handleNoteUpdate(w http.ResponseWriter, r *http.Request) {
	b, id, ok := s.noteID(w, r)
	if !ok {
		return
	}
	text, ok := noteText(r)
	if !ok {
		http.Error(w, "bad note", http.StatusBadRequest)
		return
	}
	if _, err := s.notes.Update(b.Manifest.ID, id, text); err != nil {
		s.noteFail(w, r, err)
		return
	}
	s.renderNotes(w, r, b)
}

func (s *Server) handleNoteDelete(w http.ResponseWriter, r *http.Request) {
	b, id, ok := s.noteID(w, r)
	if !ok {
		return
	}
	if err := s.notes.Delete(b.Manifest.ID, id); err != nil {
		s.noteFail(w, r, err)
		return
	}
	s.renderNotes(w, r, b)
}

// renderNotes answers every note change with the fresh list. Form field
// view=reader makes its links swap #reader instead of loading a page.
func (s *Server) renderNotes(w http.ResponseWriter, r *http.Request, b bundle.Book) {
	ns, err := s.notes.List(b.Manifest.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, r, views.NotesList(b, ns, r.FormValue("view") == "reader"))
}

func (s *Server) noteID(w http.ResponseWriter, r *http.Request) (bundle.Book, string, bool) {
	b, ok := s.book(w, r)
	if !ok {
		return bundle.Book{}, "", false
	}
	id := r.PathValue("nid")
	if !notes.ValidID(id) {
		http.NotFound(w, r)
		return bundle.Book{}, "", false
	}
	return b, id, true
}

func (s *Server) noteFail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, notes.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	s.fail(w, err)
}

// noteText is the trimmed "text" field; ok is false when it is too long.
func noteText(r *http.Request) (string, bool) {
	t := strings.TrimSpace(r.FormValue("text"))
	return t, utf8.RuneCountInString(t) <= maxNoteRunes
}

const (
	maxQuoteWords    = 60 // longer selections are cut with "…"
	bookmarkQuoteLen = 15 // a bookmark quotes this many words from its spot
)

// quoteFor joins the words whose start lies in [start, end]. A bookmark
// (start == end) quotes a few words on from the first word at or after
// start, within that block.
// Starts never go backwards (bundle.Validate), so the first word past end
// ends the quote.
func quoteFor(a bundle.Alignment, start, end float64) string {
	var out []string
	bookmark := start == end
	for _, blk := range a.Blocks {
		for _, wd := range blk.Words {
			if wd.S < start {
				continue
			}
			if wd.S > end && !(bookmark && len(out) < bookmarkQuoteLen) {
				return strings.Join(out, " ")
			}
			if len(out) == maxQuoteWords {
				return strings.Join(out, " ") + " …"
			}
			out = append(out, wd.T)
		}
		if bookmark && len(out) > 0 {
			break
		}
	}
	return strings.Join(out, " ")
}

// startTime prefers ?t=, then saved progress for this chapter, then 0.
func (s *Server) startTime(r *http.Request, b bundle.Book, ch bundle.Chapter) float64 {
	if t, err := strconv.ParseFloat(r.URL.Query().Get("t"), 64); err == nil && validTime(t) {
		return min(t, ch.DurationS)
	}
	if pos, ok, err := s.store.Get(b.Manifest.ID); err == nil && ok && pos.Chapter == ch.Index {
		return min(pos.Time, ch.DurationS)
	}
	return 0
}

// handleImage serves the picture of block {b}. The request names only a block
// index; the path comes from the validated alignment.
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	b, ch, ok := s.chapter(w, r)
	if !ok {
		return
	}
	a, err := b.LoadAlignment(ch)
	if err != nil {
		s.fail(w, err)
		return
	}
	i, err := strconv.Atoi(r.PathValue("b"))
	if err != nil || i < 0 || i >= len(a.Blocks) || a.Blocks[i].Kind != bundle.KindFigure {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, b.ImagePath(a.Blocks[i]))
}

func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	b, ch, ok := s.chapter(w, r)
	if !ok {
		return
	}
	p := b.AudioPath(ch)
	f, err := os.Open(p)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			s.log.Warn("close audio", "path", p, "err", err)
		}
	}()
	info, err := f.Stat()
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", audioType(p))
	// ServeContent handles Range requests, which the <audio> element needs to seek.
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	b, ok := s.book(w, r)
	if !ok {
		return
	}
	chIdx, err1 := strconv.Atoi(r.FormValue("chapter"))
	t, err2 := strconv.ParseFloat(r.FormValue("time"), 64)
	if err1 != nil || err2 != nil || !validTime(t) {
		http.Error(w, "bad progress", http.StatusBadRequest)
		return
	}
	if _, err := b.Chapter(chIdx); err != nil {
		http.Error(w, "bad chapter", http.StatusBadRequest)
		return
	}
	pos := progress.Position{Chapter: chIdx, Time: t, Updated: time.Now().UTC()}
	if err := s.store.Save(b.Manifest.ID, pos); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) book(w http.ResponseWriter, r *http.Request) (bundle.Book, bool) {
	b, ok := s.lib.Get().Book(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
	}
	return b, ok
}

func (s *Server) chapter(w http.ResponseWriter, r *http.Request) (bundle.Book, bundle.Chapter, bool) {
	b, ok := s.book(w, r)
	if !ok {
		return bundle.Book{}, bundle.Chapter{}, false
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.NotFound(w, r)
		return bundle.Book{}, bundle.Chapter{}, false
	}
	ch, err := b.Chapter(n)
	if err != nil {
		http.NotFound(w, r)
		return bundle.Book{}, bundle.Chapter{}, false
	}
	return b, ch, true
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		s.log.Error("render", "path", r.URL.Path, "err", err)
	}
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "err", err)
	status := http.StatusInternalServerError
	if errors.Is(err, fs.ErrNotExist) {
		status = http.StatusNotFound
	}
	http.Error(w, http.StatusText(status), status)
}

func validTime(t float64) bool { return t >= 0 && !math.IsInf(t, 0) && !math.IsNaN(t) }

func audioType(path string) string {
	switch ext := filepath.Ext(path); ext {
	case ".opus", ".ogg":
		return "audio/ogg"
	default:
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
		return "application/octet-stream"
	}
}

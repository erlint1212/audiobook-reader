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
	"time"

	"github.com/a-h/templ"

	"audiobook-reader/internal/bundle"
	"audiobook-reader/internal/library"
	"audiobook-reader/internal/progress"
	"audiobook-reader/internal/web/views"
)

//go:embed static
var staticFS embed.FS

// Server is immutable after New; all handlers are safe for concurrent use.
type Server struct {
	lib   library.Library
	store progress.Store
	log   *slog.Logger
	mux   *http.ServeMux
}

// New builds the router.
func New(lib library.Library, store progress.Store, log *slog.Logger) *Server {
	s := &Server{lib: lib, store: store, log: log, mux: http.NewServeMux()}
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
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, views.LibraryPage(s.lib.Books()))
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
	s.render(w, r, views.BookPage(b, pos, has && err == nil))
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
	// htmx chapter switches only need the reader section.
	if r.Header.Get("HX-Request") == "true" {
		s.render(w, r, views.Reader(b, ch, a, start))
		return
	}
	s.render(w, r, views.ReaderPage(b, ch, a, start))
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
	defer f.Close()
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
	b, ok := s.lib.Book(r.PathValue("id"))
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

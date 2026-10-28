package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"audiobook-reader/internal/library"
	"audiobook-reader/internal/progress"
)

func newTestServer(t *testing.T) (*Server, progress.Store) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	lib, err := library.Open("../../testdata/golden-bundle", log)
	if err != nil {
		t.Fatal(err)
	}
	store, err := progress.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(lib, store, log), store
}

func do(s *Server, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestRoutesStatus(t *testing.T) {
	s, _ := newTestServer(t)
	cases := map[string]int{
		"/":                          200,
		"/books/demo":                200,
		"/books/demo/ch/1":           200,
		"/books/demo/ch/2":           200,
		"/books/demo/audio/1":        200,
		"/static/sync.js":            200,
		"/books/nope":                404,
		"/books/demo/ch/0":           404,
		"/books/demo/ch/3":           404,
		"/books/demo/ch/x":           404,
		"/books/demo/audio/9":        404,
		"/books/demo/cover":          404,
		"/books/demo/ch/1/../2":      307, // ServeMux cleans paths with a redirect
		"/books/figures/ch/1":        200,
		"/books/figures/ch/1/img/0":  200,
		"/books/figures/ch/1/img/3":  200,
		"/books/figures/ch/1/img/1":  404, // heading, not a picture
		"/books/figures/ch/1/img/9":  404,
		"/books/figures/ch/1/img/x":  404,
		"/books/figures/ch/1/img/-1": 404,
		"/books/figures/ch/2/img/0":  404,
		"/books/demo/ch/1/img/0":     404,
	}
	for path, want := range cases {
		if got := do(s, httptest.NewRequest("GET", path, nil)).Code; got != want {
			t.Errorf("GET %s = %d, want %d", path, got, want)
		}
	}
}

func TestReaderRendersEveryWord(t *testing.T) {
	s, _ := newTestServer(t)
	body := do(s, httptest.NewRequest("GET", "/books/demo/ch/1", nil)).Body.String()
	if n := strings.Count(body, "<span data-s="); n != 6 {
		t.Errorf("rendered %d word spans, want 6", n)
	}
	for _, want := range []string{`data-s="1.000">Hello,</span>`, `<!doctype html>`, `x-data="reader"`} {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestReaderRendersPictures(t *testing.T) {
	s, _ := newTestServer(t)
	body := do(s, httptest.NewRequest("GET", "/books/figures/ch/1", nil)).Body.String()
	if n := strings.Count(body, "<figure"); n != 2 {
		t.Errorf("rendered %d figures, want 2", n)
	}
	// Only the captioned picture gets a figcaption; its words stay clickable spans.
	if n := strings.Count(body, "<figcaption"); n != 1 {
		t.Errorf("rendered %d figcaptions, want 1", n)
	}
	if n := strings.Count(body, "<span data-s="); n != 9 {
		t.Errorf("rendered %d word spans, want 9", n)
	}
	for _, want := range []string{
		`src="/books/figures/ch/1/img/0"`,
		`src="/books/figures/ch/1/img/3"`,
		`has-[span[data-active]]:ring-4`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// Block order is kept: the caption follows its picture.
	if strings.Index(body, "/img/3") > strings.Index(body, `>fox.</span>`) {
		t.Error("caption rendered before its picture")
	}
}

// The custom bottom player replaces native controls, and sync.js auto-advances
// by clicking a[rel=next], so only a chapter with a next one may render it.
func TestReaderPlayerAndNextLink(t *testing.T) {
	s, _ := newTestServer(t)
	first := do(s, httptest.NewRequest("GET", "/books/demo/ch/1", nil)).Body.String()
	for _, want := range []string{
		`x-ref="bar"`,
		`x-on:click="toggle()"`,
		`x-on:click="skip(-10)"`,
		`x-on:click="skip(10)"`,
		`rel="next"`,
		`href="/books/demo/ch/2"`,
		`pb-[40vh]`, // last lines can scroll above the fixed bar
		`aria-label="Playback speed"`,
		`x-on:input="setSpeed($event.target.value)"`,
		`x-on:click="setSpeed(1)"`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("ch 1 missing %q", want)
		}
	}
	if strings.Contains(first, "controls") {
		t.Error("audio still renders native controls")
	}
	last := do(s, httptest.NewRequest("GET", "/books/demo/ch/2", nil)).Body.String()
	if strings.Contains(last, `rel="next"`) {
		t.Error("last chapter renders a next link; auto-advance would loop")
	}
	if !strings.Contains(last, `rel="prev"`) {
		t.Error("last chapter missing prev link")
	}
}

func TestImageServesFile(t *testing.T) {
	s, _ := newTestServer(t)
	w := do(s, httptest.NewRequest("GET", "/books/figures/ch/1/img/3", nil))
	want, err := os.ReadFile("../../testdata/golden-bundle/figures/images/001.png")
	if err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || w.Body.String() != string(want) {
		t.Fatalf("status %d, %d bytes", w.Code, w.Body.Len())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestHTMXFragment(t *testing.T) {
	s, _ := newTestServer(t)
	r := httptest.NewRequest("GET", "/books/demo/ch/2", nil)
	r.Header.Set("HX-Request", "true")
	body := do(s, r).Body.String()
	if strings.Contains(strings.ToLower(body), "<!doctype") {
		t.Error("fragment response contains full page")
	}
	if !strings.HasPrefix(strings.TrimSpace(body), `<section id="reader"`) {
		t.Errorf("fragment does not start with reader section: %.80q", body)
	}
}

func TestAudioRange(t *testing.T) {
	s, _ := newTestServer(t)
	r := httptest.NewRequest("GET", "/books/demo/audio/1", nil)
	r.Header.Set("Range", "bytes=0-3")
	w := do(s, r)
	if w.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", w.Code)
	}
	if w.Body.String() != "OggS" {
		t.Errorf("body = %q, want OggS", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "audio/ogg" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestProgressSaveAndResume(t *testing.T) {
	s, store := newTestServer(t)
	post := func(form url.Values) int {
		r := httptest.NewRequest("POST", "/books/demo/progress", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return do(s, r).Code
	}
	for _, bad := range []url.Values{
		{"chapter": {"9"}, "time": {"1"}},
		{"chapter": {"1"}, "time": {"-1"}},
		{"chapter": {"1"}, "time": {"NaN"}},
		{"chapter": {"x"}, "time": {"1"}},
	} {
		if code := post(bad); code != http.StatusBadRequest {
			t.Errorf("POST %v = %d, want 400", bad, code)
		}
	}
	if code := post(url.Values{"chapter": {"1"}, "time": {"2.5"}}); code != http.StatusNoContent {
		t.Fatalf("POST valid = %d", code)
	}
	pos, ok, err := store.Get("demo")
	if err != nil || !ok || pos.Chapter != 1 || pos.Time != 2.5 {
		t.Fatalf("stored %+v ok %v err %v", pos, ok, err)
	}
	book := do(s, httptest.NewRequest("GET", "/books/demo", nil)).Body.String()
	if !strings.Contains(book, "/books/demo/ch/1?t=2.500") {
		t.Error("book page missing resume link")
	}
	ch := do(s, httptest.NewRequest("GET", "/books/demo/ch/1", nil)).Body.String()
	if !strings.Contains(ch, `data-start="2.500"`) {
		t.Error("chapter did not resume from saved time")
	}
	ch = do(s, httptest.NewRequest("GET", "/books/demo/ch/1?t=99", nil)).Body.String()
	if !strings.Contains(ch, `data-start="4.000"`) {
		t.Error("?t= past duration not clamped")
	}
}

func TestRefreshRedirectsBack(t *testing.T) {
	s, _ := newTestServer(t)
	cases := map[string]string{
		"/refresh":            "/",
		"/books/demo/refresh": "/books/demo",
	}
	for path, want := range cases {
		w := do(s, httptest.NewRequest("POST", path, nil))
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != want {
			t.Errorf("POST %s = %d %q, want 303 %q", path, w.Code, w.Header().Get("Location"), want)
		}
	}
	if got := do(s, httptest.NewRequest("POST", "/books/nope/refresh", nil)).Code; got != 404 {
		t.Errorf("POST unknown book refresh = %d, want 404", got)
	}
	for page, action := range cases {
		if body := do(s, httptest.NewRequest("GET", action, nil)).Body.String(); !strings.Contains(body, `action="`+page+`"`) {
			t.Errorf("%s has no refresh form posting to %s", action, page)
		}
	}
}

// The last chapter polls for more. Once forge adds one, the poll returns
// the live next link and the chapter list shows it.
func TestNextLinkAppearsWhenChapterAdded(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.CopyFS(dir, os.DirFS("../../testdata/golden-bundle/demo")); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "manifest.json")
	full, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(full, &m); err != nil {
		t.Fatal(err)
	}
	m["chapters"] = m["chapters"].([]any)[:1]
	short, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, short, 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(root, log)
	if err != nil {
		t.Fatal(err)
	}
	store, err := progress.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(lib, store, log)

	page := do(s, httptest.NewRequest("GET", "/books/demo/ch/1", nil)).Body.String()
	for _, want := range []string{`hx-get="/books/demo/ch/1/next"`, `hx-trigger="every 30s"`} {
		if !strings.Contains(page, want) {
			t.Errorf("last chapter missing %q", want)
		}
	}
	if strings.Contains(page, `rel="next"`) {
		t.Error("last chapter renders a next link")
	}
	poll := do(s, httptest.NewRequest("GET", "/books/demo/ch/1/next", nil)).Body.String()
	if strings.Contains(poll, `rel="next"`) || !strings.Contains(poll, `hx-trigger="every 30s"`) {
		t.Errorf("poll before new chapter should return placeholder, got %q", poll)
	}

	if err := os.WriteFile(manifest, full, 0o644); err != nil {
		t.Fatal(err)
	}
	poll = do(s, httptest.NewRequest("GET", "/books/demo/ch/1/next", nil)).Body.String()
	for _, want := range []string{`rel="next"`, `href="/books/demo/ch/2"`, `x-init="nextReady()"`} {
		if !strings.Contains(poll, want) {
			t.Errorf("poll after new chapter missing %q: %q", want, poll)
		}
	}
	if strings.Contains(poll, "every 30s") {
		t.Error("live link still polls")
	}
	if got := do(s, httptest.NewRequest("GET", "/books/demo/ch/2", nil)).Code; got != 200 {
		t.Errorf("new chapter = %d, want 200", got)
	}
}

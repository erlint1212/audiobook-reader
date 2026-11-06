package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"audiobook-reader/internal/bundle"
	"audiobook-reader/internal/library"
	"audiobook-reader/internal/notes"
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
	return New(lib, store, newNotes(t), log), store
}

func newNotes(t *testing.T) notes.Store {
	t.Helper()
	ns, err := notes.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ns
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
		"/books/demo/notes/export":   200,
		"/books/nope/notes/export":   404,
		"/books/demo/ch/1/sections":  200,
		"/books/demo/ch/9/sections":  404,
		"/books/nope/ch/1/sections":  404,
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

// The player has a volume slider, and the top bar an "Aa" dropdown whose
// buttons call setPref. The article reads each pref from <html> data
// attributes through Tailwind variants.
func TestReaderSettingsAndVolume(t *testing.T) {
	s, _ := newTestServer(t)
	body := do(s, httptest.NewRequest("GET", "/books/demo/ch/1", nil)).Body.String()
	for _, want := range []string{
		`aria-label="Volume"`,
		`x-on:input="setVolume($event.target.value)"`,
		`x-on:click="toggleMute()"`,
		`max="2"`, // volume boosts past 100%; must match MAX_VOLUME in sync.js
		`x-on:click="setVolume(1)"`,
		`aria-label="Reading settings"`,
		`x-ref="settings"`,
		`setPref(&#39;size&#39;, &#39;xl&#39;)`,
		`setPref(&#39;font&#39;, &#39;serif&#39;)`,
		`setPref(&#39;leading&#39;, &#39;loose&#39;)`,
		`setPref(&#39;width&#39;, &#39;narrow&#39;)`,
		`setPref(&#39;theme&#39;, &#39;dark&#39;)`,
		`[[data-size=lg]_&]:text-xl`,
		`[[data-font=serif]_&]:font-serif`,
		`[[data-leading=loose]_&]:leading-loose`,
		`[[data-width=narrow]_&]:max-w-xl`,
		`[[data-width=wide]_&]:max-w-5xl`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("reader missing %q", want)
		}
	}
}

// Every value the dropdown offers must be allowed by PREFS in sync.js, or
// setPref would silently ignore the click.
func TestPrefValuesMatchSyncJS(t *testing.T) {
	js, err := os.ReadFile("static/sync.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	s, _ := newTestServer(t)
	body := do(s, httptest.NewRequest("GET", "/books/demo/ch/1", nil)).Body.String()
	for _, key := range []string{"size", "font", "leading", "width", "theme"} {
		start := strings.Index(src, "  "+key+": [")
		if start < 0 {
			t.Fatalf("PREFS has no %q", key)
		}
		line := src[start : start+strings.Index(src[start:], "\n")]
		rest := body
		for {
			i := strings.Index(rest, "setPref(&#39;"+key+"&#39;, &#39;")
			if i < 0 {
				break
			}
			rest = rest[i+len("setPref(&#39;"+key+"&#39;, &#39;"):]
			value := rest[:strings.Index(rest, "&#39;")]
			if !strings.Contains(line, `"`+value+`"`) {
				t.Errorf("dropdown offers %s=%q, PREFS line is %s", key, value, line)
			}
		}
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
	s := New(lib, store, newNotes(t), log)

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

// Chapter lists show the chapter number in front of the title.
func TestBookPageNumbersChapters(t *testing.T) {
	s, _ := newTestServer(t)
	body := do(s, httptest.NewRequest("GET", "/books/demo", nil)).Body.String()
	one := strings.Index(body, ">1.</span>")
	two := strings.Index(body, ">2.</span>")
	if one < 0 || two < 0 {
		t.Fatal("book page missing chapter numbers")
	}
	if one > strings.Index(body, "Chapter One") {
		t.Error("number 1 not before Chapter One")
	}
	if two > strings.Index(body, "Chapter Two") {
		t.Error("number 2 not before Chapter Two")
	}
}

// The reader's chapter drawer lists every chapter as an htmx link, marks the
// current one, and adds no rel (auto-advance clicks a[rel=next]).
func TestReaderChapterDrawer(t *testing.T) {
	s, _ := newTestServer(t)
	body := do(s, httptest.NewRequest("GET", "/books/demo/ch/2", nil)).Body.String()
	for _, want := range []string{
		`x-on:click="openChapters()"`,
		`x-ref="drawer"`,
		`x-on:wheel.window="unfollow($event)"`,
		`hx-get="/books/demo/ch/1"`,
		`hx-get="/books/demo/ch/2"`,
		`x-on:click="pick()"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("reader missing %q", want)
		}
	}
	if n := strings.Count(body, `aria-current="page"`); n != 1 {
		t.Fatalf("aria-current count = %d, want 1", n)
	}
	i := strings.Index(body, `aria-current="page"`)
	link := body[strings.LastIndex(body[:i], "<a "):i]
	if !strings.Contains(link, `href="/books/demo/ch/2"`) {
		t.Errorf("aria-current on wrong link: %s", link)
	}
	if strings.Contains(body, `rel="next"`) {
		t.Error("drawer added a next link on the last chapter; auto-advance would loop")
	}
}

func newNotesServer(t *testing.T) (*Server, notes.Store) {
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
	ns := newNotes(t)
	return New(lib, store, ns, log), ns
}

func postForm(s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return do(s, r)
}

// The client sends only word times; the server quotes the book itself.
func TestNoteAdd(t *testing.T) {
	s, ns := newNotesServer(t)
	for _, bad := range []url.Values{
		{"chapter": {"9"}, "start": {"1"}, "end": {"1"}},
		{"chapter": {"x"}, "start": {"1"}, "end": {"1"}},
		{"chapter": {"1"}, "start": {"2"}, "end": {"1"}},
		{"chapter": {"1"}, "start": {"1"}, "end": {"99"}},
		{"chapter": {"1"}, "start": {"NaN"}, "end": {"1"}},
		{"chapter": {"1"}, "start": {"-1"}, "end": {"1"}},
		{"chapter": {"1"}, "start": {"3.5"}, "end": {"3.9"}}, // no word starts there
		{"chapter": {"1"}, "start": {"1"}, "end": {"1"}, "text": {strings.Repeat("x", maxNoteRunes+1)}},
	} {
		if code := postForm(s, "/books/demo/notes", bad).Code; code != http.StatusBadRequest {
			t.Errorf("POST %v = %d, want 400", bad, code)
		}
	}
	if code := postForm(s, "/books/nope/notes", url.Values{"chapter": {"1"}, "start": {"1"}, "end": {"1"}}).Code; code != 404 {
		t.Errorf("unknown book = %d, want 404", code)
	}

	w := postForm(s, "/books/demo/notes?view=reader", url.Values{
		"chapter": {"1"}, "start": {"1.000"}, "end": {"2.100"}, "text": {"  my note  "},
	})
	if w.Code != 200 {
		t.Fatalf("POST valid = %d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Hello, brave new</blockquote>",
		"my note",
		`x-init="notesChanged()"`,
		`hx-get="/books/demo/ch/1?t=1.000"`,
		`data-ch="1" data-start="1.000" data-end="2.100"`,
		"Ch 1 · 0:01",
		"Chapter One",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("list missing %q", want)
		}
	}
	got, err := ns.List("demo")
	if err != nil || len(got) != 1 || got[0].Text != "my note" || got[0].Quote != "Hello, brave new" {
		t.Fatalf("stored %+v, %v", got, err)
	}

	// Bookmark: one point quotes on to the end of its block.
	w = postForm(s, "/books/demo/notes?view=reader", url.Values{"chapter": {"1"}, "start": {"0.4"}, "end": {"0.4"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "🔖 Bookmark") {
		t.Fatalf("bookmark = %d %s", w.Code, w.Body)
	}
	got, err = ns.List("demo")
	if err != nil || len(got) != 2 || got[0].Quote != "One" || !got[0].Bookmark() {
		t.Fatalf("stored %+v, %v", got, err)
	}
}

func TestNoteUpdateDelete(t *testing.T) {
	s, ns := newNotesServer(t)
	n, err := ns.Add("demo", notes.Note{Chapter: 1, Start: 1, End: 1, Quote: "Hello,"})
	if err != nil {
		t.Fatal(err)
	}
	item := "/books/demo/notes/" + n.ID
	w := postForm(s, item, url.Values{"text": {"edited"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "edited") {
		t.Fatalf("update = %d %s", w.Code, w.Body)
	}
	// Book page list: plain links, no reader hooks.
	if body := w.Body.String(); strings.Contains(body, "notesChanged") || strings.Contains(body, `hx-get=`) {
		t.Error("book-page list carries reader hooks")
	}
	if code := postForm(s, item, url.Values{"text": {strings.Repeat("x", maxNoteRunes+1)}}).Code; code != 400 {
		t.Errorf("too long update = %d, want 400", code)
	}
	for path, want := range map[string]int{
		"/books/demo/notes/0000000000000000": 404,
		"/books/demo/notes/not-an-id":        404,
		"/books/nope/notes/" + n.ID:          404,
	} {
		if code := postForm(s, path, url.Values{"text": {"x"}}).Code; code != want {
			t.Errorf("POST %s = %d, want %d", path, code, want)
		}
		if code := do(s, httptest.NewRequest("DELETE", path, nil)).Code; code != want {
			t.Errorf("DELETE %s = %d, want %d", path, code, want)
		}
	}
	w = do(s, httptest.NewRequest("DELETE", item+"?view=reader", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No notes yet") || !strings.Contains(w.Body.String(), "notesChanged") {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
	if code := do(s, httptest.NewRequest("DELETE", item, nil)).Code; code != 404 {
		t.Errorf("second delete = %d, want 404", code)
	}
}

// Notes show on the book page (plain links) and in the reader's drawer
// (htmx links that swap #reader at the note's time).
func TestNotesRendered(t *testing.T) {
	s, ns := newNotesServer(t)
	book := do(s, httptest.NewRequest("GET", "/books/demo", nil)).Body.String()
	if strings.Contains(book, `id="notes"`) {
		t.Error("book page shows notes section with no notes")
	}
	if _, err := ns.Add("demo", notes.Note{Chapter: 2, Start: 0.5, End: 0.5, Quote: "quoted bit", Text: "thought"}); err != nil {
		t.Fatal(err)
	}
	book = do(s, httptest.NewRequest("GET", "/books/demo", nil)).Body.String()
	for _, want := range []string{`id="notes"`, `href="/books/demo/ch/2?t=0.500"`, "quoted bit", "thought", "Chapter Two"} {
		if !strings.Contains(book, want) {
			t.Errorf("book page missing %q", want)
		}
	}
	reader := do(s, httptest.NewRequest("GET", "/books/demo/ch/1", nil)).Body.String()
	for _, want := range []string{
		`x-ref="notes"`,
		`x-on:click="bookmark()"`,
		`x-on:selectionchange.document="selChanged()"`,
		`hx-post="/books/demo/notes?view=reader"`,
		`hx-get="/books/demo/ch/2?t=0.500"`,
		`x-init="notesChanged()"`,
		"quoted bit",
	} {
		if !strings.Contains(reader, want) {
			t.Errorf("reader missing %q", want)
		}
	}
	if code := do(s, httptest.NewRequest("GET", "/books/demo/ch/2?t=0.500", nil)).Code; code != 200 {
		t.Errorf("note link = %d", code)
	}
}

// The ✎ button opens the drawer through openNotes, which scrolls to the
// nearest note using the list's data-ch/data-start.
func TestReaderNotesDrawerOpensAtNearest(t *testing.T) {
	s, ns := newNotesServer(t)
	for _, n := range []notes.Note{
		{Chapter: 1, Start: 0.5, End: 0.5},
		{Chapter: 2, Start: 0.5, End: 0.5},
	} {
		if _, err := ns.Add("demo", n); err != nil {
			t.Fatal(err)
		}
	}
	reader := do(s, httptest.NewRequest("GET", "/books/demo/ch/2", nil)).Body.String()
	for _, want := range []string{
		`x-on:click="openNotes()"`,
		`data-ch="1"`,
		`data-ch="2"`,
		`data-start="0.500"`,
	} {
		if !strings.Contains(reader, want) {
			t.Errorf("reader missing %q", want)
		}
	}
	if strings.Contains(reader, `x-on:click="notes = true"`) {
		t.Error("✎ button opens the drawer without openNotes")
	}
	js, err := os.ReadFile("static/sync.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), "openNotes() {") {
		t.Error("sync.js has no openNotes method")
	}
}

func TestQuoteFor(t *testing.T) {
	ws := func(start float64, ts ...string) []bundle.Word {
		out := make([]bundle.Word, len(ts))
		for i, tx := range ts {
			out[i] = bundle.Word{T: tx, S: start + float64(i), E: start + float64(i) + 0.5}
		}
		return out
	}
	a := bundle.Alignment{Blocks: []bundle.Block{
		{Kind: bundle.KindParagraph, Words: ws(0, "a", "b", "c")},
		{Kind: bundle.KindParagraph, Words: ws(3, "d", "e")},
	}}
	cases := []struct {
		start, end float64
		want       string
	}{
		{0, 0.9, "a"},
		{1, 3, "b c d"},   // spans blocks
		{1, 1, "b c"},     // bookmark stops at its block's end
		{2.5, 2.5, "d e"}, // bookmark between words starts at the next one
		{9, 9, ""},
	}
	for _, c := range cases {
		if got := quoteFor(a, c.start, c.end); got != c.want {
			t.Errorf("quoteFor(%v, %v) = %q, want %q", c.start, c.end, got, c.want)
		}
	}
	long := bundle.Alignment{Blocks: []bundle.Block{{Words: ws(0, strings.Split(strings.Repeat("w ", maxQuoteWords+5), " ")[:maxQuoteWords+5]...)}}}
	got := quoteFor(long, 0, 1000)
	if !strings.HasSuffix(got, " …") || strings.Count(got, "w") != maxQuoteWords {
		t.Errorf("long quote not cut: %q", got)
	}
}

// upload builds a POST /import with body as the "file" form field.
func upload(t *testing.T, body []byte) *http.Request { t.Helper(); return uploadTo(t, "/import", body) }

func uploadTo(t *testing.T, path string, body []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "backup.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", path, &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

func TestExportImport(t *testing.T) {
	src, store := newTestServer(t)
	if err := store.Save("demo", progress.Position{Chapter: 2, Time: 1.5}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"chapter": {"1"}, "start": {"0.000"}, "end": {"0.000"}, "text": {"carry me"}}
	r := httptest.NewRequest("POST", "/books/demo/notes", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w := do(src, r); w.Code != http.StatusOK {
		t.Fatalf("add note = %d %s", w.Code, w.Body)
	}

	lib := do(src, httptest.NewRequest("GET", "/", nil)).Body.String()
	if !strings.Contains(lib, `href="/export"`) || !strings.Contains(lib, `action="/import"`) {
		t.Error("library page missing export/import controls")
	}

	w := do(src, httptest.NewRequest("GET", "/export", nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Disposition"), `attachment; filename="reader-data-`) {
		t.Fatalf("export = %d, disposition %q", w.Code, w.Header().Get("Content-Disposition"))
	}
	exported := w.Body.Bytes()

	dst, _ := newTestServer(t)
	if w := do(dst, upload(t, exported)); w.Code != http.StatusSeeOther {
		t.Fatalf("import = %d %s", w.Code, w.Body)
	}
	book := do(dst, httptest.NewRequest("GET", "/books/demo", nil)).Body.String()
	if !strings.Contains(book, "carry me") || !strings.Contains(book, "/books/demo/ch/2?t=1.500") {
		t.Error("imported note or progress missing from book page")
	}

	for name, req := range map[string]*http.Request{
		"bad json": upload(t, []byte(`{"version": 99}`)),
		"too big":  upload(t, bytes.Repeat([]byte(" "), maxImport+1)),
		"no file":  httptest.NewRequest("POST", "/import", nil),
	} {
		if w := do(dst, req); w.Code != http.StatusBadRequest {
			t.Errorf("%s: import = %d, want 400", name, w.Code)
		}
	}
}

func TestNoteCounts(t *testing.T) {
	s, ns := newNotesServer(t)
	for _, n := range []notes.Note{
		{Chapter: 1, Start: 1, End: 1, Quote: "a", Text: "x"},
		{Chapter: 1, Start: 2, End: 2, Quote: "b", Text: "y"},
		{Chapter: 1, Start: 3, End: 3, Quote: "c"},
	} {
		if _, err := ns.Add("demo", n); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/books/demo", "/books/demo/ch/2"} {
		body := do(s, httptest.NewRequest("GET", path, nil)).Body.String()
		for _, want := range []string{`id="note-count-1"`, "✎ 2", "🔖 1", `id="note-count-2"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing %q", path, want)
			}
		}
	}

	// Every note change sends all counts out of band, zeros included.
	w := postForm(s, "/books/demo/notes?view=reader", url.Values{"chapter": {"2"}, "start": {"0"}, "end": {"0"}, "text": {"z"}})
	body := w.Body.String()
	if w.Code != 200 || strings.Count(body, `hx-swap-oob="true"`) != 2 || !strings.Contains(body, "✎ 1") {
		t.Fatalf("note add response = %d %s", w.Code, body)
	}
}

func TestBookPageFoldsNotes(t *testing.T) {
	s, ns := newNotesServer(t)
	for i := range 4 {
		if _, err := ns.Add("demo", notes.Note{Chapter: 1, Start: float64(i), End: float64(i), Quote: "q"}); err != nil {
			t.Fatal(err)
		}
	}
	book := do(s, httptest.NewRequest("GET", "/books/demo", nil)).Body.String()
	if !strings.Contains(book, "Show all 4 notes") || strings.Count(book, `x-show="all"`) != 2 { // 4th note + "Show fewer"
		t.Errorf("book page should fold the 4th note behind Show all")
	}
	reader := do(s, httptest.NewRequest("GET", "/books/demo/ch/1", nil)).Body.String()
	if strings.Contains(reader, "Show all") || strings.Contains(reader, `x-show="all"`) {
		t.Error("reader drawer must not fold notes")
	}
}

func TestShareNotes(t *testing.T) {
	src, store := newTestServer(t)
	form := url.Values{"chapter": {"1"}, "start": {"0.000"}, "end": {"0.000"}, "text": {"for you"}}
	if w := postForm(src, "/books/demo/notes", form); w.Code != http.StatusOK {
		t.Fatalf("add note = %d %s", w.Code, w.Body)
	}
	if err := store.Save("demo", progress.Position{Chapter: 2, Time: 1.5}); err != nil {
		t.Fatal(err)
	}
	book := do(src, httptest.NewRequest("GET", "/books/demo", nil)).Body.String()
	if !strings.Contains(book, `action="/books/demo/notes/export"`) || !strings.Contains(book, `action="/books/demo/notes/import"`) {
		t.Error("book page missing share controls")
	}

	bad := do(src, httptest.NewRequest("GET", "/books/demo/notes/export?name="+strings.Repeat("x", 61), nil))
	if bad.Code != http.StatusBadRequest {
		t.Errorf("long name = %d, want 400", bad.Code)
	}
	w := do(src, httptest.NewRequest("GET", "/books/demo/notes/export?name=+Kari+", nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Disposition"), `attachment; filename="demo-notes-`) {
		t.Fatalf("export = %d, disposition %q", w.Code, w.Header().Get("Content-Disposition"))
	}
	exported := w.Body.Bytes()
	if !bytes.Contains(exported, []byte(`"author": "Kari"`)) || bytes.Contains(exported, []byte(`"chapter": 2,`)) {
		t.Errorf("export should stamp author and carry no progress: %s", exported)
	}

	dst, dstStore := newTestServer(t)
	if w := do(dst, uploadTo(t, "/books/figures/notes/import", exported)); w.Code != http.StatusBadRequest {
		t.Errorf("wrong-book import = %d, want 400", w.Code)
	}
	w = do(dst, uploadTo(t, "/books/demo/notes/import", exported))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/books/demo" {
		t.Fatalf("import = %d %q %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	got := do(dst, httptest.NewRequest("GET", "/books/demo", nil)).Body.String()
	if !strings.Contains(got, "for you") || !strings.Contains(got, "from Kari") {
		t.Error("imported note or its author missing from book page")
	}
	if _, ok, err := dstStore.Get("demo"); err != nil || ok {
		t.Errorf("notes import must not touch progress: ok %v err %v", ok, err)
	}
}

func TestReaderRendersStructure(t *testing.T) {
	s, _ := newTestServer(t)
	body := do(s, httptest.NewRequest("GET", "/books/structured/ch/1", nil)).Body.String()
	for _, want := range []string{
		// The listing keeps its line break and indentation, each word a span.
		`<code><span data-s="8.000">def</span> <span data-s="8.300" title="f open paren x close paren colon.">f(x):</span>` +
			"\n    " + `<span data-s="9.800">return</span>`,
		`<th scope="col"`,
		`<pre data-hscroll`, // sync.js follows the word sideways in these
		`<div data-hscroll class="overflow-x-auto">`,
		`<td class="px-3 py-2 align-top"><span data-s="12.600" title="Name: Alice,">Alice</span> </td>`,
		`<blockquote`,
		`<ul class="space-y-1">`,
		`>◦</b>`,
		`class="flex gap-2 pl-6"`, // depth 1
		// Shown "$5," but spoken "five dollars,": the hover tells.
		`<span data-s="2.200" title="five dollars,">$5,</span>`,
		`<span data-s="1.800">costs</span>`, // read as shown: no title
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if n := strings.Count(body, "<span data-s="); n != 22 {
		t.Errorf("rendered %d word spans, want every word (22)", n)
	}
	if n := strings.Count(body, "<ul"); n != 1 {
		t.Errorf("rendered %d lists, want the two items in one", n)
	}
}

// Chapter rows show ✓ when finished, a bar and % while part heard, and
// nothing for a chapter only opened (a save under progress.StartedMin).
func TestChapterStatus(t *testing.T) {
	s, store := newTestServer(t)
	save := func(ch, at string) {
		t.Helper()
		w := postForm(s, "/books/demo/progress", url.Values{"chapter": {ch}, "time": {at}})
		if w.Code != http.StatusNoContent {
			t.Fatalf("POST progress %s@%s = %d", ch, at, w.Code)
		}
	}
	save("2", "0.5") // opened chapter 2, barely played
	body := do(s, httptest.NewRequest("GET", "/books/demo", nil)).Body.String()
	if strings.Contains(body, "<progress") || strings.Contains(body, `title="Completed"`) {
		t.Error("an opened-but-unplayed chapter shows progress")
	}
	if !strings.Contains(body, "0 of 2 done") {
		t.Error("book page missing done count")
	}
	save("1", "1.5") // 1.5 of 4s
	save("2", "1.5") // within DoneSlack of 2s: done
	pos, _, err := store.Get("demo")
	if err != nil || !pos.Chapters[2].Done || pos.Chapters[1].Furthest != 1.5 {
		t.Fatalf("marks = %+v, %v", pos.Chapters, err)
	}
	for _, page := range []string{"/books/demo", "/books/demo/ch/1"} {
		body = do(s, httptest.NewRequest("GET", page, nil)).Body.String()
		for _, want := range []string{`value="0.375"`, "37%", `title="Completed"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing %q", page, want)
			}
		}
	}
	body = do(s, httptest.NewRequest("GET", "/books/demo", nil)).Body.String()
	// 1 of 2 done; 2.5s of chapter 1 left, shown at the chosen speed.
	if !strings.Contains(body, "1 of 2 done") || !strings.Contains(body, `x-text="$est(2.500)"`) {
		t.Error("book page summary wrong")
	}
}

// Every chapter row is a dropdown that loads its subchapters on first open;
// the reader's current chapter is open with them rendered already.
func TestChapterSections(t *testing.T) {
	s, _ := newTestServer(t)
	book := do(s, httptest.NewRequest("GET", "/books/demo", nil)).Body.String()
	for _, want := range []string{
		`hx-get="/books/demo/ch/1/sections"`,
		`hx-trigger="toggle once"`,
		`hx-target="find ol"`,
	} {
		if !strings.Contains(book, want) {
			t.Errorf("book page missing %q", want)
		}
	}
	reader := do(s, httptest.NewRequest("GET", "/books/demo/ch/1", nil)).Body.String()
	if !strings.Contains(reader, `hx-get="/books/demo/ch/2/sections?view=reader"`) {
		t.Error("drawer rows do not load sections in reader view")
	}
	if strings.Contains(reader, `hx-get="/books/demo/ch/1/sections`) {
		t.Error("current chapter should render its sections inline, not load them")
	}
	if !strings.Contains(reader, "<details class=\"group\" open") {
		t.Error("current chapter's dropdown not open")
	}
	frag := do(s, httptest.NewRequest("GET", "/books/structured/ch/1/sections", nil)).Body.String()
	if !strings.Contains(frag, "No sections") {
		t.Errorf("sections fragment = %q", frag)
	}
}

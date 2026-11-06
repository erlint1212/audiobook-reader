package progress

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestSaveGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Get("demo"); err != nil || ok {
		t.Fatalf("empty Get = ok %v err %v", ok, err)
	}
	want := Position{Chapter: 2, Time: 12.5, Updated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	for range 2 { // second save overwrites
		if err := s.Save("demo", want); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := s.Get("demo")
	if err != nil || !ok || !got.Updated.Equal(want.Updated) || got.Chapter != want.Chapter || got.Time != want.Time {
		t.Fatalf("Get = %+v ok %v err %v, want %+v", got, ok, err, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want 1 (no temp leftovers)", len(entries))
	}
}

func TestBooks(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"zeta", "alpha"} {
		if err := s.Save(id, Position{Chapter: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Bad Name.json", "x.json.1.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := s.Books()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"alpha", "zeta"}; !slices.Equal(got, want) {
		t.Fatalf("Books = %v, want %v", got, want)
	}
}

func open(t *testing.T) Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, s Store, id string) Position {
	t.Helper()
	pos, ok, err := s.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get = ok %v err %v", ok, err)
	}
	return pos
}

func TestRecordMarks(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	steps := []struct {
		ch      int
		t, dur  float64
		want    Mark
		started bool
	}{
		{1, 0.5, 100, Mark{}, false},                           // opened, barely played: unstarted
		{1, 40, 100, Mark{Furthest: 40}, true},                 // in progress
		{1, 10, 100, Mark{Furthest: 40}, true},                 // seeking back keeps furthest
		{1, 98.5, 100, Mark{Furthest: 98.5, Done: true}, true}, // within DoneSlack: done
		{1, 5, 100, Mark{Furthest: 98.5, Done: true}, true},    // done stays done
	}
	for i, st := range steps {
		if err := s.Record("demo", st.ch, st.t, st.dur, now); err != nil {
			t.Fatal(err)
		}
		pos := get(t, s, "demo")
		if pos.Chapter != st.ch || pos.Time != st.t {
			t.Fatalf("step %d: position %d@%v, want %d@%v", i, pos.Chapter, pos.Time, st.ch, st.t)
		}
		m, ok := pos.Chapters[st.ch]
		if ok != st.started || m != st.want || m.Started() != st.started {
			t.Fatalf("step %d: mark %+v ok %v, want %+v ok %v", i, m, ok, st.want, st.started)
		}
	}
}

func TestMarkFraction(t *testing.T) {
	for _, c := range []struct {
		m    Mark
		dur  float64
		want float64
	}{
		{Mark{Furthest: 25}, 100, 0.25},
		{Mark{Furthest: 120}, 100, 1},
		{Mark{Furthest: 5, Done: true}, 100, 1},
		{Mark{Furthest: 5}, 0, 0},
		{Mark{}, 100, 0},
	} {
		if got := c.m.Fraction(c.dur); got != c.want {
			t.Errorf("%+v.Fraction(%v) = %v, want %v", c.m, c.dur, got, c.want)
		}
	}
}

func TestMergeKeepsNewerPositionAndAllMarks(t *testing.T) {
	s := open(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Record("demo", 3, 50, 100, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	old := Position{Chapter: 1, Time: 7, Updated: t0, Chapters: map[int]Mark{
		1: {Furthest: 100, Done: true},
		3: {Furthest: 20},
	}}
	if err := s.Merge("demo", old); err != nil {
		t.Fatal(err)
	}
	pos := get(t, s, "demo")
	if pos.Chapter != 3 || pos.Time != 50 {
		t.Fatalf("older import moved position to %d@%v", pos.Chapter, pos.Time)
	}
	want := map[int]Mark{1: {Furthest: 100, Done: true}, 3: {Furthest: 50}}
	if !maps.Equal(pos.Chapters, want) {
		t.Fatalf("marks = %v, want %v", pos.Chapters, want)
	}
	newer := Position{Chapter: 2, Time: 3, Updated: t0.Add(2 * time.Hour)}
	if err := s.Merge("demo", newer); err != nil {
		t.Fatal(err)
	}
	pos = get(t, s, "demo")
	if pos.Chapter != 2 || pos.Time != 3 || !maps.Equal(pos.Chapters, want) {
		t.Fatalf("newer import = %+v", pos)
	}
}

func TestRecordConcurrentKeepsEveryChapter(t *testing.T) {
	s := open(t)
	var wg sync.WaitGroup
	for ch := 1; ch <= 20; ch++ {
		wg.Go(func() {
			if err := s.Record("demo", ch, 10, 100, time.Now()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := len(get(t, s, "demo").Chapters); n != 20 {
		t.Fatalf("%d marks, want 20", n)
	}
}

package progress

import (
	"os"
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

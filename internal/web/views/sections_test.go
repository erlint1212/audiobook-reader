package views

import (
	"slices"
	"testing"

	"audiobook-reader/internal/bundle"
)

func heading(text string, s float64) bundle.Block {
	return bundle.Block{Kind: bundle.KindHeading, Words: []bundle.Word{{T: text, S: s, E: s + 0.5}}}
}

func TestSections(t *testing.T) {
	a := bundle.Alignment{Blocks: []bundle.Block{
		{Kind: bundle.KindFigure, Image: "images/cover.png"},
		heading("Chapter 1. Intro", 0),
		heading("A subtitle", 0.5),
		{Kind: bundle.KindParagraph, Words: []bundle.Word{{T: "text", S: 1, E: 2}}},
		heading("Note", 5),
		heading("Why SRE", 10),
		heading("Figure 1-1. A pyramid", 12),
		heading("Tables", 20),
		heading("Warning:", 25),
		heading("Conclusion", 30),
		{Kind: bundle.KindHeading}, // no words: skipped
	}}
	got := sections(a, bundle.Chapter{DurationS: 40})
	want := []section{
		{Title: "Why SRE", Start: 10, Dur: 10},
		{Title: "Tables", Start: 20, Dur: 10},
		{Title: "Conclusion", Start: 30, Dur: 10},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("sections = %+v, want %+v", got, want)
	}
}

func TestSectionsNone(t *testing.T) {
	a := bundle.Alignment{Blocks: []bundle.Block{heading("Only the chapter title", 0)}}
	if got := sections(a, bundle.Chapter{DurationS: 4}); len(got) != 0 {
		t.Fatalf("sections = %+v, want none", got)
	}
}

package views

import (
	"strings"
	"unicode"

	"audiobook-reader/internal/bundle"
)

// section is one subchapter: a heading inside a chapter, from its first
// word to the next section (or the chapter end).
type section struct {
	Title string
	Start float64
	Dur   float64
}

// calloutTitles are one-word headings forge keeps for boxed notes. They are
// not sections a listener wants to jump to.
var calloutTitles = []string{"note", "tip", "warning", "caution", "important"}

// numberedPrefixes start captions like "Figure 1-1. …"; only skipped when a
// digit follows, so a section called "Tables" stays.
var numberedPrefixes = []string{"Figure ", "Table ", "Example "}

// sections lists a chapter's subchapters from its heading blocks. Headings
// before the first body block are the chapter's own title (maybe after a
// cover picture) and are skipped, as are callouts and captions. Bundles
// carry no heading level, so the list is flat.
func sections(a bundle.Alignment, ch bundle.Chapter) []section {
	var out []section
	inBody := false
	for _, blk := range a.Blocks {
		if blk.Kind != bundle.KindHeading {
			inBody = inBody || blk.Kind != bundle.KindFigure
			continue
		}
		if !inBody || len(blk.Words) == 0 {
			continue
		}
		title := headingText(blk.Words)
		if isNoiseHeading(title) {
			continue
		}
		out = append(out, section{Title: title, Start: blk.Words[0].S})
	}
	for i := range out {
		end := ch.DurationS
		if i+1 < len(out) {
			end = out[i+1].Start
		}
		out[i].Dur = max(0, end-out[i].Start)
	}
	return out
}

func headingText(ws []bundle.Word) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = w.T
	}
	return strings.Join(parts, " ")
}

func isNoiseHeading(title string) bool {
	bare := strings.TrimRight(title, ":.")
	for _, c := range calloutTitles {
		if strings.EqualFold(bare, c) {
			return true
		}
	}
	for _, p := range numberedPrefixes {
		rest, ok := strings.CutPrefix(title, p)
		if ok && rest != "" && unicode.IsDigit(rune(rest[0])) {
			return true
		}
	}
	return false
}

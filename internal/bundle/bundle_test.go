package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const (
	golden   = "../../testdata/golden-bundle/demo"    // schema v1
	goldenV2 = "../../testdata/golden-bundle/figures" // schema v2, pictures
)

func TestLoadGolden(t *testing.T) {
	b, err := Load(golden)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if b.Manifest.ID != "demo" || len(b.Manifest.Chapters) != 2 {
		t.Fatalf("unexpected manifest: %+v", b.Manifest)
	}
	want := []int{6, 2}
	for i, ch := range b.Manifest.Chapters {
		a, err := b.LoadAlignment(ch)
		if err != nil {
			t.Fatalf("LoadAlignment(%d): %v", ch.Index, err)
		}
		if got := a.WordCount(); got != want[i] {
			t.Errorf("chapter %d word count = %d, want %d", ch.Index, got, want[i])
		}
	}
}

func TestChapterBounds(t *testing.T) {
	b, err := Load(golden)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 3, -1} {
		if _, err := b.Chapter(i); err != ErrNoChapter {
			t.Errorf("Chapter(%d) err = %v, want ErrNoChapter", i, err)
		}
	}
}

func TestLoadGoldenV2(t *testing.T) {
	b, err := Load(goldenV2)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a, err := b.LoadAlignment(b.Manifest.Chapters[0])
	if err != nil {
		t.Fatalf("LoadAlignment: %v", err)
	}
	var kinds []BlockKind
	for _, blk := range a.Blocks {
		kinds = append(kinds, blk.Kind)
	}
	want := []BlockKind{KindFigure, KindHeading, KindParagraph, KindFigure, KindParagraph}
	if !slices.Equal(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	if got := b.ImagePath(a.Blocks[3]); got != filepath.Join(goldenV2, "images", "001.png") {
		t.Errorf("ImagePath = %q", got)
	}
	if got := b.ImagePath(a.Blocks[2]); got != "" {
		t.Errorf("ImagePath(paragraph) = %q, want empty", got)
	}
}

// copyGolden copies the golden bundle into a temp dir and applies edit to the
// named JSON file, so each test breaks exactly one rule.
func copyGolden(t *testing.T, file string, edit func(map[string]any)) string {
	t.Helper()
	return copyBundle(t, golden, file, edit)
}

func copyBundle(t *testing.T, src, file string, edit func(map[string]any)) string {
	t.Helper()
	dst := t.TempDir()
	err := os.CopyFS(dst, os.DirFS(src))
	if err != nil {
		t.Fatal(err)
	}
	if file == "" {
		return dst
	}
	p := filepath.Join(dst, file)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func firstWords(doc map[string]any) []any {
	return doc["blocks"].([]any)[1].(map[string]any)["words"].([]any)
}

func TestManifestRejects(t *testing.T) {
	cases := map[string]func(map[string]any){
		"schema version": func(d map[string]any) { d["schema_version"] = 3 },
		"bad id":         func(d map[string]any) { d["id"] = "Bad ID" },
		"unknown field":  func(d map[string]any) { d["extra"] = true },
		"path escape": func(d map[string]any) {
			d["chapters"].([]any)[0].(map[string]any)["audio"] = "../../etc/passwd"
		},
		"index gap": func(d map[string]any) {
			d["chapters"].([]any)[1].(map[string]any)["index"] = 5
		},
		"no chapters": func(d map[string]any) { d["chapters"] = []any{} },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(copyGolden(t, ManifestFile, edit)); err == nil {
				t.Fatal("Load succeeded, want error")
			}
		})
	}
}

func TestMissingAudio(t *testing.T) {
	dir := copyGolden(t, "", nil)
	if err := os.Remove(filepath.Join(dir, "chapters", "002.opus")); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load succeeded with missing audio")
	}
}

func TestAlignmentRejects(t *testing.T) {
	cases := map[string]func(map[string]any){
		"starts go backwards": func(d map[string]any) {
			firstWords(d)[2].(map[string]any)["s"] = 0.5
		},
		"end before start": func(d map[string]any) {
			firstWords(d)[0].(map[string]any)["e"] = 0.9
		},
		"past duration": func(d map[string]any) {
			firstWords(d)[3].(map[string]any)["e"] = 99.0
		},
		"wrong chapter":        func(d map[string]any) { d["chapter_index"] = 2 },
		"unknown kind":         func(d map[string]any) { d["blocks"].([]any)[0].(map[string]any)["kind"] = "img" },
		"schema version":       func(d map[string]any) { d["schema_version"] = 0 },
		"not manifest version": func(d map[string]any) { d["schema_version"] = 2 },
		"picture in v1": func(d map[string]any) {
			d["blocks"] = append(d["blocks"].([]any), map[string]any{
				"kind": "fig", "image": "chapters/001.opus", "words": []any{},
			})
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			dir := copyGolden(t, "chapters/001.align.json", edit)
			b, err := Load(dir)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if _, err := b.LoadAlignment(b.Manifest.Chapters[0]); err == nil {
				t.Fatal("LoadAlignment succeeded, want error")
			}
		})
	}
}

func TestIsSlug(t *testing.T) {
	for s, want := range map[string]bool{
		"demo": true, "a-1": true, "": false, "-a": false, "A": false, "a b": false, "ä": false,
	} {
		if got := isSlug(s); got != want {
			t.Errorf("isSlug(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestPictureRejects(t *testing.T) {
	block := func(d map[string]any, i int) map[string]any {
		return d["blocks"].([]any)[i].(map[string]any)
	}
	cases := map[string]func(map[string]any){
		"no image":         func(d map[string]any) { delete(block(d, 0), "image") },
		"image escapes":    func(d map[string]any) { block(d, 0)["image"] = "../../etc/passwd" },
		"absolute image":   func(d map[string]any) { block(d, 0)["image"] = "/etc/passwd" },
		"missing image":    func(d map[string]any) { block(d, 0)["image"] = "images/nope.png" },
		"image on heading": func(d map[string]any) { block(d, 1)["image"] = "images/001.png" },
		"empty paragraph":  func(d map[string]any) { block(d, 2)["words"] = []any{} },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := Load(copyBundle(t, goldenV2, "chapters/001.align.json", edit))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if _, err := b.LoadAlignment(b.Manifest.Chapters[0]); err == nil {
				t.Fatal("LoadAlignment succeeded, want error")
			}
		})
	}
}

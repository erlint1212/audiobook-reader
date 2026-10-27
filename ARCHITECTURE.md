# audiobook-reader — architecture

Web reader for audiobooks. It plays a chapter, highlights the word being spoken,
and scrolls the text along. Its sibling repo **audiobook-forge** (Python) makes
the bundles this repo reads. The two repos share **nothing but the bundle format**.

## Big picture

```
EPUB ──forge──▶ bundle dir (manifest.json, NNN.opus, NNN.align.json, images/) ──reader──▶ browser
```

Bundles are schema v1 or v2. v2 adds picture blocks: `{"kind": "fig",
"image": "images/001.png", "words": [...]}`, where words are the caption
(else alt text) and may be empty. The reader renders a `<figure>` with the
image and the words as a `<figcaption>`. While a caption word is active the
figure gets a ring through Tailwind `has-[span[data-active]]:` (CSS `:has()`),
so sync.js needs no picture logic beyond keeping the whole figure in view.

The reader scans `-library` once at startup. Each request renders HTML on the
server (templ). The browser gets one `<span data-s="start">` per word. A small
Alpine component follows `audio.currentTime` and moves the highlight.

## Stack and why

| Piece | Choice | Why |
|---|---|---|
| Language | Go 1.26, `CGO_ENABLED=0` | Server-first, one static binary, no C toolchain. |
| Routing | stdlib `net/http` ServeMux patterns | Method + `{id}` patterns built in since 1.22; no framework needed. |
| Templates | `a-h/templ`, pinned as a `go tool` in go.mod | Typed, compiled HTML components. |
| Partial updates | HTMX **v4** (CDN, pinned) | Chapter prev/next swaps `#reader` with no full reload. Progress saves through a hidden form. |
| Client behaviour | Alpine.js (CDN, pinned) | Word sync needs ~100 lines; Alpine keeps them declarative. |
| Styling | TailwindCSS v4 standalone CLI from nixpkgs | Only Tailwind; no npm. All styling lives in templ files. |
| Assets | `go:embed internal/web/static` | Binary is self-contained (after `make css`). |
| State | One JSON file per book in `-data` (XDG state dir) | Single-user; atomic temp+rename writes; no DB. |

## Packages

- `cmd/reader`: flags, wiring, graceful shutdown.
- `internal/bundle`: Go types for bundle v1/v2, strict decode (`DisallowUnknownFields`), and
  semantic checks the JSON Schema cannot express: chapter indexes 1..n, starts
  never going backwards, `e >= s`, words inside duration, paths staying inside the bundle,
  `fig` only in v2 with an existing local image, alignment version = manifest version.
- `internal/library`: immutable index built by `Scan`. Bad bundles get logged and
  skipped; duplicate ids are a hard error.
- `internal/progress`: per-book position store.
- `internal/web`: handlers + routes. `views/` holds templ components and
  `static/` holds `sync.js` + Tailwind input.

## Routes

| Route | What |
|---|---|
| `GET /` | library grid |
| `GET /books/{id}` | chapter list + resume button |
| `GET /books/{id}/ch/{n}[?t=sec]` | reader; with `HX-Request: true` returns only `<section id="reader">` |
| `GET /books/{id}/audio/{n}` | audio via `http.ServeContent` (Range → 206, needed for seeking) |
| `GET /books/{id}/cover` | cover image if the manifest has one |
| `GET /books/{id}/ch/{n}/img/{b}` | image of picture block `b` (0-based block index); 404 if not a `fig` |
| `POST /books/{id}/progress` | form `chapter`, `time` → 204 |

## Rules for working in this repo

1. **The bundle format is a contract.** `schema/bundle.v*.schema.json` and
   `testdata/golden-bundle/` must stay byte-identical with audiobook-forge.
   Golden `demo` is v1, `figures` is v2 (pictures). A breaking change means a
   new `schema_version` in both repos, plus a reader that still loads the old ones
   (`MinSchemaVersion`..`SchemaVersion`).
2. **Server renders, client syncs.** New UI is templ + HTMX first. Add JS only
   if it must run every frame or touch media APIs, and put it in
   `static/sync.js`. No JS build step, no npm.
3. **Tailwind only.** No hand-written CSS rules. Style state through attributes
   (`data-active`) and Tailwind arbitrary variants, as `Reader` does.
4. **Never build a file path from request input.** Paths come from the
   validated manifest/alignment only (`Book.AudioPath`, `CoverPath`, `ImagePath`);
   requests carry indexes, never paths.
5. **Library and Server are immutable after construction.** No locks, no
   globals. Restart the server to pick up new books.
6. **Generated `*_templ.go` files are committed.** Run `make generate` after
   editing `.templ`; `make check` fails if they are stale.
7. **Every fix or feature gets a test.** Handler tests use the golden bundle via
   `httptest`.
8. **htmx 4 is not htmx 2.** Attribute inheritance is explicit, and some
   attributes were renamed (`hx-disable`, `hx-config`, ...). Check the v4 docs
   before adding attributes.
9. **No regex.** Validation is plain Go (see `isSlug`).

## Dev on NixOS

```
nix develop        # go, gopls, tailwindcss_4, make
make run           # serves testdata/golden-bundle on http://127.0.0.1:8080
make run LIBRARY=~/audiobooks
make check         # fmt, vet, tests, templ freshness
nix build          # first run: copy the reported vendorHash into flake.nix
```

The golden bundle's `.opus` files are placeholder bytes. Pages render, but
audio will not play; use a real forge bundle for that.

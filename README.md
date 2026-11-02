# audiobook-reader

Listen to an audiobook and follow along: the current word is highlighted and
the page scrolls with the narration. Click any word to jump there.

Reads bundles made by [audiobook-forge](../audiobook-forge).

```
nix develop
make run LIBRARY=/path/to/library
```

Keys: <kbd>Space</kbd> play/pause, <kbd>←</kbd>/<kbd>→</kbd> ±10 s.
Scroll by hand to pause follow mode; "Back to reading position" turns it back on.

Progress and notes live as JSON files in `-data` (default: your XDG state
dir), not in the browser. Back them up by copying that folder, or use
**Export** on the library page to download one file; **Import** merges such a
file back in without deleting anything newer.

To share notes on one book, use **Export notes** on the book page (add your
name so the receiver sees who wrote them). They import the file with
**Import** next to it; only notes are merged, never their reading position.

See [ARCHITECTURE.md](ARCHITECTURE.md).

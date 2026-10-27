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

See [ARCHITECTURE.md](ARCHITECTURE.md).

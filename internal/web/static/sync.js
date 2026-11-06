// Word-sync for the reader: the one piece of hand-written JS in this repo.
// The server renders each word as <span data-s="start">. This component
// follows audio.currentTime, marks the current word with data-active
// (styled by Tailwind in the template), keeps it in view, and saves progress.

// Reading prefs (reader.prefs) live as data attributes on <html>, so Tailwind
// variants like [[data-size=lg]_&]:text-xl style the page. This file loads in
// <head>, so they are set before first paint: no flash. Each key lists its
// allowed values; the first is the default.
const PREFS = {
  size: ["md", "sm", "lg", "xl"],
  font: ["sans", "serif", "mono"],
  leading: ["normal", "tight", "loose"],
  width: ["normal", "narrow", "wide"],
  theme: ["system", "light", "dark"],
};
const PREFS_KEY = "reader.prefs";
const darkQuery = window.matchMedia("(prefers-color-scheme: dark)");

// Unknown or missing values fall back to the default, so a bad stored value
// can never leave the page unstyled.
const cleanPrefs = (p) => {
  const out = {};
  for (const [k, allowed] of Object.entries(PREFS)) {
    out[k] = allowed.includes(p?.[k]) ? p[k] : allowed[0];
  }
  return out;
};
const loadPrefs = () => {
  try { return cleanPrefs(JSON.parse(localStorage.getItem(PREFS_KEY))); } catch { return cleanPrefs(null); }
};
const storePrefs = (p) => {
  try { localStorage.setItem(PREFS_KEY, JSON.stringify(p)); } catch { /* ignore */ }
};
// "system" theme resolves to light or dark here; the dark: variant only
// looks at data-theme.
const applyPrefs = (p) => {
  const root = document.documentElement.dataset;
  root.size = p.size;
  root.font = p.font;
  root.leading = p.leading;
  root.width = p.width;
  root.theme = p.theme === "system" ? (darkQuery.matches ? "dark" : "light") : p.theme;
};
applyPrefs(loadPrefs());
// OS theme changed: follow it, unless a fixed theme was picked.
darkQuery.addEventListener("change", () => applyPrefs(loadPrefs()));

document.addEventListener("alpine:init", () => {
  // Set when a chapter ends. The next reader (swapped in by htmx) sees it in
  // init() and starts playing. Shared across instances, so it lives out here.
  let autoplay = false;

  // Playback speed carries across chapters and reloads. Storage can be
  // blocked (private window); then speed just resets to 1x on reload.
  const SPEED_KEY = "reader.speed";
  const clampSpeed = (r) => Math.min(3, Math.max(0.5, Number(r) || 1));
  const loadSpeed = () => {
    try { return clampSpeed(localStorage.getItem(SPEED_KEY)); } catch { return 1; }
  };
  const storeSpeed = (r) => {
    try { localStorage.setItem(SPEED_KEY, String(r)); } catch { /* ignore */ }
  };

  // Volume works the same way. Mute is not stored: a reload is never silent.
  // 0 to MAX_VOLUME; above 1 a Web Audio gain boosts the book past what
  // audio.volume allows. Rounded so arrow-key steps do not drift.
  const VOLUME_KEY = "reader.volume";
  const MAX_VOLUME = 2;
  const clampVolume = (v) => {
    const n = Number(v);
    return Number.isFinite(n) ? Math.round(Math.min(MAX_VOLUME, Math.max(0, n)) * 100) / 100 : 1;
  };
  const loadVolume = () => {
    try { return clampVolume(localStorage.getItem(VOLUME_KEY) ?? 1); } catch { return 1; }
  };
  const storeVolume = (v) => {
    try { localStorage.setItem(VOLUME_KEY, String(v)); } catch { /* ignore */ }
  };

  Alpine.data("reader", () => {
    // Non-reactive state: DOM nodes and big arrays stay out of Alpine proxies.
    let words = [];
    let starts = [];
    let current = -1;
    let frame = 0;
    let lastSave = 0;
    // Set when the last known chapter ended. If the polled "Next →" link
    // shows up later (forge wrote another chapter), nextReady() goes on.
    let waiting = false;
    // This reader's chapter index, read from the root in init(). Methods run
    // from child expressions, where this.$el is the child, not the root.
    let chapter = "";
    // Start time the reader opened at (?t=), until audio metadata loads.
    let resume = 0;
    // Web Audio GainNode for volume above 100%, built on first need.
    let gain = null;

    const wake = () => {
      if (gain?.context.state === "suspended") gain.context.resume().catch(() => { /* next play retries */ });
    };

    // audio.volume stops at 1, so the rest goes to a GainNode. The graph is
    // kept once built: createMediaElementSource cannot be undone. If the
    // context starts suspended (no user gesture yet), "play" resumes it.
    // iOS Safari ignores audio.volume, but the gain still works there.
    const applyVolume = (audio, v) => {
      audio.volume = Math.min(1, v);
      if (!gain && v > 1) {
        const Ctx = window.AudioContext ?? window.webkitAudioContext;
        if (!Ctx) return;
        try {
          const ctx = new Ctx();
          const node = ctx.createGain();
          ctx.createMediaElementSource(audio).connect(node).connect(ctx.destination);
          gain = node;
        } catch { return; } // no boost; plain audio.volume still works
      }
      if (!gain) return;
      gain.gain.value = Math.max(1, v);
      wake();
    };

    // Last index whose start <= t, or -1 before the first word.
    const indexAt = (t) => {
      let lo = 0, hi = starts.length - 1, found = -1;
      while (lo <= hi) {
        const mid = (lo + hi) >> 1;
        if (starts[mid] <= t) { found = mid; lo = mid + 1; } else { hi = mid - 1; }
      }
      return found;
    };

    // keepInView works in the visible band between the sticky top bar and the
    // fixed player bar, so the spoken word never hides behind either.
    const keepInView = (w, top, bar) => {
      const lo = top.getBoundingClientRect().bottom;
      const hi = bar.getBoundingClientRect().top;
      const h = hi - lo;
      const centre = (r) => window.scrollBy({ top: (r.top + r.bottom) / 2 - (lo + hi) / 2, behavior: "smooth" });
      // While a picture's caption is read, keep the whole picture visible if
      // it fits; else follow the word as usual.
      const fig = w.closest("figure");
      const f = fig?.getBoundingClientRect();
      if (f && f.height < h * 0.9) {
        if (f.top < lo || f.bottom > hi) centre(f);
        return;
      }
      const r = w.getBoundingClientRect();
      if (r.top < lo + h * 0.2 || r.bottom > lo + h * 0.7) centre(r);
      // A wide code listing or table scrolls sideways in its own box: bring
      // the word into that box. scrollLeft, so only the box moves.
      const box = w.closest("[data-hscroll]");
      const b = box?.getBoundingClientRect();
      if (b && (r.left < b.left || r.right > b.right)) {
        box.scrollLeft += (r.left + r.right) / 2 - (b.left + b.right) / 2;
      }
    };

    return {
      follow: true,
      time: 0,
      duration: 0,
      playing: false,
      speed: 1,
      volume: 1,
      muted: false,
      settings: false, // reading settings dropdown open
      prefs: loadPrefs(),
      chapters: false,
      notes: false, // notes drawer open
      sel: false, // text selected inside the article: show "＋ Note"
      draft: null, // {start, end, quote} of the note being written
      mark: "", // start time sent by the bookmark form
      flash: "", // short status message ("Bookmarked")

      init() {
        words = Array.from(this.$refs.text.querySelectorAll("span[data-s]"));
        starts = words.map((w) => Number(w.dataset.s));
        chapter = this.$el.dataset.chapter;
        const audio = this.$refs.audio;
        const start = Number(this.$el.dataset.start) || 0;
        resume = start;
        if (start > 0) {
          audio.addEventListener("loadedmetadata", () => { audio.currentTime = start; }, { once: true });
        }
        this.setSpeed(loadSpeed());
        this.setVolume(loadVolume());
        audio.addEventListener("durationchange", () => { this.duration = audio.duration; });
        audio.addEventListener("play", () => { wake(); waiting = false; this.playing = true; this.loop(); });
        audio.addEventListener("pause", () => { this.playing = false; this.tick(); this.save(); });
        audio.addEventListener("seeked", () => this.tick());
        audio.addEventListener("ended", () => this.next());
        if (autoplay) {
          autoplay = false;
          window.scrollTo({ top: 0 });
          audio.play().catch(() => {}); // blocked autoplay: user presses play
        }
      },

      // Chapter done: follow the server-rendered "Next →" link (htmx swaps
      // #reader) and let the new reader start itself. Last known chapter:
      // stop, and wait for the polled link (see nextReady).
      next() {
        const link = this.$el.querySelector("a[rel=next]");
        if (!link) { waiting = true; return; }
        waiting = false;
        autoplay = true;
        link.click();
      },

      // x-init of the live "Next →" link. Runs on every render; acts only if
      // the chapter already ended. setTimeout lets htmx finish processing the
      // swapped-in link before it is clicked.
      nextReady() {
        if (waiting) setTimeout(() => this.next(), 0);
      },

      // Open the chapter drawer centred on the current chapter. scrollTop,
      // not scrollIntoView, so only the drawer scrolls, never the page.
      openChapters() {
        this.chapters = true;
        this.$nextTick(() => {
          const d = this.$refs.drawer;
          const cur = d.querySelector("[aria-current=page]");
          if (!cur) return;
          d.scrollTop = cur.offsetTop - d.clientHeight / 2 + cur.offsetHeight / 2;
          cur.focus({ preventScroll: true });
        });
      },

      // Open the notes drawer centred on the note nearest the current spot.
      // The list is in reading order: find the first note after (chapter,
      // time), then pick it or the one before. Same scrollTop trick as
      // openChapters, so the page text never moves.
      openNotes() {
        this.notes = true;
        this.$nextTick(() => {
          const d = this.$refs.notes;
          const items = Array.from(d.querySelectorAll("#notes li[data-ch]"));
          if (items.length === 0) return;
          const ch = Number(chapter);
          const t = this.$refs.audio.currentTime || resume;
          const chOf = (li) => Number(li.dataset.ch);
          let i = items.findIndex((li) => chOf(li) > ch || (chOf(li) === ch && Number(li.dataset.start) > t));
          if (i < 0) i = items.length; // every note is before us
          const prev = items[i - 1], next = items[i];
          const here = (li) => li !== undefined && chOf(li) === ch;
          let pick = prev || next;
          if (here(prev) && here(next)) {
            pick = t - Number(prev.dataset.start) <= Number(next.dataset.start) - t ? prev : next;
          } else if (here(next)) {
            pick = next;
          } else if (here(prev)) {
            pick = prev;
          }
          d.scrollTop = pick.offsetTop - d.clientHeight / 2 + pick.offsetHeight / 2;
          pick.querySelector("a")?.focus({ preventScroll: true });
        });
      },

      // Drawer link clicked (htmx does the swap). Keep playing if we were.
      pick() {
        autoplay = this.playing;
      },

      // Page scrolled by hand: stop following. Scrolling a drawer does not count.
      unfollow(e) {
        for (const r of [this.$refs.drawer, this.$refs.notes, this.$refs.settings]) {
          if (r.contains(e.target)) return;
        }
        this.follow = false;
      },

      // selectionchange: offer "＋ Note" while text in the article is selected.
      selChanged() {
        const s = document.getSelection();
        this.sel = !!s && !s.isCollapsed && s.rangeCount > 0 &&
          this.$refs.text.contains(s.getRangeAt(0).commonAncestorContainer);
      },

      // Turn the selection into a draft: the first and last selected words
      // give the times. The server builds the real quote from those.
      noteFromSelection() {
        const s = document.getSelection();
        if (!this.sel || !s) return;
        const hit = words.filter((w) => s.containsNode(w, true));
        if (hit.length === 0) return;
        const quote = s.toString().trim(); // the blockquote collapses whitespace
        this.draft = {
          start: hit[0].dataset.s,
          end: hit[hit.length - 1].dataset.s,
          quote: quote.length > 300 ? quote.slice(0, 300) + "…" : quote,
        };
        s.removeAllRanges();
        this.sel = false;
        this.notes = true;
        this.$nextTick(() => this.$refs.noteText.focus());
      },

      // Bookmark the current word (or the first one before playback starts).
      bookmark() {
        if (words.length === 0) return;
        this.mark = words[Math.max(current, 0)].dataset.s;
        this.$nextTick(() => this.$refs.bookmark.dispatchEvent(new Event("bookmark")));
        this.flash = "Bookmarked";
        setTimeout(() => { this.flash = ""; }, 1500);
      },

      // x-init of the notes list, on first render and after every change:
      // drop the draft, then redraw the underlines.
      notesChanged() {
        this.draft = null;
        this.$refs.noteText.value = "";
        this.markNotes();
      },

      // Underline (data-note) every word whose start lies in a note of this
      // chapter. Ranges come from the server-rendered list.
      markNotes() {
        for (const w of words) delete w.dataset.note;
        for (const li of document.querySelectorAll(`#notes li[data-ch="${chapter}"]`)) {
          const s = Number(li.dataset.start), e = Number(li.dataset.end);
          let i = indexAt(s);
          while (i > 0 && starts[i - 1] >= s) i--; // equal starts before i
          if (i < 0 || starts[i] < s) i++;
          for (; i < words.length && starts[i] <= e; i++) words[i].dataset.note = "";
        }
      },

      toggle() {
        const audio = this.$refs.audio;
        audio.paused ? audio.play() : audio.pause();
      },

      skip(secs) {
        const audio = this.$refs.audio;
        audio.currentTime = Math.min(audio.duration || Infinity, Math.max(0, audio.currentTime + secs));
        this.resumeFollow();
      },

      scrub(e) {
        this.$refs.audio.currentTime = Number(e.target.value);
      },

      // Slider input, or 1 from the reset button. defaultPlaybackRate keeps
      // the rate if the media reloads. Browsers keep pitch natural by default.
      setSpeed(r) {
        this.speed = clampSpeed(r);
        const audio = this.$refs.audio;
        audio.defaultPlaybackRate = this.speed;
        audio.playbackRate = this.speed;
        storeSpeed(this.speed);
      },

      // Slider input, arrow keys, or 1 from the reset label. Moving the
      // slider also unmutes.
      setVolume(v) {
        this.volume = clampVolume(v);
        applyVolume(this.$refs.audio, this.volume);
        if (this.muted && this.volume > 0) this.toggleMute();
        storeVolume(this.volume);
      },

      toggleMute() {
        this.muted = !this.muted;
        this.$refs.audio.muted = this.muted;
      },

      // Settings dropdown button. Values outside PREFS are ignored. Size and
      // width move the text, so re-centre the spoken word if following.
      setPref(k, v) {
        if (!PREFS[k]?.includes(v)) return;
        this.prefs = { ...this.prefs, [k]: v };
        applyPrefs(this.prefs);
        storePrefs(this.prefs);
        if (this.follow) this.$nextTick(() => this.resumeFollow());
      },

      // m:ss, or h:mm:ss for long chapters.
      clock(t) {
        if (!Number.isFinite(t)) return "0:00";
        const s = Math.floor(t);
        const mm = String(Math.floor(s / 60) % 60);
        const ss = String(s % 60).padStart(2, "0");
        return s >= 3600 ? `${Math.floor(s / 3600)}:${mm.padStart(2, "0")}:${ss}` : `${mm}:${ss}`;
      },

      destroy() {
        cancelAnimationFrame(frame);
      },

      loop() {
        this.tick();
        const audio = this.$refs.audio;
        if (!audio.paused && !audio.ended) {
          frame = requestAnimationFrame(() => this.loop());
        }
        if (performance.now() - lastSave > 10000) this.save();
      },

      tick() {
        this.time = this.$refs.audio.currentTime;
        const i = indexAt(this.time);
        if (i === current) return;
        if (current >= 0) delete words[current].dataset.active;
        current = i;
        if (i < 0) return;
        words[i].dataset.active = "";
        if (this.follow) keepInView(words[i], this.$refs.top, this.$refs.bar);
      },

      resumeFollow() {
        this.follow = true;
        if (current >= 0) keepInView(words[current], this.$refs.top, this.$refs.bar);
      },

      seek(e) {
        const s = e.target.dataset?.s;
        if (s === undefined) return;
        // Ending a text selection is not a click to seek.
        if (!document.getSelection()?.isCollapsed) return;
        this.$refs.audio.currentTime = Number(s);
        this.resumeFollow();
      },

      key(e) {
        if (e.target.closest("input, textarea, button, audio, a")) return;
        if (e.code === "Space") {
          e.preventDefault();
          this.toggle();
        } else if (e.code === "ArrowLeft" || e.code === "ArrowRight") {
          e.preventDefault();
          this.skip(e.code === "ArrowLeft" ? -10 : 10);
        } else if (e.code === "ArrowUp" || e.code === "ArrowDown") {
          e.preventDefault();
          this.setVolume(this.volume + (e.code === "ArrowUp" ? 0.1 : -0.1));
        } else if (e.code === "PageUp" || e.code === "PageDown") {
          this.follow = false;
        }
      },

      // The hidden form's htmx trigger listens for "savepos". $nextTick lets
      // Alpine flush `time` into the hidden input first.
      save() {
        lastSave = performance.now();
        this.$nextTick(() => this.$refs.pos.dispatchEvent(new Event("savepos")));
      },
    };
  });
});

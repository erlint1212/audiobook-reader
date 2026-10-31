// Word-sync for the reader: the one piece of hand-written JS in this repo.
// The server renders each word as <span data-s="start">. This component
// follows audio.currentTime, marks the current word with data-active
// (styled by Tailwind in the template), keeps it in view, and saves progress.
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
    };

    return {
      follow: true,
      time: 0,
      duration: 0,
      playing: false,
      speed: 1,
      chapters: false,

      init() {
        words = Array.from(this.$refs.text.querySelectorAll("span[data-s]"));
        starts = words.map((w) => Number(w.dataset.s));
        const audio = this.$refs.audio;
        const start = Number(this.$el.dataset.start) || 0;
        if (start > 0) {
          audio.addEventListener("loadedmetadata", () => { audio.currentTime = start; }, { once: true });
        }
        this.setSpeed(loadSpeed());
        audio.addEventListener("durationchange", () => { this.duration = audio.duration; });
        audio.addEventListener("play", () => { waiting = false; this.playing = true; this.loop(); });
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

      // Drawer link clicked (htmx does the swap). Keep playing if we were.
      pick() {
        autoplay = this.playing;
      },

      // Page scrolled by hand: stop following. Scrolling the drawer does not count.
      unfollow(e) {
        if (this.$refs.drawer.contains(e.target)) return;
        this.follow = false;
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

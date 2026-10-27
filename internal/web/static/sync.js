// Word-sync for the reader: the one piece of hand-written JS in this repo.
// The server renders each word as <span data-s="start">. This component
// follows audio.currentTime, marks the current word with data-active
// (styled by Tailwind in the template), keeps it in view, and saves progress.
document.addEventListener("alpine:init", () => {
  Alpine.data("reader", () => {
    // Non-reactive state: DOM nodes and big arrays stay out of Alpine proxies.
    let words = [];
    let starts = [];
    let current = -1;
    let frame = 0;
    let lastSave = 0;

    // Last index whose start <= t, or -1 before the first word.
    const indexAt = (t) => {
      let lo = 0, hi = starts.length - 1, found = -1;
      while (lo <= hi) {
        const mid = (lo + hi) >> 1;
        if (starts[mid] <= t) { found = mid; lo = mid + 1; } else { hi = mid - 1; }
      }
      return found;
    };

    const keepInView = (w) => {
      const h = window.innerHeight;
      // While a picture's caption is read, keep the whole picture on screen
      // (below the sticky player) if it fits; else follow the word as usual.
      const fig = w.closest("figure");
      const f = fig?.getBoundingClientRect();
      if (f && f.height < h * 0.8) {
        if (f.top < h * 0.15 || f.bottom > h) fig.scrollIntoView({ block: "center", behavior: "smooth" });
        return;
      }
      const r = w.getBoundingClientRect();
      if (r.top < h * 0.3 || r.bottom > h * 0.7) {
        w.scrollIntoView({ block: "center", behavior: "smooth" });
      }
    };

    return {
      follow: true,
      time: 0,

      init() {
        words = Array.from(this.$refs.text.querySelectorAll("span[data-s]"));
        starts = words.map((w) => Number(w.dataset.s));
        const audio = this.$refs.audio;
        const start = Number(this.$el.dataset.start) || 0;
        if (start > 0) {
          audio.addEventListener("loadedmetadata", () => { audio.currentTime = start; }, { once: true });
        }
        audio.addEventListener("play", () => this.loop());
        audio.addEventListener("pause", () => { this.tick(); this.save(); });
        audio.addEventListener("seeked", () => this.tick());
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
        if (this.follow) keepInView(words[i]);
      },

      resumeFollow() {
        this.follow = true;
        if (current >= 0) keepInView(words[current]);
      },

      seek(e) {
        const s = e.target.dataset?.s;
        if (s === undefined) return;
        this.$refs.audio.currentTime = Number(s);
        this.resumeFollow();
      },

      key(e) {
        if (e.target.closest("input, textarea, button, audio, a")) return;
        const audio = this.$refs.audio;
        if (e.code === "Space") {
          e.preventDefault();
          audio.paused ? audio.play() : audio.pause();
        } else if (e.code === "ArrowLeft" || e.code === "ArrowRight") {
          e.preventDefault();
          audio.currentTime = Math.max(0, audio.currentTime + (e.code === "ArrowLeft" ? -10 : 10));
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

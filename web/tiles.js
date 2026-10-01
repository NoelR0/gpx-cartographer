// Explorer mode: the map starts out covered in parchment; every zoom-14 tile
// a track passes through uncovers itself and its eight neighbours. Computed in the browser
// from the track segments of /api/data, mirroring internal/tiles.
// Tracks without timestamps (e.g. planned routes) are not counted.
// Every tile is credited to the oldest track that uncovered it. Tiles first
// uncovered in the last RECENT_DAYS glow, as do the new tiles of the track
// open in the detail view. Nothing is stored: all of it follows from the
// track dates. The replay shows the tiles uncovered up to a moving date,
// with the tracks whose tiles are glowing drawn as bright beams. Uncovered
// areas get soft, singed edges and fade in when they are new.
window.GPXTiles = (() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const ZOOM = 14;
  const N = 2 ** ZOOM;
  const GRID_MIN_PX = 12; // draw grid lines once a tile is at least this large on screen
  const GRID = "rgba(95, 62, 25, .3)";
  const SOFT_MIN_PX = 6;  // soft edges once a tile is at least this large on screen
  const SOFT_MAX_PX = 14; // blur radius of the edges at most
  const SINGE = "rgba(92, 52, 14, .55)"; // darkened parchment around uncovered areas
  const REVEAL_S = 1.2;   // new tiles fade in this long after loading
  const REPLAY_REVEAL_S = 0.4; // and this long during the replay
  const GLOW = "rgba(255, 190, 40, .7)";
  const RECENT_DAYS = 7;
  const DAY_MS = 86400000;
  // replay speeds in days per second
  const SPEEDS = [[1, "1 day"], [2, "2 days"], [7, "1 week"], [14, "2 weeks"], [30, "1 month"],
    [91, "3 months"], [182, "6 months"], [365, "1 year"]];
  const REPLAY_TARGET_S = 30; // the initial speed plays all tracks in about this long
  const REPLAY_GAP_S = 0.5;   // gaps without tracks take at most this long after the glow
  const REPLAY_GLOW_S = 1;    // tiles glow for this long after being uncovered
  const REPLAY_FRAME_MS = 50; // the map is redrawn at most this often

  let map = null;
  let tracks = [];        // timed tracks, oldest first
  let discovered = null;  // Map of x * N + y -> index of the first track that uncovered it, computed lazily
  let newCounts = null;   // track id -> number of tiles it uncovered first
  let layer = null;       // canvas fog layer
  let glowLayer = null;   // canvas layer with the highlighted tiles
  let glow = null;        // Set of highlighted tiles, computed lazily
  let focusId = null;     // track open in the detail view
  let replay = null;      // running replay, see startReplay
  const beams = new Map(); // track index -> beam layer of the replay
  let onReplay = () => {};
  let enabled = false;
  let coverageTimer = 0;
  let coverageSeq = 0;    // ignores responses to outdated requests
  let starts = [];        // start time of every track in ms
  let reveal = null;      // fade-in of the recent tiles after loading, {from, t0, raf}
  let fogPattern = null;
  const reducedMotion = window.matchMedia?.("(prefers-reduced-motion: reduce)");

  const store = {
    get(k) { try { return localStorage.getItem(k); } catch { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch { /* ignore */ } },
  };

  // ---------- Tile math (Web Mercator) ----------
  const toTileX = (lon) => ((lon + 180) / 360) * N;
  const toTileY = (lat) => {
    const r = (Math.max(-85.0511, Math.min(85.0511, lat)) * Math.PI) / 180;
    return ((1 - Math.log(Math.tan(r) + 1 / Math.cos(r)) / Math.PI) / 2) * N;
  };
  const key = (x, y) => x * N + y;

  // ---------- Computation ----------
  function compute() {
    let visited = new Set();
    const add = (x, y) => { if (x >= 0 && x < N && y >= 0 && y < N) visited.add(key(x, y)); };

    // Walks every tile the straight line between two points crosses, so that
    // long simplified segments do not skip tiles (Amanatides & Woo).
    const line = (ax, ay, bx, by) => {
      let x = Math.floor(ax), y = Math.floor(ay);
      const ex = Math.floor(bx), ey = Math.floor(by);
      add(x, y);
      const dx = bx - ax, dy = by - ay;
      const sx = Math.sign(dx), sy = Math.sign(dy);
      const tdx = dx ? Math.abs(1 / dx) : Infinity, tdy = dy ? Math.abs(1 / dy) : Infinity;
      let tx = dx ? (sx > 0 ? x + 1 - ax : ax - x) * tdx : Infinity;
      let ty = dy ? (sy > 0 ? y + 1 - ay : ay - y) * tdy : Infinity;
      for (let steps = Math.abs(ex - x) + Math.abs(ey - y); steps > 0; steps--) {
        if (tx < ty) { x += sx; tx += tdx; } else { y += sy; ty += tdy; }
        add(x, y);
      }
    };

    // tracks are sorted by start, so the first track to reach a tile keeps it
    const out = new Map();
    newCounts = new Map();
    tracks.forEach((t, i) => {
      visited = new Set();
      for (const seg of t.segments) {
        let px = null, py = null;
        for (const [lat, lon] of seg) {
          const x = toTileX(lon), y = toTileY(lat);
          if (px == null) add(Math.floor(x), Math.floor(y));
          else line(px, py, x, y);
          px = x; py = y;
        }
      }
      // every visited tile uncovers its eight neighbours as well; x wraps
      // around the antimeridian
      let fresh = 0;
      for (const k of visited) {
        const x = Math.floor(k / N), y = k % N;
        for (let dy = -1; dy <= 1; dy++) {
          const ny = y + dy;
          if (ny < 0 || ny >= N) continue;
          for (let dx = -1; dx <= 1; dx++) {
            const nk = key((x + dx + N) % N, ny);
            if (!out.has(nk)) { out.set(nk, i); fresh++; }
          }
        }
      }
      newCounts.set(t.id, fresh);
    });
    return out;
  }

  function current() {
    if (!discovered) discovered = compute();
    return discovered;
  }

  // index of the first track within the last RECENT_DAYS
  function recentFrom() {
    const since = Date.now() - RECENT_DAYS * 86400000;
    const i = tracks.findIndex((t) => Date.parse(t.start) >= since);
    return i < 0 ? tracks.length : i;
  }

  // the tiles just uncovered in the replay, the new tiles of the focused
  // track, or else the recently uncovered tiles
  function highlighted() {
    if (glow) return glow;
    const tiles = current();
    if (replay) {
      glow = new Set();
      for (const [k, i] of tiles) if (i >= replay.glowFrom && i <= replay.cutoff) glow.add(k);
      return glow;
    }
    const focus = focusId == null ? -1 : tracks.findIndex((t) => t.id === focusId);
    const from = recentFrom();
    const hit = focus >= 0 ? (i) => i === focus : (i) => i >= from;
    glow = new Set();
    if (focus >= 0 || from < tracks.length) {
      for (const [k, i] of tiles) if (hit(i)) glow.add(k);
    }
    return glow;
  }

  // number of recently uncovered tiles
  function recentCount() {
    const from = recentFrom();
    let n = 0;
    for (let i = from; i < tracks.length; i++) n += newCounts.get(tracks[i].id);
    return n;
  }

  // ---------- Drawing ----------
  // calls fn(x, y, key) for every tile of the set (Set or Map keys) within
  // x0..x1, y0..y1, scanning the area or the set, whichever is smaller
  function eachIn(tiles, x0, y0, x1, y1, fn) {
    if ((x1 - x0 + 1) * (y1 - y0 + 1) > tiles.size) {
      for (const k of tiles.keys()) {
        const x = Math.floor(k / N), y = k % N;
        if (x >= x0 && x <= x1 && y >= y0 && y <= y1) fn(x, y, k);
      }
    } else {
      for (let x = x0; x <= x1; x++) {
        for (let y = y0; y <= y1; y++) {
          const k = key(x, y);
          if (tiles.has(k)) fn(x, y, k);
        }
      }
    }
  }

  // the explorer tiles covered by a map tile and their pixel rectangles
  function tileGeometry(size, coords) {
    const scale = 2 ** (ZOOM - coords.z);       // explorer tiles per map tile (per side)
    const cell = size / scale;                  // explorer tile size in px
    const wrap = 2 ** coords.z;                 // the map repeats horizontally
    const ox = (((coords.x % wrap) + wrap) % wrap) * scale, oy = coords.y * scale;
    const rect = (x, y) => {
      // keep single tiles visible as one pixel when zoomed out
      if (cell < 1) return [Math.floor((x - ox) * cell), Math.floor((y - oy) * cell), 1, 1];
      // rounded edges, so that neighbouring tiles leave no seams
      const px = Math.round((x - ox) * cell), py = Math.round((y - oy) * cell);
      return [px, py, Math.round((x + 1 - ox) * cell) - px, Math.round((y + 1 - oy) * cell) - py];
    };
    return {
      cell, ox, oy, rect,
      x0: Math.floor(ox), y0: Math.floor(oy),
      x1: Math.ceil(ox + scale) - 1, y1: Math.ceil(oy + scale) - 1,
    };
  }

  // A tileable parchment texture of two map tiles per side: soft blotches
  // from a few octaves of value noise plus fine grain.
  function makeFogPattern(ctx, size) {
    const S = size * 2;
    const c = document.createElement("canvas");
    c.width = c.height = S;
    const g = c.getContext("2d");
    const img = g.createImageData(S, S);
    // deterministic pseudo random numbers, so every load looks the same
    let seed = 7;
    const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
    const octaves = [4, 8, 16, 48].map((n, i) => ({ n, amp: [0.45, 0.3, 0.17, 0.08][i], v: Array.from({ length: n * n }, rnd) }));
    const smooth = (t) => t * t * (3 - 2 * t);
    for (let py = 0; py < S; py++) {
      for (let px = 0; px < S; px++) {
        let v = 0;
        for (const o of octaves) {
          const fx = (px / S) * o.n, fy = (py / S) * o.n;
          const ix = Math.floor(fx), iy = Math.floor(fy);
          const tx = smooth(fx - ix), ty = smooth(fy - iy);
          const at = (x, y) => o.v[(y % o.n) * o.n + (x % o.n)]; // wraps: the texture tiles
          const a = at(ix, iy) + (at(ix + 1, iy) - at(ix, iy)) * tx;
          const b = at(ix, iy + 1) + (at(ix + 1, iy + 1) - at(ix, iy + 1)) * tx;
          v += (a + (b - a) * ty) * o.amp;
        }
        const shade = (v - 0.5) * 46 + (rnd() - 0.5) * 10;
        const i = (py * S + px) * 4;
        img.data[i] = 206 + shade;
        img.data[i + 1] = 182 + shade * 0.95;
        img.data[i + 2] = 136 + shade * 0.8;
        img.data[i + 3] = 255;
      }
    }
    g.putImageData(img, 0, 0);
    return ctx.createPattern(c, "repeat");
  }

  // how far the tiles of track i are uncovered, 0..1
  function revealed(i) {
    if (replay) {
      if (i > replay.cutoff) return 0;
      if (replay.t >= replay.end) return 1; // the end shows the final state
      const p = (replay.t - starts[i]) / (REPLAY_REVEAL_S * daysPerSecond() * DAY_MS);
      return p >= 1 || reducedMotion?.matches ? 1 : Math.max(0, p);
    }
    if (reveal && i >= reveal.from) return reveal.p;
    return 1;
  }

  // fills a path with blurred edges: the shape itself is drawn far off the
  // canvas and only its shadow lands on it (works in every browser, unlike
  // ctx.filter). One fill per path, so overlapping tiles leave no seams.
  function softFill(ctx, path, color, blur, size) {
    if (!blur) {
      ctx.fillStyle = color;
      ctx.fill(path);
      return;
    }
    const off = size * 3;
    ctx.save();
    ctx.translate(-off, 0);
    ctx.shadowOffsetX = off;
    ctx.shadowBlur = blur;
    ctx.shadowColor = color;
    ctx.fillStyle = "#000";
    ctx.fill(path);
    ctx.restore();
  }

  // the tiles of a Set or Map around a map tile as paths, grouped by how far
  // they are uncovered (progress gets the track index of a Map entry); tiles a little beyond the edges are included, so that the
  // blur matches across neighbouring map tiles
  function uncoveredPaths(tiles, g, margin, progress) {
    const paths = new Map(); // progress (in steps of 0.1) -> Path2D
    eachIn(tiles, g.x0 - margin, g.y0 - margin, g.x1 + margin, g.y1 + margin, (x, y, k) => {
      const p = Math.round(progress(tiles instanceof Map ? tiles.get(k) : -1) * 10) / 10;
      if (p <= 0) return;
      if (!paths.has(p)) paths.set(p, new Path2D());
      paths.get(p).rect(...g.rect(x, y));
    });
    return paths;
  }

  const blurOf = (cell) => (cell >= SOFT_MIN_PX ? Math.min(cell * 0.35, SOFT_MAX_PX) : 0);

  function drawTile(canvas, coords) {
    const tiles = current();
    const size = canvas.width;
    const ctx = canvas.getContext("2d");
    const g = tileGeometry(size, coords);
    const blur = blurOf(g.cell);

    // parchment, aligned with the neighbouring tiles
    if (!fogPattern) fogPattern = makeFogPattern(ctx, size);
    ctx.globalCompositeOperation = "source-over";
    ctx.globalAlpha = 1;
    ctx.save();
    ctx.translate(-(coords.x % 2) * size, -(coords.y % 2) * size);
    ctx.fillStyle = fogPattern;
    ctx.fillRect((coords.x % 2) * size, (coords.y % 2) * size, size, size);
    ctx.restore();

    const paths = uncoveredPaths(tiles, g, blur ? Math.ceil((blur * 2) / g.cell) + 1 : 0, revealed);
    if (blur) {
      // singe the parchment around the openings
      ctx.globalCompositeOperation = "source-atop";
      for (const [p, path] of paths) {
        ctx.globalAlpha = p;
        softFill(ctx, path, SINGE, blur * 2.5, size);
      }
    }
    // cut the openings
    ctx.globalCompositeOperation = "destination-out";
    for (const [p, path] of paths) {
      ctx.globalAlpha = p;
      softFill(ctx, path, "#000", blur, size);
    }
    ctx.globalCompositeOperation = "source-over";
    ctx.globalAlpha = 1;

    const { cell, ox, oy, x0, y0, x1, y1 } = g;
    if (cell >= GRID_MIN_PX) {
      ctx.strokeStyle = GRID;
      ctx.lineWidth = 1;
      ctx.beginPath();
      for (let x = x0; x <= x1 + 1; x++) {
        const px = Math.round((x - ox) * cell) + 0.5;
        ctx.moveTo(px, 0); ctx.lineTo(px, size);
      }
      for (let y = y0; y <= y1 + 1; y++) {
        const py = Math.round((y - oy) * cell) + 0.5;
        ctx.moveTo(0, py); ctx.lineTo(size, py);
      }
      ctx.stroke();
    }
  }

  function drawGlowTile(canvas, coords) {
    const ctx = canvas.getContext("2d");
    const g = tileGeometry(canvas.width, coords);
    const blur = blurOf(g.cell);
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    const paths = uncoveredPaths(highlighted(), g, blur ? Math.ceil((blur * 2) / g.cell) + 1 : 0, () => 1);
    for (const path of paths.values()) softFill(ctx, path, GLOW, blur, canvas.width);
  }

  // fades in the recently uncovered tiles
  function startReveal() {
    if (reveal) cancelAnimationFrame(reveal.raf);
    reveal = null;
    const from = recentFrom();
    if (!layer || from >= tracks.length || reducedMotion?.matches) return;
    reveal = { from, t0: performance.now(), p: 0, raf: 0 };
    const step = (now) => {
      // a short pause first, so the eye finds the spot
      const t = Math.min(1, Math.max(0, (now - reveal.t0) / 1000 - 0.2) / REVEAL_S);
      reveal.p = 1 - (1 - t) ** 3;
      if (layer) layer.repaint();
      if (t < 1) reveal.raf = requestAnimationFrame(step);
      else reveal = null;
    };
    layer.repaint();
    reveal.raf = requestAnimationFrame(step);
  }

  function makeLayer(pane, draw) {
    const Layer = L.GridLayer.extend({
      createTile(coords) {
        const canvas = document.createElement("canvas");
        const size = this.getTileSize();
        canvas.width = size.x;
        canvas.height = size.y;
        canvas.coords = coords;
        draw(canvas, coords);
        this.canvases.add(canvas);
        return canvas;
      },
    });
    // Drawing a tile is cheap and synchronous, so update while panning (Leaflet
    // waits until the pan ends on mobile by default) and keep extra tiles
    // around; otherwise the uncovered map flashes at the edges.
    const l = new Layer({ pane, updateWhenZooming: false, updateWhenIdle: false, updateInterval: 50, keepBuffer: 4,
      noWrap: true, // one copy of the world, like the map tiles (see app.js)
    });
    // the canvases on the map, so they can be repainted in place (redraw()
    // would replace them and let the map flash through)
    l.canvases = new Set();
    l.on("tileunload", (e) => l.canvases.delete(e.tile));
    l.on("remove", () => l.canvases.clear());
    l.repaint = () => { for (const c of l.canvases) draw(c, c.coords); };
    return l;
  }

  // shows the highlighted tiles, if any, and lets them flash once
  function updateGlow() {
    const show = enabled && (replay != null || highlighted().size > 0);
    if (show && !glowLayer) glowLayer = makeLayer("explorerGlow", drawGlowTile).addTo(map);
    else if (show) glowLayer.repaint();
    else if (glowLayer) { glowLayer.remove(); glowLayer = null; }
    const pane = map.getPane("explorerGlow");
    pane.classList.remove("flash");
    pane.classList.toggle("replaying", replay != null);
    if (show && !replay) {
      void pane.offsetWidth; // restart the animation
      pane.classList.add("flash");
    }
  }

  // ---------- Discovered share of the regions at the map center ----------
  const LEVEL_LABELS = { world: "World (land)", continent: "Continent", country: "Country", state: "State / province" };

  const fmtPct = (visited, total) => {
    const p = total ? Math.min(100, (visited / total) * 100) : 0;
    if (p === 0) return "0 %";
    if (p >= 1) return p.toLocaleString(undefined, { maximumFractionDigits: p >= 10 ? 1 : 2 }) + " %";
    return p.toLocaleString(undefined, { maximumSignificantDigits: 2 }) + " %";
  };
  const fmtKm2 = (v) => v.toLocaleString(undefined, { maximumFractionDigits: v < 10 ? 1 : 0 }) + " km²";

  function scheduleCoverage(delay = 300) {
    clearTimeout(coverageTimer);
    if (enabled) coverageTimer = setTimeout(loadCoverage, delay);
  }

  async function loadCoverage() {
    const seq = ++coverageSeq;
    const c = map.getCenter().wrap();
    const url = `api/coverage?lat=${c.lat.toFixed(5)}&lon=${c.lng.toFixed(5)}`;
    const el = $("tiles-coverage");
    try {
      const res = await fetch(url, { cache: "no-store" });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      if (seq !== coverageSeq || !enabled) return;
      el.innerHTML = '<h3>At the map center</h3><ul>' +
        ["state", "country", "continent", "world"].filter((k) => data[k]).map((k) => {
          const a = data[k];
          const width = a.area_km2 ? Math.min(100, (a.visited_km2 / a.area_km2) * 100) : 0;
          return `<li><div class="cov-head"><span class="cov-name"><small>${LEVEL_LABELS[k]}</small>${esc(a.name)}</span>` +
            `<b>${fmtPct(a.visited_km2, a.area_km2)}</b></div>` +
            `<div class="cov-bar"><span style="width:${width ? `max(2px, ${width}%)` : 0}"></span></div>` +
            `<div class="cov-sub">${fmtKm2(a.visited_km2)} of ${fmtKm2(a.area_km2)}</div></li>`;
        }).join("") + "</ul>";
    } catch (err) {
      if (seq !== coverageSeq) return;
      el.innerHTML = `<span class="warn">Could not load region statistics: ${esc(err.message)}</span>`;
    }
  }

  // ---------- Replay ----------
  // Plays back the discoveries: a date runs from the first to the last track
  // and the fog shows only what the tracks up to that date uncovered.
  const startOf = (i) => starts[i];
  const fmtReplayDate = (t) => new Date(t).toLocaleDateString(undefined, { dateStyle: "medium" });

  function startReplay() {
    if (!tracks.length) return;
    current();
    const first = startOf(0), end = startOf(tracks.length - 1);
    let speed = SPEEDS.findIndex(([d]) => (end - first) / (d * DAY_MS) <= REPLAY_TARGET_S);
    if (speed < 0) speed = SPEEDS.length - 1;
    replay = { t: first, end, speed, playing: false, cutoff: -1, glowFrom: 0, count: 0, drawn: "", lastDraw: 0, last: 0, raf: 0 };
    setOpen(false);
    document.body.classList.add("replaying");
    $("replay").hidden = false;
    onReplay(true);
    glow = null;
    updateGlow();
    advance(first);
    draw(true);
    play();
  }

  function stopReplay() {
    if (!replay) return;
    cancelAnimationFrame(replay.raf);
    replay = null;
    updateBeam();
    document.body.classList.remove("replaying");
    $("replay").hidden = true;
    glow = null;
    onReplay(false);
    if (layer) layer.repaint();
    refresh();
  }

  function play() {
    const r = replay;
    if (r.t >= r.end) {
      // from the start again
      r.cutoff = -1; r.count = 0;
      advance(startOf(0));
    }
    r.playing = true;
    r.last = performance.now();
    r.raf = requestAnimationFrame(frame);
    updateBar();
  }

  function pause() {
    replay.playing = false;
    cancelAnimationFrame(replay.raf);
    draw(true);
    updateBar();
  }

  const daysPerSecond = () => SPEEDS[replay.speed][0];

  function frame(now) {
    const r = replay;
    if (!r || !r.playing) return;
    const ms = daysPerSecond() * DAY_MS; // replay time per second
    let t = r.t + (Math.min(now - r.last, 100) / 1000) * ms;
    r.last = now;
    // skip long stretches without tracks, once the latest track stopped glowing
    const next = r.cutoff + 1 < tracks.length ? startOf(r.cutoff + 1) : r.end;
    const glowEnd = r.cutoff >= 0 ? startOf(r.cutoff) + REPLAY_GLOW_S * ms : -Infinity;
    if (t >= glowEnd && next - t > REPLAY_GAP_S * ms) t = next - REPLAY_GAP_S * ms;
    if (t >= r.end) {
      t = r.end;
      r.playing = false;
    }
    advance(t);
    draw(!r.playing);
    if (r.playing) r.raf = requestAnimationFrame(frame);
    else updateBar();
  }

  // moves the replay date to t
  function advance(t) {
    const r = replay;
    r.t = t;
    while (r.cutoff + 1 < tracks.length && startOf(r.cutoff + 1) <= t) {
      r.cutoff++;
      r.count += newCounts.get(tracks[r.cutoff].id);
    }
    const since = t - REPLAY_GLOW_S * daysPerSecond() * DAY_MS;
    let from = r.cutoff + 1;
    while (from > 0 && startOf(from - 1) >= since) from--;
    r.glowFrom = from;
    $("replay-date").textContent = fmtReplayDate(t);
    $("replay-tiles").textContent = `${r.count.toLocaleString()} tiles`;
  }

  // repaints the map if the uncovered tiles changed; throttled unless forced
  function draw(force) {
    const r = replay;
    const now = performance.now();
    // keep repainting while tiles are fading in
    let fading = false;
    for (let i = r.glowFrom; i <= r.cutoff && !fading; i++) fading = revealed(i) < 1;
    const state = fading ? now : `${r.cutoff}:${r.glowFrom}`;
    if (state === r.drawn || (!force && now - r.lastDraw < REPLAY_FRAME_MS)) return;
    r.drawn = state;
    r.lastDraw = now;
    glow = null;
    if (layer) layer.repaint();
    if (glowLayer) glowLayer.repaint();
    updateBeam();
    $("explorer-count").textContent = r.count.toLocaleString();
  }

  // draws the tracks whose tiles are glowing as glowing lines: a wide soft
  // halo with a bright core
  function updateBeam() {
    const from = replay ? replay.glowFrom : Infinity, to = replay ? replay.cutoff : -1;
    for (const [i, l] of beams) {
      if (i < from || i > to) { l.remove(); beams.delete(i); }
    }
    const opts = { pane: "replayBeam", interactive: false, lineCap: "round", lineJoin: "round" };
    for (let i = from; i <= to; i++) {
      if (beams.has(i)) continue;
      const group = L.layerGroup();
      for (const seg of tracks[i].segments) {
        group.addLayer(L.polyline(seg, { ...opts, color: "#ffb31a", weight: 12, opacity: 0.45, className: "replay-beam" }));
        group.addLayer(L.polyline(seg, { ...opts, color: "#fffbe6", weight: 3, opacity: 1, className: "replay-beam" }));
      }
      beams.set(i, group.addTo(map));
    }
  }

  function updateBar() {
    const playing = replay.playing;
    const btn = $("replay-play");
    btn.textContent = playing ? "\u23F8\uFE0E" : "\u25B6\uFE0E";
    btn.setAttribute("aria-label", playing ? "Pause" : "Play");
    btn.title = playing ? "Pause (Space)" : "Play (Space)";
    $("replay-speed").textContent = `${SPEEDS[replay.speed][1]}/s`;
    $("replay-slower").disabled = replay.speed === 0;
    $("replay-faster").disabled = replay.speed === SPEEDS.length - 1;
  }

  function setSpeed(delta) {
    replay.speed = Math.max(0, Math.min(SPEEDS.length - 1, replay.speed + delta));
    updateBar();
  }

  // ---------- Panel ----------
  function refresh() {
    if (enabled) {
      if (!layer) layer = makeLayer("explorer", drawTile).addTo(map);
      else layer.repaint();
      $("explorer-count").textContent = current().size.toLocaleString();
      const recent = recentCount();
      $("explorer-recent").hidden = !recent;
      $("explorer-recent").textContent = `+${recent.toLocaleString()} in the last ${RECENT_DAYS} days`;
      $("replay-start").hidden = !tracks.length;
    } else if (layer) {
      layer.remove();
      layer = null;
    }
    updateGlow();
    if (enabled) startReveal();
    $("explorer-fab").classList.toggle("active", enabled);
    $("explorer-fab").setAttribute("aria-label", enabled ? "Explorer (on)" : "Explorer (off)");
    $("explorer-badge").hidden = !enabled || !current().size;
    if (enabled) $("explorer-badge").textContent = fmtCompact.format(current().size);
    $("explorer-body").hidden = !enabled;
    $("explorer-collapse").hidden = !enabled;
    scheduleCoverage(0);
  }

  const fmtCompact = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 });

  // mobile only: the panel is a popover opened from the map button
  function setOpen(open) {
    $("explorer").classList.toggle("open", open);
    $("explorer-fab").setAttribute("aria-expanded", String(open));
  }

  function setCollapsed(collapsed) {
    $("explorer").classList.toggle("collapsed", collapsed);
    $("explorer-collapse").setAttribute("aria-expanded", String(!collapsed));
  }

  function init(opts) {
    map = opts.map;
    const pane = map.createPane("explorer");
    pane.style.zIndex = 350; // above the base map, below tracks and photos
    pane.style.pointerEvents = "none";
    const glowPane = map.createPane("explorerGlow");
    glowPane.style.zIndex = 351;
    glowPane.style.pointerEvents = "none";
    const beamPane = map.createPane("replayBeam");
    beamPane.style.zIndex = 352;
    beamPane.style.pointerEvents = "none";

    // the server decides the initial mode on every page load
    enabled = opts.enabled;
    $("explorer-on").checked = enabled;
    $("explorer-on").addEventListener("change", (e) => {
      enabled = e.target.checked;
      if (!enabled) stopReplay();
      refresh();
    });

    onReplay = opts.onReplay || onReplay;
    $("replay-start").addEventListener("click", startReplay);
    $("replay-play").addEventListener("click", () => (replay.playing ? pause() : play()));
    $("replay-stop").addEventListener("click", stopReplay);
    $("replay-slower").addEventListener("click", () => setSpeed(-1));
    $("replay-faster").addEventListener("click", () => setSpeed(1));
    document.addEventListener("keydown", (e) => {
      if (!replay || !$("lightbox").hidden || e.target.closest?.("input, textarea, select")) return;
      if (e.key === "Escape") stopReplay();
      else if (e.key === " " && !e.target.closest?.("button")) {
        e.preventDefault();
        replay.playing ? pause() : play();
      }
    });
    // the replay belongs to the map view
    window.addEventListener("hashchange", () => {
      if (!["", "#map"].includes(location.hash)) stopReplay();
    });

    const stored = store.get("explorer-collapsed");
    setCollapsed(stored === "1");
    $("explorer-collapse").addEventListener("click", () => {
      const collapsed = !$("explorer").classList.contains("collapsed");
      setCollapsed(collapsed);
      store.set("explorer-collapsed", collapsed ? "1" : "0");
    });

    $("explorer-fab").addEventListener("click", () => setOpen(!$("explorer").classList.contains("open")));
    map.on("click", () => setOpen(false));

    map.on("moveend", () => scheduleCoverage());
    refresh();
  }

  function setData(data) {
    stopReplay();
    tracks = data.tracks.filter((t) => t.start)
      .map((t, i) => ({ t, i, start: Date.parse(t.start) }))
      .sort((a, b) => a.start - b.start || a.i - b.i)
      .map((e) => e.t);
    starts = tracks.map((t) => Date.parse(t.start));
    discovered = null;
    newCounts = null;
    glow = null;
    refresh();
  }

  // highlights the new tiles of a track instead of the recent ones (null: back
  // to the recent ones)
  function focus(id) {
    if (id != null) stopReplay();
    if (id === focusId) return;
    focusId = id;
    glow = null;
    updateGlow();
  }

  // number of discovered tiles, also used by the statistics view
  const count = () => current().size;

  // number of tiles a track uncovered first; null for tracks that do not count
  function newTiles(id) {
    current();
    return newCounts.has(id) ? newCounts.get(id) : null;
  }

  // the track that uncovered the most tiles first, as {id, count}
  function mostNewTiles() {
    current();
    let best = null;
    for (const [id, n] of newCounts) if (n > 0 && (!best || n > best.count)) best = { id, count: n };
    return best;
  }

  return { init, setData, count, focus, newTiles, mostNewTiles };
})();

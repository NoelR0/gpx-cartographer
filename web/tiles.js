// Explorer mode: the map starts out black; every zoom-14 tile a track passes
// through uncovers itself and its eight neighbours. Computed in the browser
// from the track segments of /api/data, mirroring internal/tiles.
// Tracks without timestamps (e.g. planned routes) are not counted.
window.GPXTiles = (() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const ZOOM = 14;
  const N = 2 ** ZOOM;
  const FOG = "#000";
  const GRID_MIN_PX = 12; // draw grid lines once a tile is at least this large on screen

  let map = null;
  let tracks = [];
  let discovered = null;  // Set of x * N + y, computed lazily
  let layer = null;       // canvas fog layer
  let enabled = false;
  let coverageTimer = 0;
  let coverageSeq = 0;    // ignores responses to outdated requests

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
    const visited = new Set();
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

    for (const t of tracks) {
      for (const seg of t.segments) {
        let px = null, py = null;
        for (const [lat, lon] of seg) {
          const x = toTileX(lon), y = toTileY(lat);
          if (px == null) add(Math.floor(x), Math.floor(y));
          else line(px, py, x, y);
          px = x; py = y;
        }
      }
    }

    // every visited tile uncovers its eight neighbours as well; x wraps
    // around the antimeridian
    const out = new Set();
    for (const k of visited) {
      const x = Math.floor(k / N), y = k % N;
      for (let dy = -1; dy <= 1; dy++) {
        const ny = y + dy;
        if (ny < 0 || ny >= N) continue;
        for (let dx = -1; dx <= 1; dx++) out.add(key((x + dx + N) % N, ny));
      }
    }
    return out;
  }

  function current() {
    if (!discovered) discovered = compute();
    return discovered;
  }

  // ---------- Drawing ----------
  function drawTile(canvas, coords) {
    const tiles = current();
    const size = canvas.width;
    const ctx = canvas.getContext("2d");
    ctx.fillStyle = FOG;
    ctx.fillRect(0, 0, size, size);

    const scale = 2 ** (ZOOM - coords.z);       // explorer tiles per map tile (per side)
    const cell = size / scale;                  // explorer tile size in px
    const wrap = 2 ** coords.z;                 // the map repeats horizontally
    const ox = (((coords.x % wrap) + wrap) % wrap) * scale, oy = coords.y * scale;
    const x0 = Math.floor(ox), y0 = Math.floor(oy);
    const x1 = Math.ceil(ox + scale) - 1, y1 = Math.ceil(oy + scale) - 1;
    const clear = (x, y) => {
      if (cell < 1) {
        // keep discovered spots visible as single pixels when zoomed out
        ctx.clearRect(Math.floor((x - ox) * cell), Math.floor((y - oy) * cell), 1, 1);
        return;
      }
      // rounded edges, so that neighbouring tiles leave no seams
      const px = Math.round((x - ox) * cell), py = Math.round((y - oy) * cell);
      ctx.clearRect(px, py, Math.round((x + 1 - ox) * cell) - px, Math.round((y + 1 - oy) * cell) - py);
    };

    // either scan the tiles in view or all discovered tiles, whichever is fewer
    if ((x1 - x0 + 1) * (y1 - y0 + 1) > tiles.size) {
      for (const k of tiles) {
        const x = Math.floor(k / N), y = k % N;
        if (x >= x0 && x <= x1 && y >= y0 && y <= y1) clear(x, y);
      }
    } else {
      for (let x = x0; x <= x1; x++) {
        for (let y = y0; y <= y1; y++) {
          if (tiles.has(key(x, y))) clear(x, y);
        }
      }
    }

    if (cell >= GRID_MIN_PX) {
      ctx.strokeStyle = "rgba(60, 60, 60, .35)";
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

  function makeLayer() {
    const Layer = L.GridLayer.extend({
      createTile(coords) {
        const canvas = document.createElement("canvas");
        const size = this.getTileSize();
        canvas.width = size.x;
        canvas.height = size.y;
        drawTile(canvas, coords);
        return canvas;
      },
    });
    return new Layer({ pane: "explorer", updateWhenZooming: false });
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

  // ---------- Panel ----------
  function refresh() {
    if (enabled) {
      if (!layer) layer = makeLayer().addTo(map);
      else layer.redraw();
      $("explorer-count").textContent = current().size.toLocaleString();
    } else if (layer) {
      layer.remove();
      layer = null;
    }
    $("explorer-body").hidden = !enabled;
    $("explorer-collapse").hidden = !enabled;
    scheduleCoverage(0);
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

    // the server decides the initial mode on every page load
    enabled = opts.enabled;
    $("explorer-on").checked = enabled;
    $("explorer-on").addEventListener("change", (e) => {
      enabled = e.target.checked;
      refresh();
    });

    const stored = store.get("explorer-collapsed");
    setCollapsed(stored ? stored === "1" : window.matchMedia("(max-width: 640px)").matches);
    $("explorer-collapse").addEventListener("click", () => {
      const collapsed = !$("explorer").classList.contains("collapsed");
      setCollapsed(collapsed);
      store.set("explorer-collapsed", collapsed ? "1" : "0");
    });

    map.on("moveend", () => scheduleCoverage());
    refresh();
  }

  function setData(data) {
    tracks = data.tracks.filter((t) => t.start);
    discovered = null;
    refresh();
  }

  // number of discovered tiles, also used by the statistics view
  const count = () => current().size;

  return { init, setData, count };
})();

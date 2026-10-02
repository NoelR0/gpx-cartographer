// Relief of the terrain: shaded from Terrarium-encoded elevation tiles (by
// default Mapterhorn's), which the browser loads directly from the tile
// server, like the base map. It lies on the base map as a layer of its own
// and on the parchment of the explorer fog, where the sea (exactly 0 m) gets
// a wash of its own, so that coastlines show. Finer zoom levels exist only
// where there is detailed elevation data; elsewhere the next coarser tile is
// used. Nothing is stored on the server; the browser keeps the shaded relief
// of the most recently used tiles in memory.
window.GPXTerrain = (() => {
  "use strict";

  const BASE_ZOOM = 12;   // available worldwide; the fog loads no finer tiles
  const MAX_CACHE = 64;   // shaded source tiles kept in memory (about 1 MB each, twice that at the coast)
  // Tiles used this recently are kept even beyond MAX_CACHE: if the map tiles
  // on screen need more of them, evicting one would only load it again on
  // the next repaint, evicting the next one, and so on without end.
  const KEEP_MS = 10000;
  const MAX_MISSING = 4096; // remembered source tiles that do not exist
  const EXAGGERATION = 0.35; // extra vertical exaggeration per zoom level below BASE_ZOOM
  const ALT = Math.PI / 4;   // the light comes from the north-west, 45° above the horizon
  const AZ = (7 * Math.PI) / 4;
  const LX = Math.cos(ALT) * Math.sin(AZ), LY = -Math.cos(ALT) * Math.cos(AZ), LZ = Math.sin(ALT); // x east, y south
  const DARK = [70, 42, 14], DARK_MAX = 0.75, DARK_GAIN = 1.6;      // slopes facing away from the light
  const LIGHT = [255, 248, 228], LIGHT_MAX = 0.45, LIGHT_GAIN = 1.4; // slopes facing it
  const SEA = [150, 158, 140], SEA_ALPHA = 0.45;
  const MAP_ALPHA = 0.5; // strength of the relief on the base map

  let url = "";
  let maxZoom = 18;
  let attribution = "";
  // "z/x/y" of a source tile -> {relief, sea (null without sea), used (time of
  // the last use), waiting: Map of map tile canvas -> redraw}; a Map keeps the
  // insertion order, so the first entry is the least recently used
  const cache = new Map();
  const missing = new Set(); // "z/x/y" of the source tiles that could not be loaded

  function configure(opts) {
    url = opts?.url || "";
    maxZoom = opts?.maxZoom ?? maxZoom;
    attribution = opts?.attribution || "";
  }

  // Decodes the elevations and shades them into transparent overlays: the
  // relief, and the sea for the parchment. y and z give the latitude, and
  // with it the size of a pixel.
  function shade(img, z, y) {
    const S = img.width;
    const canvas = () => {
      const c = document.createElement("canvas");
      c.width = c.height = S;
      return c;
    };
    const relief = canvas();
    const g = relief.getContext("2d", { willReadFrequently: true });
    g.drawImage(img, 0, 0);
    const data = g.getImageData(0, 0, S, S);
    const px = data.data;
    const e = new Float32Array(S * S);
    for (let i = 0; i < e.length; i++) e[i] = px[i * 4] * 256 + px[i * 4 + 1] + px[i * 4 + 2] / 256 - 32768;

    const lat = Math.atan(Math.sinh(Math.PI * (1 - (2 * (y + 0.5)) / 2 ** z)));
    const m = (40075016.686 * Math.cos(lat)) / (2 ** z * S); // metres per pixel
    const ex = 1 + EXAGGERATION * Math.max(0, BASE_ZOOM - z);
    let seaData = null;
    const set = (d, i, [r, gr, b], a) => { d[i] = r; d[i + 1] = gr; d[i + 2] = b; d[i + 3] = a * 255; };
    for (let py = 0; py < S; py++) {
      // one-sided differences at the edges, so that the tiles join without seams
      const y0 = Math.max(0, py - 1), y1 = Math.min(S - 1, py + 1);
      for (let qx = 0; qx < S; qx++) {
        const k = py * S + qx;
        if (e[k] === 0) {
          seaData ??= g.createImageData(S, S);
          set(seaData.data, k * 4, SEA, SEA_ALPHA);
          px[k * 4 + 3] = 0;
          continue;
        }
        const x0 = Math.max(0, qx - 1), x1 = Math.min(S - 1, qx + 1);
        const dx = ((e[py * S + x1] - e[py * S + x0]) / ((x1 - x0) * m)) * ex;
        const dy = ((e[y1 * S + qx] - e[y0 * S + qx]) / ((y1 - y0) * m)) * ex;
        const d = (LZ - dx * LX - dy * LY) / Math.sqrt(dx * dx + dy * dy + 1) - LZ; // < 0 in shadow
        if (d < 0) set(px, k * 4, DARK, Math.min(-d * DARK_GAIN, DARK_MAX));
        else set(px, k * 4, LIGHT, Math.min(d * LIGHT_GAIN, LIGHT_MAX));
      }
    }
    g.putImageData(data, 0, 0);
    let sea = null;
    if (seaData) {
      sea = canvas();
      sea.getContext("2d").putImageData(seaData, 0, 0);
    }
    return { relief, sea };
  }

  function load(k, z, x, y) {
    const entry = { relief: null, sea: null, used: 0, waiting: new Map() };
    // credentials: "omit" sends no cookies; fetch, unlike an image, tells a
    // missing tile from others
    fetch(L.Util.template(url, { z, x, y }), { credentials: "omit" })
      .then((res) => {
        if (!res.ok) throw new Error(res.status);
        return res.blob();
      })
      .then((blob) => createImageBitmap(blob))
      .then((img) => {
        Object.assign(entry, shade(img, z, y));
        img.close();
      })
      .catch(() => {
        // the coarser tile is used instead (also when offline: all of them fail)
        cache.delete(k);
        if (missing.size >= MAX_MISSING) missing.clear();
        missing.add(k);
      })
      .finally(() => {
        for (const redraw of entry.waiting.values()) redraw();
        entry.waiting.clear();
      });
    return entry;
  }

  // Draws the relief of a map tile onto its canvas, using source tiles of at
  // most zoom level top, and the sea as well if asked to. Returns whether
  // anything was drawn; if the elevations have yet to load, redraw is called
  // once they are there.
  function drawRelief(canvas, coords, redraw, top, withSea) {
    if (!url) return false;
    // a source tile has 512 pixels, so one zoom level less gives the full
    // resolution of a 256 pixel map tile
    for (let z = Math.max(0, Math.min(coords.z - 1, top)); z >= 0; z--) {
      const f = 2 ** (coords.z - z); // map tiles per source tile and side
      const x = Math.floor(coords.x / f), y = Math.floor(coords.y / f);
      const k = `${z}/${x}/${y}`;
      if (missing.has(k)) continue;
      let entry = cache.get(k);
      if (entry) cache.delete(k);
      else entry = load(k, z, x, y);
      entry.used = performance.now();
      cache.set(k, entry);
      for (const [old, e] of cache) {
        if (cache.size <= MAX_CACHE || e.used > entry.used - KEEP_MS) break;
        cache.delete(old);
      }
      if (!entry.relief) {
        entry.waiting.set(canvas, redraw);
        return false;
      }
      const part = entry.relief.width / f;
      const sx = (coords.x - x * f) * part, sy = (coords.y - y * f) * part;
      const ctx = canvas.getContext("2d");
      const size = canvas.width;
      ctx.drawImage(entry.relief, sx, sy, part, part, 0, 0, size, size);
      if (withSea && entry.sea) ctx.drawImage(entry.sea, sx, sy, part, part, 0, 0, size, size);
      return true;
    }
    return false;
  }

  // the relief and sea on the parchment of the fog
  function draw(canvas, coords, redraw) {
    drawRelief(canvas, coords, redraw, Math.min(BASE_ZOOM, maxZoom), true);
  }

  // the layer with the relief on the base map
  function layer(pane) {
    const Layer = L.GridLayer.extend({
      createTile(coords) {
        const canvas = document.createElement("canvas");
        const size = this.getTileSize();
        canvas.width = size.x;
        canvas.height = size.y;
        const paint = () => {
          canvas.getContext("2d").clearRect(0, 0, size.x, size.y);
          drawRelief(canvas, coords, paint, maxZoom, false);
        };
        paint();
        return canvas;
      },
    });
    return new Layer({ pane, attribution, opacity: MAP_ALPHA, noWrap: true });
  }

  return { configure, draw, layer, enabled: () => url !== "" };
})();

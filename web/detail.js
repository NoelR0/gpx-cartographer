// Track detail view: one track alone on the map, with charts and statistics
// in a side panel (bottom sheet on phones). Opened via #track=<id>; the full
// profile comes from /api/track/detail and is not kept once the view closes.
window.GPXDetail = (() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const HASH = "#track=";
  const PAUSE_S = 60;          // gaps longer than this break speed windows and lines
  const SPEED_WINDOW_S = 20;   // speeds are averaged over at least this long
  const GRADE_WINDOW_M = 100;  // gradients are measured over this distance
  const ELE_SMOOTH_M = 40;     // elevation is averaged over ±this distance
  const MOVING_MPS = 0.3;      // same threshold as the server's moving time
  const STEEP_MIN_M = 200;     // "steepest" sections must be at least this long

  // diverging gradient scale (downhill blue, flat gray, uphill orange); the
  // map tiles are always light, so the map uses the light-mode values
  const GRADE_BANDS = [-Infinity, -15, -8, -3, 3, 8, 15, Infinity];
  const GRADE_MAP = ["#184f95", "#2a78d6", "#9fc4f2", "#9ba1ab", "#f5b394", "#eb6834", "#a8401a"];
  // sequential scale for speed and elevation on the map
  const SEQ_MAP = ["#f2c46d", "#eb9a45", "#dc6a2c", "#b0401c", "#6e1f0d"];

  // ---------- Formatting ----------
  const num = (v, digits = 0) => v.toLocaleString(undefined, { maximumFractionDigits: digits, minimumFractionDigits: digits });
  const fmtKm = (m) => num(m / 1000, m < 10000 ? 2 : 1) + " km";
  const fmtDuration = (s) => {
    if (s == null || !isFinite(s)) return "–";
    const min = Math.round(s / 60);
    if (min < 60) return `${min} min`;
    return `${Math.floor(min / 60)} h ${String(min % 60).padStart(2, "0")} min`;
  };
  const fmtClock = (s) => `${Math.floor(s / 3600)}:${String(Math.floor((s % 3600) / 60)).padStart(2, "0")}`;
  const fmtSpeed = (mps) => (mps > 0 ? num(mps * 3.6, 1) + " km/h" : "–");
  const fmtPace = (mps) => {
    if (!(mps > 0.05)) return "–";
    const s = Math.round(1000 / mps);
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")} /km`;
  };
  const fmtEle = (m) => (m == null ? "–" : num(m) + " m");
  const fmtGrade = (g) => (g == null ? "–" : (g > 0 ? "+" : "") + num(g, Math.abs(g) < 10 ? 1 : 0) + " %");
  const fmtDateTime = (d) => d.toLocaleString(undefined, { dateStyle: "full", timeStyle: "short" });
  const fmtTime = (d) => d.toLocaleTimeString(undefined, { timeStyle: "short" });

  // ---------- State ----------
  let ctx = null;              // callbacks from app.js
  let tracks = new Map();      // id -> {data, color}
  let loaded = false;
  let cur = null;              // {id, t, color, p (profile) | null, error}
  let seq = 0;                 // ignores responses to outdated requests
  let returnHash = "#map";
  const state = { x: "dist", color: "plain", split: "speed" };
  let layer = null;            // everything the detail view draws on the map
  let lineLayer = null;
  let hoverMarker = null;
  let splitLine = null;
  let mapLeaveTimer = 0;
  let charts = {};             // hover data of the line charts, by key
  let xAxis = null;            // shared x axis of the line charts
  let tips = [];

  // ---------- Profile ----------
  // Derives smoothed elevation, gradient, speed and cumulative values from
  // the raw points of /api/track/detail.
  function buildProfile(t, raw) {
    const n = raw.lat.length;
    const dist = raw.dist_m, ele = raw.ele, time = raw.time_s;
    const segStart = new Uint8Array(n);
    for (const s of raw.seg_start) segStart[s] = 1;
    // brk[i]: the line must not connect point i-1 with point i
    const brk = new Uint8Array(n);
    for (let i = 0; i < n; i++) {
      brk[i] = i === 0 || segStart[i] ||
        (time[i] != null && time[i - 1] != null && time[i] - time[i - 1] > PAUSE_S) ? 1 : 0;
    }
    // window [j, k] around i that stays within one segment
    const around = (i, ok) => {
      let j = i, k = i;
      let moved = true;
      while (moved) {
        moved = false;
        if (j > 0 && !segStart[j] && ok(j - 1, k)) { j--; moved = true; }
        if (k < n - 1 && !segStart[k + 1] && ok(j, k + 1)) { k++; moved = true; }
      }
      return [j, k];
    };

    const hasEle = ele.filter((v) => v != null).length > n / 2;
    const hasTime = time.filter((v) => v != null).length > n / 2 && t.start;

    // elevation: mean over ±ELE_SMOOTH_M
    const es = new Array(n).fill(null);
    if (hasEle) {
      for (let i = 0; i < n; i++) {
        const [j, k] = around(i, (a, b) => dist[i] - dist[a] <= ELE_SMOOTH_M && dist[b] - dist[i] <= ELE_SMOOTH_M);
        let s = 0, c = 0;
        for (let x = j; x <= k; x++) if (ele[x] != null) { s += ele[x]; c++; }
        es[i] = c ? s / c : null;
      }
    }
    // gradient in % over GRADE_WINDOW_M
    const grade = new Array(n).fill(null);
    if (hasEle) {
      const half = GRADE_WINDOW_M / 2;
      for (let i = 0; i < n; i++) {
        const [j, k] = around(i, (a, b) => dist[i] - dist[a] <= half && dist[b] - dist[i] <= half);
        const d = dist[k] - dist[j];
        if (d >= GRADE_WINDOW_M / 4 && es[j] != null && es[k] != null) grade[i] = ((es[k] - es[j]) / d) * 100;
      }
    }
    // speed in m/s over at least SPEED_WINDOW_S, not across pauses
    const speed = new Array(n).fill(null);
    if (hasTime) {
      for (let i = 0; i < n; i++) {
        if (time[i] == null) continue;
        const [j, k] = around(i, (a, b) => !brk[a + 1] && !brk[b] && time[a] != null && time[b] != null &&
          (time[b] - time[a] <= SPEED_WINDOW_S || b - a < 2));
        const dt = time[k] - time[j];
        if (dt > 0) speed[i] = (dist[k] - dist[j]) / dt;
      }
    }

    // cumulative moving time, ascent and descent (hysteresis like the server)
    const cumMove = new Float64Array(n), cumAsc = new Float64Array(n), cumDesc = new Float64Array(n);
    let ref = null;
    for (let i = 0; i < n; i++) {
      if (i > 0) {
        cumMove[i] = cumMove[i - 1];
        cumAsc[i] = cumAsc[i - 1];
        cumDesc[i] = cumDesc[i - 1];
        const dt = time[i] != null && time[i - 1] != null ? time[i] - time[i - 1] : 0;
        if (!segStart[i] && dt > 0 && dt <= 300 && (dist[i] - dist[i - 1]) / dt >= MOVING_MPS) cumMove[i] += dt;
      }
      if (es[i] == null) continue;
      if (ref == null || segStart[i]) ref = es[i];
      else if (es[i] - ref >= 5) { cumAsc[i] += es[i] - ref; ref = es[i]; }
      else if (ref - es[i] >= 5) { cumDesc[i] += ref - es[i]; ref = es[i]; }
    }

    return {
      n, lat: raw.lat, lon: raw.lon, dist, ele, time, es, grade, speed, brk, segStart,
      cumMove, cumAsc, cumDesc, hasEle, hasTime: Boolean(hasTime),
    };
  }

  // value of the cumulative array arr at distance d (linear interpolation)
  function atDist(p, arr, d) {
    let lo = 0, hi = p.n - 1;
    if (d <= p.dist[0]) return arr[0];
    if (d >= p.dist[hi]) return arr[hi];
    while (hi - lo > 1) {
      const mid = (lo + hi) >> 1;
      if (p.dist[mid] <= d) lo = mid; else hi = mid;
    }
    const span = p.dist[hi] - p.dist[lo];
    const f = span ? (d - p.dist[lo]) / span : 0;
    return arr[lo] + (arr[hi] - arr[lo]) * f;
  }

  // index of the first point at or after distance d
  function indexAt(p, d) {
    let lo = 0, hi = p.n - 1;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (p.dist[mid] < d) lo = mid + 1; else hi = mid;
    }
    return lo;
  }

  function splitLength(total) {
    if (total <= 25000) return 1000;
    if (total <= 100000) return 5000;
    return 10000;
  }

  function splits(p) {
    const total = p.dist[p.n - 1];
    const len = splitLength(total);
    const out = [];
    for (let a = 0; a < total - 1; a += len) {
      const b = Math.min(total, a + len);
      const s = {
        from: a, to: b,
        moving: atDist(p, p.cumMove, b) - atDist(p, p.cumMove, a),
        asc: atDist(p, p.cumAsc, b) - atDist(p, p.cumAsc, a),
        desc: atDist(p, p.cumDesc, b) - atDist(p, p.cumDesc, a),
      };
      s.speed = s.moving > 0 ? (b - a) / s.moving : 0;
      out.push(s);
    }
    // a tiny remainder (e.g. the last 30 m) is merged into the previous split
    if (out.length > 1 && out[out.length - 1].to - out[out.length - 1].from < len * 0.2) {
      const last = out.pop(), prev = out[out.length - 1];
      prev.to = last.to;
      prev.moving += last.moving;
      prev.asc += last.asc;
      prev.desc += last.desc;
      prev.speed = prev.moving > 0 ? (prev.to - prev.from) / prev.moving : 0;
    }
    return out;
  }

  // steepest climb and descent over at least STEEP_MIN_M
  function steepest(p) {
    let up = null, down = null;
    for (let i = 0; i < p.n; i++) {
      if (p.es[i] == null) continue;
      const k = indexAt(p, p.dist[i] + STEEP_MIN_M);
      if (k >= p.n || p.es[k] == null) break;
      let crosses = false;
      for (let x = i + 1; x <= k; x++) if (p.segStart[x]) { crosses = true; break; }
      if (crosses) continue;
      const g = ((p.es[k] - p.es[i]) / (p.dist[k] - p.dist[i])) * 100;
      if (!up || g > up.g) up = { g, i, k };
      if (!down || g < down.g) down = { g, i, k };
    }
    return { up: up && up.g > 0 ? up : null, down: down && down.g < 0 ? down : null };
  }

  function summary(t, p) {
    let maxEle = null, minEle = null, maxSpeed = 0;
    for (let i = 0; i < p.n; i++) {
      const e = p.es[i];
      if (e != null) {
        if (maxEle == null || e > maxEle.v) maxEle = { v: e, i };
        if (minEle == null || e < minEle.v) minEle = { v: e, i };
      }
      if (p.speed[i] > maxSpeed) maxSpeed = p.speed[i];
    }
    const moving = t.moving_s ?? t.duration_s ?? 0;
    return {
      maxEle, minEle, maxSpeed,
      moving,
      elapsed: t.duration_s || 0,
      speedMoving: moving ? t.distance_m / moving : 0,
      speedElapsed: t.duration_s ? t.distance_m / t.duration_s : 0,
      steep: p.hasEle ? steepest(p) : { up: null, down: null },
    };
  }

  // pace for walking/running speeds, km/h for anything faster
  const usePace = (s) => s.speedMoving > 0 && s.speedMoving < 10 / 3.6;

  // ---------- Map ----------
  const bucket = (v, cuts) => {
    let b = 0;
    while (b < cuts.length && v > cuts[b]) b++;
    return b;
  };
  const quantile = (sorted, q) => sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))];

  // colour scale of the current map colouring: {colorOf(i), legend}
  function mapScale(p) {
    if (state.color === "grade" && p.hasEle) {
      return {
        colorOf: (i) => (p.grade[i] == null ? null : GRADE_MAP[bucket(p.grade[i], GRADE_BANDS.slice(1, -1))]),
        legend: GRADE_MAP.map((c, b) => ({ c, label: gradeBandLabel(b) })),
      };
    }
    const values = state.color === "speed" && p.hasTime ? p.speed
      : state.color === "ele" && p.hasEle ? p.es : null;
    if (!values) return null;
    const sorted = values.filter((v) => v != null).sort((a, b) => a - b);
    if (!sorted.length) return null;
    // spread the colours over the 5–95 % range, so single outliers do not wash them out
    const lo = quantile(sorted, 0.05), hi = quantile(sorted, 0.95);
    const cuts = [1, 2, 3, 4].map((k) => lo + ((hi - lo) * k) / 5);
    // a decimal place when the range is narrow, so the bands stay distinguishable
    const fmt = state.color === "speed" ? (v) => num(v * 3.6, (hi - lo) * 3.6 < 10 ? 1 : 0) : (v) => num(v, 0);
    const unit = state.color === "speed" ? "km/h" : "m";
    const edges = [lo, ...cuts, hi];
    return {
      colorOf: (i) => (values[i] == null ? null : SEQ_MAP[bucket(values[i], cuts)]),
      legend: SEQ_MAP.map((c, b) => ({ c, label: `${fmt(edges[b])}–${fmt(edges[b + 1])} ${unit}` })),
    };
  }

  function gradeBandLabel(b) {
    const lo = GRADE_BANDS[b], hi = GRADE_BANDS[b + 1];
    if (lo === -Infinity) return `< ${hi} %`;
    if (hi === Infinity) return `> ${lo} %`;
    return `${lo} to ${hi} %`;
  }

  function drawMap(fit) {
    const { map } = ctx;
    if (!layer) layer = L.layerGroup().addTo(map);
    if (lineLayer) layer.removeLayer(lineLayer);
    lineLayer = L.featureGroup();
    const p = cur.p;
    const runs = [];   // [{color, pts}]
    if (p) {
      const scale = mapScale(p);
      for (let i = 0; i < p.n; i++) {
        const ll = [p.lat[i], p.lon[i]];
        const color = (scale && scale.colorOf(i)) || cur.color;
        const last = runs[runs.length - 1];
        if (p.segStart[i] || !last) runs.push({ color, pts: [ll] });
        else if (last.color === color) last.pts.push(ll);
        else runs.push({ color, pts: [last.pts[last.pts.length - 1], ll] }); // starts where the last run ended
      }
    } else {
      for (const seg of cur.t.segments) runs.push({ color: cur.color, pts: seg });
    }
    // white casing keeps the line readable on any map background
    for (const r of runs) lineLayer.addLayer(L.polyline(r.pts, { color: "#fff", weight: 9, opacity: 0.85, interactive: false }));
    for (const r of runs) {
      const line = L.polyline(r.pts, { color: r.color, weight: 5, opacity: 1 });
      if (p) {
        line.on("mousemove", (e) => { clearTimeout(mapLeaveTimer); setHover(nearestPoint(e.latlng), e.originalEvent); });
        // the line consists of many pieces when it is coloured; only clear the
        // hover if the pointer does not enter the next piece right away
        line.on("mouseout", () => { clearTimeout(mapLeaveTimer); mapLeaveTimer = setTimeout(clearHover, 120); });
        line.on("click", (e) => { L.DomEvent.stopPropagation(e); setHover(nearestPoint(e.latlng), e.originalEvent); });
      }
      lineLayer.addLayer(line);
    }
    // start and finish
    const first = p ? [p.lat[0], p.lon[0]] : cur.t.segments[0][0];
    const lastSeg = cur.t.segments[cur.t.segments.length - 1];
    const lastPt = p ? [p.lat[p.n - 1], p.lon[p.n - 1]] : lastSeg[lastSeg.length - 1];
    const pin = (cls, title) => L.divIcon({ className: "", iconSize: [18, 18], html: `<div class="detail-pin ${cls}" title="${title}"></div>` });
    lineLayer.addLayer(L.marker(lastPt, { icon: pin("finish", "Finish"), interactive: false, keyboard: false }));
    lineLayer.addLayer(L.marker(first, { icon: pin("start", "Start"), interactive: false, keyboard: false }));
    // split markers (every km, 5 km or 10 km)
    if (p) {
      const len = splitLength(p.dist[p.n - 1]);
      for (let d = len; d < p.dist[p.n - 1] - len * 0.2; d += len) {
        const i = indexAt(p, d);
        lineLayer.addLayer(L.marker([p.lat[i], p.lon[i]], {
          icon: L.divIcon({ className: "", iconSize: [22, 16], html: `<div class="detail-km">${d / 1000}</div>` }),
          interactive: false, keyboard: false,
        }));
      }
    }
    layer.addLayer(lineLayer);
    if (fit) fitTrack();
  }

  // keeps the track clear of the panel (right side, or bottom sheet on phones)
  function fitTrack(bounds) {
    const panel = $("detail");
    const sheet = window.matchMedia("(max-width: 640px)").matches;
    const r = panel.getBoundingClientRect();
    const b = bounds || lineLayer?.getBounds();
    if (!b || !b.isValid()) return;
    ctx.map.fitBounds(b, {
      paddingTopLeft: [30, 30],
      paddingBottomRight: sheet ? [30, window.innerHeight - r.top + 30] : [r.width + 42, 30],
      maxZoom: 17,
    });
  }

  function nearestPoint(latlng) {
    const p = cur.p;
    const k = Math.cos((latlng.lat * Math.PI) / 180);
    let best = 0, bd = Infinity;
    for (let i = 0; i < p.n; i++) {
      const dy = p.lat[i] - latlng.lat, dx = (p.lon[i] - latlng.lng) * k;
      const d = dx * dx + dy * dy;
      if (d < bd) { bd = d; best = i; }
    }
    return best;
  }

  function showSplitOnMap(s) {
    if (splitLine && splitLine.split === s) return splitLine;
    hideSplitOnMap();
    if (!s) return;
    const p = cur.p;
    const pts = [];
    for (let i = indexAt(p, s.from); i < p.n && p.dist[i] <= s.to; i++) pts.push([p.lat[i], p.lon[i]]);
    if (pts.length < 2) return;
    splitLine = L.polyline(pts, { color: "#1d1f23", weight: 9, opacity: 0.55, interactive: false }).addTo(layer);
    splitLine.split = s;
    return splitLine;
  }
  function hideSplitOnMap() {
    if (splitLine) { layer.removeLayer(splitLine); splitLine = null; }
  }

  // ---------- Charts ----------
  function niceStep(raw) {
    const p = 10 ** Math.floor(Math.log10(raw));
    const f = raw / p;
    return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10) * p;
  }
  function niceScale(lo, hi, count = 4) {
    if (!(hi > lo)) { hi = lo + 1; }
    const step = niceStep((hi - lo) / count);
    const a = Math.floor(lo / step) * step, b = Math.ceil(hi / step) * step;
    const ticks = [];
    for (let v = a; v <= b + step * 0.001; v += step) ticks.push(v);
    return { lo: a, hi: b, ticks };
  }
  function timeTicks(max) {
    const steps = [300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400];
    const step = steps.find((s) => max / s <= 6) || 86400;
    const ticks = [];
    for (let v = 0; v <= max; v += step) ticks.push(v);
    return ticks;
  }

  // x axis shared by all line charts: distance (km) or elapsed time
  function buildAxis(p, width) {
    const m = { top: 12, right: 10, bottom: 24, left: 46 };
    const iw = width - m.left - m.right;
    const idx = [], xs = [];
    const byTime = state.x === "time" && p.hasTime;
    for (let i = 0; i < p.n; i++) {
      const v = byTime ? p.time[i] : p.dist[i];
      if (v == null) continue;
      if (byTime && xs.length && v < xs[xs.length - 1]) continue; // unsorted timestamps
      idx.push(i);
      xs.push(v);
    }
    const max = xs[xs.length - 1] || 1;
    const ticks = byTime ? timeTicks(max) : niceScale(0, max / 1000, width < 380 ? 3 : 5).ticks.map((v) => v * 1000).filter((v) => v <= max * 1.0001);
    const x = (v) => m.left + (v / max) * iw;
    const label = byTime ? (v) => fmtClock(v) : (v) => num(v / 1000, v % 1000 ? 1 : 0);
    const xOf = (i) => (byTime ? p.time[i] : p.dist[i]);
    return { m, iw, idx, xs, max, ticks, x, label, xOf, byTime, unit: byTime ? "h" : "km" };
  }

  // single-series line (or area) chart over the shared x axis
  function lineChart(key, width, height, ys, { fmtAxis, area, refLine, photos } = {}) {
    const A = xAxis, p = cur.p, m = A.m;
    const ih = height - m.top - m.bottom;
    let lo = Infinity, hi = -Infinity;
    for (const i of A.idx) if (ys[i] != null) { lo = Math.min(lo, ys[i]); hi = Math.max(hi, ys[i]); }
    if (!isFinite(lo)) return "";
    if (!area) lo = Math.min(lo, 0);
    const sc = niceScale(lo, hi, 4);
    const y = (v) => m.top + ih - ((v - sc.lo) / (sc.hi - sc.lo)) * ih;
    const base = m.top + ih;

    let out = sc.ticks.map((v) =>
      `<line class="grid${v === sc.lo ? " base" : ""}" x1="${m.left}" x2="${width - m.right}" y1="${y(v)}" y2="${y(v)}"/>` +
      `<text class="tick" x="${m.left - 6}" y="${y(v)}" text-anchor="end" dominant-baseline="middle">${esc(fmtAxis(v))}</text>`).join("");
    out += A.ticks.map((v, k) =>
      `<text class="xlabel" x="${A.x(v)}" y="${base + 16}" text-anchor="${k === 0 ? "start" : "middle"}">${esc(A.label(v))}${k === A.ticks.length - 1 && A.x(v) < width - 40 ? " " + A.unit : ""}</text>`).join("");

    // one sub-path per run of points without a gap
    const runs = [];
    let run = null;
    for (const i of A.idx) {
      if (ys[i] == null || (run && p.brk[i] && !area)) { run = null; if (ys[i] == null) continue; }
      if (!run) runs.push(run = []);
      run.push(`${A.x(A.xOf(i)).toFixed(1)},${y(ys[i]).toFixed(1)}`);
    }
    for (const r of runs) {
      if (area && r.length > 1) {
        out += `<path class="area" d="M${r[0].split(",")[0]},${base}L${r.join("L")}L${r[r.length - 1].split(",")[0]},${base}Z"/>`;
      }
      out += `<path class="line s1" d="M${r.join("L")}"/>`;
    }
    if (refLine != null && refLine > sc.lo && refLine < sc.hi) {
      out += `<line class="ref" x1="${m.left}" x2="${width - m.right}" y1="${y(refLine)}" y2="${y(refLine)}"/>`;
    }
    out += `<line class="crosshair" x1="0" x2="0" y1="${m.top}" y2="${base}" visibility="hidden"/>`;
    out += `<circle class="dot s1" r="4" visibility="hidden"/>`;
    out += `<rect class="hit-line" data-chart="${key}" x="${m.left}" y="${m.top}" width="${A.iw}" height="${ih}"/>`;
    // photos taken during the track, as dots on the line (above the hover area, so they can be hovered)
    for (const ph of photos || []) {
      const i = ph.i;
      if (ys[i] == null) continue;
      out += `<circle class="photo-dot" cx="${A.x(A.xOf(i))}" cy="${y(ys[i])}" r="5" data-photo="${ph.k}"/>`;
    }
    charts[key] = { y, ys };
    return `<svg class="chart" data-key="${key}" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">${out}</svg>`;
  }

  // bars: [{label, tip, split?, values: [{value, cls}]}]; several values are
  // drawn side by side (2px apart). xTitle names the x axis below the labels.
  function barChart(width, height, bars, { fmtAxis, unit, xTitle } = {}) {
    const m = { top: 14, right: 8, bottom: xTitle ? 38 : 24, left: 46 };
    const iw = width - m.left - m.right, ih = height - m.top - m.bottom;
    const max = Math.max(...bars.flatMap((b) => b.values.map((v) => v.value)), 0);
    const sc = niceScale(0, max, 4);
    const y = (v) => m.top + ih - (v / sc.hi) * ih;
    const band = iw / bars.length;
    const nv = bars[0]?.values.length || 1;
    const bw = Math.max(2, Math.min(28, (band - 2 - 2 * (nv - 1)) / nv));
    const every = band < 26 ? Math.ceil(26 / band) : 1;
    let out = sc.ticks.map((v) =>
      `<line class="grid${v === 0 ? " base" : ""}" x1="${m.left}" x2="${width - m.right}" y1="${y(v)}" y2="${y(v)}"/>` +
      `<text class="tick" x="${m.left - 6}" y="${y(v)}" text-anchor="end" dominant-baseline="middle">${esc(fmtAxis(v))}</text>`).join("");
    bars.forEach((b, i) => {
      const cx = m.left + band * (i + 0.5);
      const gw = nv * bw + 2 * (nv - 1);
      b.values.forEach((v, j) => {
        const h = y(0) - y(v.value);
        if (h <= 0) return;
        const r = Math.min(4, bw / 2, h), x0 = cx - gw / 2 + j * (bw + 2), top = y(v.value);
        out += `<path class="bar ${v.cls || "s1"}" d="M${x0},${top + h}V${top + r}Q${x0},${top} ${x0 + r},${top}H${x0 + bw - r}Q${x0 + bw},${top} ${x0 + bw},${top + r}V${top + h}Z"/>`;
      });
      if (i % every === 0) out += `<text class="xlabel" x="${cx}" y="${m.top + ih + 16}" text-anchor="middle">${esc(b.label)}</text>`;
      out += `<rect class="hit" x="${cx - band / 2}" y="${m.top}" width="${band}" height="${ih + m.bottom}" data-tip="${tips.push(b.tip) - 1}"` +
        `${b.split != null ? ` data-split="${b.split}"` : ""}/>`;
    });
    if (unit) out += `<text class="tick" x="${width - m.right}" y="${m.top - 4}" text-anchor="end">${esc(unit)}</text>`;
    if (xTitle) out += `<text class="xsub" x="${m.left + iw / 2}" y="${height - 4}" text-anchor="middle">${esc(xTitle)}</text>`;
    return `<svg class="chart" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">${out}</svg>`;
  }

  // ---------- Sections ----------
  function tile(label, value, sub = "") {
    return `<div class="tile"><div class="tile-label">${esc(label)}</div><div class="tile-value">${value}</div>` +
      (sub ? `<div class="tile-sub">${sub}</div>` : "") + `</div>`;
  }

  function tilesSection(t, p, s, photos) {
    const pace = usePace(s);
    const out = [tile("Distance", esc(fmtKm(t.distance_m)))];
    if (t.duration_s) {
      out.push(tile("Moving time", esc(fmtDuration(s.moving)), s.elapsed > s.moving ? `${esc(fmtDuration(s.elapsed - s.moving))} breaks` : ""));
      out.push(tile("Elapsed time", esc(fmtDuration(s.elapsed)),
        `${esc(fmtTime(new Date(t.start)))} – ${esc(fmtTime(new Date(t.end)))}`));
      out.push(tile(pace ? "Avg. pace moving" : "Avg. speed moving",
        esc(pace ? fmtPace(s.speedMoving) : fmtSpeed(s.speedMoving)),
        esc(pace ? fmtSpeed(s.speedMoving) : fmtPace(s.speedMoving))));
      out.push(tile("Avg. speed incl. breaks", esc(fmtSpeed(s.speedElapsed))));
      if (p && p.hasTime) out.push(tile("Max. speed", esc(fmtSpeed(s.maxSpeed)), `over ${SPEED_WINDOW_S} s`));
    }
    if (t.ascent_m || t.descent_m) {
      out.push(tile("Ascent", esc(num(t.ascent_m) + " m"), `↓ ${esc(num(t.descent_m))} m descent`));
      out.push(tile("Climb per km", esc(num(t.ascent_m / Math.max(0.1, t.distance_m / 1000)) + " m")));
    }
    if (p && s.maxEle) {
      out.push(tile("Highest point", esc(fmtEle(s.maxEle.v)), `lowest ${esc(fmtEle(s.minEle.v))}`));
    }
    if (s.steep.up) out.push(tile("Steepest climb", esc(fmtGrade(s.steep.up.g)), `over ${STEEP_MIN_M} m`));
    if (s.steep.down) out.push(tile("Steepest descent", esc(fmtGrade(s.steep.down.g)), `over ${STEEP_MIN_M} m`));
    if (photos.length) out.push(tile("Photos", esc(num(photos.length))));
    return `<div class="tiles detail-tiles">${out.join("")}</div>`;
  }

  function seg(name, options, value, disabled = {}) {
    return `<div class="segmented" role="group" data-set="${name}">` + options.map(([v, label]) =>
      `<button data-v="${v}" aria-pressed="${v === value}"${disabled[v] ? " disabled" : ""}>${esc(label)}</button>`).join("") + `</div>`;
  }

  function colorSection(p) {
    if (!p.hasTime && !p.hasEle) return "";
    const scale = mapScale(p);
    const legend = scale
      ? `<div class="detail-legend">${scale.legend.map((l) => `<span><i style="background:${l.c}"></i>${esc(l.label)}</span>`).join("")}</div>`
      : "";
    return `<section class="detail-sec"><h3>Colour the track by</h3>` +
      seg("color", [["plain", "Track"], ["speed", "Speed"], ["grade", "Gradient"], ["ele", "Elevation"]], state.color,
        { speed: !p.hasTime, grade: !p.hasEle, ele: !p.hasEle }) + legend + `</section>`;
  }

  function chartsSection(p, s, width, photos) {
    let out = "";
    if (p.hasTime) {
      out += `<div class="detail-axis"><span>X axis</span>${seg("x", [["dist", "Distance"], ["time", "Time"]], state.x)}</div>`;
    }
    if (p.hasEle) {
      out += `<section class="detail-sec"><h3>Elevation</h3>` +
        lineChart("ele", width, 170, p.es, { fmtAxis: (v) => num(v) + " m", area: true, photos }) + `</section>`;
    }
    if (p.hasTime) {
      out += `<section class="detail-sec"><h3>Speed <span class="card-sub">km/h · dashed: average moving</span></h3>` +
        lineChart("speed", width, 150, p.speed.map((v) => (v == null ? null : v * 3.6)),
          { fmtAxis: (v) => num(v), refLine: s.speedMoving * 3.6 }) + `</section>`;
    }
    if (p.hasEle) {
      out += `<section class="detail-sec"><h3>Gradient <span class="card-sub">over ${GRADE_WINDOW_M} m</span></h3>` +
        lineChart("grade", width, 130, p.grade, { fmtAxis: (v) => num(v) + " %" }) + `</section>`;
    }
    return out;
  }

  function splitsSection(p, s, width) {
    if (!p.hasTime && !p.hasEle) return "";
    const list = splits(p);
    if (list.length < 2) return "";
    const pace = usePace(s);
    const label = (sp) => num(sp.to / 1000, sp.to % 1000 ? 1 : 0);
    // what the bars show; only the measures this track has data for
    const metrics = [];
    if (p.hasTime) metrics.push(["speed", pace ? "Pace" : "Speed"], ["time", "Duration"]);
    if (p.hasEle) metrics.push(["ele", "Elevation"]);
    if (!metrics.some(([v]) => v === state.split)) state.split = metrics[0][0];
    const opts = {
      // pace bars: taller = slower, so the axis says what it shows
      speed: {
        values: (sp) => [{ value: pace ? (sp.speed > 0 ? 1000 / sp.speed / 60 : 0) : sp.speed * 3.6 }],
        fmtAxis: pace ? (v) => `${num(v)}′` : (v) => num(v), unit: pace ? "min/km" : "km/h",
      },
      time: { values: (sp) => [{ value: sp.moving / 60 }], fmtAxis: (v) => num(v), unit: "min moving" },
      ele: {
        values: (sp) => [{ value: sp.asc, cls: "g5" }, { value: sp.desc, cls: "g1" }],
        fmtAxis: (v) => num(v), unit: "m",
      },
    }[state.split];
    const chart = barChart(width, 160, list.map((sp, k) => ({
      label: label(sp),
      values: opts.values(sp),
      split: k,
      tip: `<b>${esc(fmtKm(sp.from))} – ${esc(fmtKm(sp.to))}</b>` +
        (p.hasTime ? `<div>${esc(pace ? fmtPace(sp.speed) : fmtSpeed(sp.speed))} · ${esc(fmtDuration(sp.moving))} moving</div>` : "") +
        (p.hasEle ? `<div class="tip-row muted">↑ ${esc(num(sp.asc))} m · ↓ ${esc(num(sp.desc))} m</div>` : "") +
        `<div class="tip-row muted">Click to show on the map</div>`,
    })), { fmtAxis: opts.fmtAxis, unit: opts.unit, xTitle: "Split end (km)" });
    const legend = state.split === "ele"
      ? `<div class="detail-legend"><span><i class="g5"></i>Ascent</span><span><i class="g1"></i>Descent</span></div>` : "";
    const rows = list.map((sp, k) => `<tr data-split="${k}"><th>${esc(label(sp))}</th>` +
      (p.hasTime ? `<td>${esc(fmtDuration(sp.moving))}</td><td>${esc(pace ? fmtPace(sp.speed) : fmtSpeed(sp.speed))}</td>` : "") +
      (p.hasEle ? `<td>${esc(num(sp.asc))} m</td><td>${esc(num(sp.desc))} m</td>` : "") + `</tr>`).join("");
    const len = splitLength(p.dist[p.n - 1]) / 1000;
    return `<section class="detail-sec"><h3>Splits <span class="card-sub">every ${len} km</span></h3>` +
      (metrics.length > 1 ? seg("split", metrics, state.split) : "") + `${legend}${chart}
      <div class="table-wrap"><table class="data splits"><thead><tr><th>km</th>` +
      (p.hasTime ? `<th>Moving</th><th>${pace ? "Pace" : "Speed"}</th>` : "") +
      (p.hasEle ? `<th>↑</th><th>↓</th>` : "") + `</tr></thead><tbody>${rows}</tbody></table></div></section>`;
  }

  // distance per gradient band, as horizontal bars
  function gradeSection(p) {
    if (!p.hasEle) return "";
    const dist = new Array(GRADE_MAP.length).fill(0);
    for (let i = 1; i < p.n; i++) {
      if (p.segStart[i] || p.grade[i] == null) continue;
      dist[bucket(p.grade[i], GRADE_BANDS.slice(1, -1))] += p.dist[i] - p.dist[i - 1];
    }
    const total = dist.reduce((a, b) => a + b, 0);
    if (!total) return "";
    const max = Math.max(...dist);
    const names = ["Steep downhill", "Downhill", "Gentle downhill", "Flat", "Gentle uphill", "Uphill", "Steep uphill"];
    // empty bands are left out; the colours still tell the bands apart
    const rows = dist.map((d, b) => !d ? "" : `<li><span class="gd-label">${esc(names[b])}<small>${esc(gradeBandLabel(b))}</small></span>` +
      `<span class="gd-bar"><i class="g${b}" style="width:${(d / max) * 100}%"></i></span>` +
      `<span class="gd-value">${esc(fmtKm(d))}<small>${num((d / total) * 100)} %</small></span></li>`).join("");
    return `<section class="detail-sec"><h3>Distance by gradient</h3><ul class="grade-dist">${rows}</ul></section>`;
  }

  // moving time per speed band
  function speedSection(p, s, width) {
    if (!p.hasTime) return "";
    const maxKmh = Math.max(1, s.maxSpeed * 3.6);
    const step = maxKmh <= 12 ? 1 : maxKmh <= 30 ? 2 : maxKmh <= 60 ? 5 : 10;
    const bins = new Array(Math.ceil(maxKmh / step) + 1).fill(0);
    for (let i = 1; i < p.n; i++) {
      const dt = p.cumMove[i] - p.cumMove[i - 1];
      if (dt <= 0 || p.speed[i] == null) continue;
      bins[Math.min(bins.length - 1, Math.floor((p.speed[i] * 3.6) / step))] += dt;
    }
    while (bins.length > 1 && !bins[bins.length - 1]) bins.pop();
    const total = bins.reduce((a, b) => a + b, 0);
    if (!total) return "";
    return `<section class="detail-sec"><h3>Time at each speed <span class="card-sub">how long you moved at which speed</span></h3>` +
      barChart(width, 160, bins.map((v, b) => ({
        label: String(b * step),
        values: [{ value: v / 60 }],
        tip: `<b>${b * step}–${(b + 1) * step} km/h</b><div>${esc(fmtDuration(v))} · ${num((v / total) * 100)} % of the moving time</div>`,
      })), { fmtAxis: (v) => num(v), unit: "min", xTitle: `Speed (km/h), in steps of ${step}` }) + `</section>`;
  }

  function photosSection(photos) {
    if (!photos.length) return "";
    return `<section class="detail-sec"><h3>Photos <span class="card-sub">${photos.length}</span></h3><div class="detail-photos">` +
      photos.map((ph, k) => `<button data-photo="${k}" title="${esc(ph.name)}"><img src="${ctx.thumbUrl(ph)}" loading="lazy" alt="${esc(ph.name)}"></button>`).join("") +
      `</div></section>`;
  }

  // ---------- Rendering ----------
  function renderHead() {
    const t = cur.t;
    $("detail-swatch").style.background = cur.color;
    $("detail-name").textContent = t.name;
    $("detail-name").title = t.file;
    $("detail-when").textContent = t.start ? fmtDateTime(new Date(t.start)) : "No timestamps (planned route)";
    $("detail-download").href = `api/track/download?file=${encodeURIComponent(t.file)}`;
  }

  function render() {
    const body = $("detail-body");
    tips = [];
    charts = {};
    const t = cur.t, p = cur.p;
    const photos = ctx.photosOfTrack(t);
    if (cur.error) {
      body.innerHTML = `<p class="note warn">Could not load the track profile: ${esc(cur.error)}</p>`;
      return;
    }
    if (!p) {
      body.innerHTML = tilesSection(t, null, summary(t, { n: 0, es: [], speed: [], hasEle: false }), photos) +
        `<p class="note">Loading profile…</p>`;
      return;
    }
    const s = summary(t, p);
    const width = Math.max(240, body.clientWidth - 32);
    xAxis = buildAxis(p, width);
    // photo index along the track (nearest timestamp)
    const dots = [];
    if (p.hasTime) {
      const t0 = Date.parse(t.start) / 1000;
      photos.forEach((ph, k) => {
        const at = Date.parse(ph.taken) / 1000 - t0;
        let best = -1, bd = Infinity;
        for (let i = 0; i < p.n; i++) {
          if (p.time[i] == null) continue;
          const d = Math.abs(p.time[i] - at);
          if (d < bd) { bd = d; best = i; }
        }
        if (best >= 0) dots.push({ i: best, k });
      });
    }
    cur.photos = photos;
    body.innerHTML = tilesSection(t, p, s, photos) + colorSection(p) + chartsSection(p, s, width, dots) +
      splitsSection(p, s, width) + gradeSection(p) + speedSection(p, s, width) + photosSection(photos) +
      `<p class="note">${esc(t.file)}</p>`;
    cur.splits = splits(p);
  }

  // ---------- Hover ----------
  function setHover(i, ev) {
    clearTimeout(mapLeaveTimer);
    const p = cur?.p;
    if (!p || i == null) return;
    const A = xAxis;
    const xv = A.xOf(i);
    for (const svg of $("detail-body").querySelectorAll("svg[data-key]")) {
      const c = charts[svg.dataset.key];
      const cross = svg.querySelector(".crosshair"), dot = svg.querySelector(".dot");
      if (xv == null) { cross.setAttribute("visibility", "hidden"); dot.setAttribute("visibility", "hidden"); continue; }
      const x = A.x(xv);
      cross.setAttribute("x1", x);
      cross.setAttribute("x2", x);
      cross.setAttribute("visibility", "visible");
      const v = c.ys[i];
      if (v == null) dot.setAttribute("visibility", "hidden");
      else {
        dot.setAttribute("cx", x);
        dot.setAttribute("cy", c.y(v));
        dot.setAttribute("visibility", "visible");
      }
    }
    const ll = [p.lat[i], p.lon[i]];
    if (!hoverMarker) {
      hoverMarker = L.circleMarker(ll, { radius: 7, color: "#1d1f23", weight: 3, fillColor: "#fff", fillOpacity: 1, interactive: false });
      layer.addLayer(hoverMarker);
    } else hoverMarker.setLatLng(ll);

    const rows = [`<b>${esc(fmtKm(p.dist[i]))}</b>`];
    if (p.time[i] != null) {
      const at = new Date(Date.parse(cur.t.start) + p.time[i] * 1000);
      rows.push(`<div class="tip-row muted">${esc(fmtTime(at))} · ${esc(fmtClock(p.time[i]))} h elapsed</div>`);
    }
    if (p.es[i] != null) rows.push(`<div>Elevation ${esc(fmtEle(p.es[i]))}</div>`);
    if (p.grade[i] != null) rows.push(`<div>Gradient ${esc(fmtGrade(p.grade[i]))}</div>`);
    if (p.speed[i] != null) {
      rows.push(`<div>Speed ${esc(fmtSpeed(p.speed[i]))}` +
        `${usePace({ speedMoving: p.speed[i] }) ? ` · ${esc(fmtPace(p.speed[i]))}` : ""}</div>`);
    }
    if (ev) showTip(rows.join(""), ev.clientX, ev.clientY);
  }

  function clearHover() {
    for (const el of $("detail-body").querySelectorAll(".crosshair, .dot")) el.setAttribute("visibility", "hidden");
    if (hoverMarker) { layer.removeLayer(hoverMarker); hoverMarker = null; }
    hideTip();
  }

  // The side of the cursor the tooltip sits on depends only on which half of
  // the window the cursor is in – not on the tooltip's width, which changes
  // with its content and would make it jump from side to side.
  let tipHtml = null;
  function showTip(html, x, y) {
    const el = $("detail-tip");
    if (html !== tipHtml) { el.innerHTML = html; tipHtml = html; }
    el.hidden = false;
    const r = el.getBoundingClientRect();
    const left = x > window.innerWidth / 2 ? x - r.width - 16 : x + 16;
    const top = y > window.innerHeight / 2 ? y - r.height - 16 : y + 16;
    el.style.left = Math.max(8, Math.min(window.innerWidth - r.width - 8, left)) + "px";
    el.style.top = Math.max(8, Math.min(window.innerHeight - r.height - 8, top)) + "px";
  }
  function hideTip() { $("detail-tip").hidden = true; }

  // pointer events can fire far more often than the screen refreshes;
  // handle only the latest one per frame
  let pendingPointer = null;
  function onPointerEvent(e) {
    if (!pendingPointer) requestAnimationFrame(() => {
      const ev = pendingPointer;
      pendingPointer = null;
      if (ev && cur) onPointer(ev); // null after the pointer left the panel
    });
    pendingPointer = e;
  }

  function onPointer(e) {
    const hit = e.target.closest?.(".hit-line");
    if (hit) {
      const A = xAxis;
      const svg = hit.ownerSVGElement;
      const px = e.clientX - svg.getBoundingClientRect().left;
      const v = ((px - A.m.left) / A.iw) * A.max;
      let lo = 0, hi = A.xs.length - 1;
      while (hi - lo > 1) {
        const mid = (lo + hi) >> 1;
        if (A.xs[mid] <= v) lo = mid; else hi = mid;
      }
      const k = Math.abs(A.xs[hi] - v) < Math.abs(A.xs[lo] - v) ? hi : lo;
      setHover(A.idx[k], e);
      return;
    }
    const dot = e.target.closest?.("circle.photo-dot");
    if (dot) {
      const ph = cur.photos[dot.dataset.photo];
      showTip(`<img class="tip-thumb" src="${ctx.thumbUrl(ph)}" alt=""><div>${esc(ph.name)}</div>`, e.clientX, e.clientY);
      return;
    }
    const split = e.target.closest?.("[data-split]");
    if (split) {
      showSplitOnMap(cur.splits[split.dataset.split]);
      const t = split.closest("[data-tip]");
      if (t) showTip(tips[t.dataset.tip], e.clientX, e.clientY);
      else hideTip();
      return;
    }
    hideSplitOnMap();
    const t = e.target.closest?.("[data-tip]");
    if (t) showTip(tips[t.dataset.tip], e.clientX, e.clientY);
    else clearHover();
  }

  function onClick(e) {
    const set = e.target.closest("[data-set] button");
    if (set && !set.disabled) {
      const name = set.parentElement.dataset.set;
      if (state[name] === set.dataset.v) return;
      state[name] = set.dataset.v;
      render();
      if (name === "color") drawMap(false);
      return;
    }
    const ph = e.target.closest("[data-photo]");
    if (ph) { ctx.openLightbox(cur.photos, Number(ph.dataset.photo)); return; }
    const split = e.target.closest("[data-split]");
    if (split) {
      const line = showSplitOnMap(cur.splits[split.dataset.split]);
      if (line) fitTrack(line.getBounds());
      return;
    }
    const hit = e.target.closest(".hit-line");
    if (hit && hoverMarker) ctx.map.panTo(hoverMarker.getLatLng());
  }

  // ---------- Opening and closing ----------
  const isOpen = () => !$("detail").hidden;

  function open(id) {
    if (!location.hash.startsWith(HASH)) returnHash = location.hash || "#map";
    location.hash = HASH + encodeURIComponent(id);
  }

  function close() {
    location.hash = returnHash.startsWith(HASH) ? "#map" : returnHash;
  }

  async function show(id) {
    const entry = tracks.get(id);
    if (!entry) {
      if (loaded) close();
      return;
    }
    const same = cur && cur.id === id;
    if (!same) {
      cur = { id, t: entry.data, color: entry.color, p: null, error: null };
      $("detail").classList.remove("min");
    } else {
      cur.t = entry.data; // after a reload
      cur.color = entry.color;
    }
    $("detail").hidden = false;
    document.body.classList.add("detail-open");
    ctx.setBaseLayers(false);
    ctx.map.closePopup();
    renderHead();
    render();
    drawMap(!same);
    if (same && cur.p) return;

    const my = ++seq;
    try {
      const res = await fetch(`api/track/detail?id=${encodeURIComponent(id)}`, { cache: "no-store" });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const raw = await res.json();
      if (my !== seq) return;
      cur.p = raw.lat.length ? buildProfile(cur.t, raw) : null;
      if (!cur.p) cur.error = "the track has no points";
    } catch (err) {
      if (my !== seq) return;
      cur.error = err.message;
    }
    if (state.x === "time" && !cur.p?.hasTime) state.x = "dist";
    render();
    drawMap(false);
  }

  function hide() {
    if (!isOpen()) return;
    seq++;
    $("detail").hidden = true;
    document.body.classList.remove("detail-open");
    hideTip();
    if (layer) { layer.clearLayers(); hoverMarker = null; lineLayer = null; splitLine = null; }
    const id = cur?.id;
    cur = null;
    ctx.setBaseLayers(true);
    if (id) ctx.onClose(id);
  }

  function applyHash() {
    if (location.hash.startsWith(HASH)) {
      const id = decodeURIComponent(location.hash.slice(HASH.length));
      if (loaded || tracks.has(id)) show(id);
    } else hide();
  }

  function setData(data, colors) {
    tracks = new Map(data.tracks.map((t, i) => [t.id, { data: t, color: colors[i] }]));
    loaded = true;
    if (location.hash.startsWith(HASH)) applyHash();
  }

  function init(opts) {
    ctx = opts;
    const body = $("detail-body");
    body.addEventListener("pointermove", onPointerEvent);
    body.addEventListener("pointerdown", onPointerEvent);
    body.addEventListener("pointerleave", () => { pendingPointer = null; clearHover(); hideSplitOnMap(); });
    body.addEventListener("scroll", hideTip, { passive: true });
    body.addEventListener("click", onClick);
    $("detail-close").addEventListener("click", close);
    $("detail-min").addEventListener("click", () => {
      $("detail").classList.toggle("min");
      clearHover();
    });
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape" && isOpen() && $("lightbox").hidden) close();
    }, { capture: true }); // before the lightbox handler, which would close the lightbox first
    let resizeTimer;
    window.addEventListener("resize", () => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => { if (isOpen() && cur) render(); }, 150);
    });
    window.addEventListener("hashchange", applyHash);
  }

  return { init, setData, open };
})();

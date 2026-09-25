// Statistics view: all numbers are computed in the browser from /api/data.
// Tracks without timestamps (e.g. planned routes) are not counted.
window.GPXStats = (() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const DAY_MS = 86400000;

  // ---------- Formatting ----------
  const num = (v, digits = 0) => v.toLocaleString(undefined, { maximumFractionDigits: digits });
  const compact = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 });
  const fmtKm = (km) => num(km, km < 10 ? 1 : 0) + " km";
  const fmtHours = (h) => {
    const min = Math.round(h * 60);
    if (min < 60) return `${min} min`;
    return `${num(Math.floor(min / 60))} h ${String(min % 60).padStart(2, "0")} min`;
  };
  const fmtSpeed = (mps) => (mps > 0 ? num(mps * 3.6, 1) + " km/h" : "–");
  const fmtDay = (d) => d.toLocaleDateString(undefined, { day: "numeric", month: "short" });
  const fmtDate = (d) => d.toLocaleDateString(undefined, { dateStyle: "medium" });
  const monthName = (m, style = "short") => new Date(2000, m, 1).toLocaleString(undefined, { month: style });
  // 2024-01-01 was a Monday
  const weekdayName = (i, style = "short") => new Date(2024, 0, 1 + i).toLocaleString(undefined, { weekday: style });

  // What the charts measure; switchable by the user.
  const METRICS = {
    distance: { label: "Distance", get: (a) => a.km, fmt: fmtKm, axis: (v) => compact.format(v) + " km" },
    time: { label: "Moving time", get: (a) => a.movingH, fmt: fmtHours, axis: (v) => compact.format(v) + " h" },
    ascent: { label: "Ascent", get: (a) => a.ascent, fmt: (v) => num(v) + " m", axis: (v) => compact.format(v) + " m" },
    count: { label: "Tracks", get: () => 1, fmt: (v) => num(v), axis: (v) => compact.format(v), integer: true },
  };

  // ---------- State ----------
  let acts = [];           // tracks with timestamps, oldest first
  let byDay = new Map();   // "YYYY-MM-DD" -> [act]
  let untimed = 0;
  let photoCount = 0;
  let loaded = false;
  let dirty = true;
  const state = { metric: "distance", year: new Date().getFullYear(), month: new Date().getMonth(), day: null };
  let tips = [];           // tooltip HTML, referenced by data-tip index
  let lineCharts = {};     // data for crosshair tooltips of line charts
  let showTracks = () => {};
  let discovery = null;    // discovered land area of the world from /api/coverage, or {error}
  let discoverySeq = 0;    // ignores responses to outdated requests

  const pad2 = (n) => String(n).padStart(2, "0");
  const dayKey = (d) => `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
  const addDays = (d, n) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);
  const dayOfYear = (d) => Math.round((new Date(d.getFullYear(), d.getMonth(), d.getDate()) - new Date(d.getFullYear(), 0, 1)) / DAY_MS);
  const daysInYear = (y) => (new Date(y, 1, 29).getMonth() === 1 ? 366 : 365);
  const isoWeek = (d) => {
    const t = new Date(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()));
    t.setUTCDate(t.getUTCDate() + 4 - (t.getUTCDay() || 7));
    return Math.ceil(((t - Date.UTC(t.getUTCFullYear(), 0, 1)) / DAY_MS + 1) / 7);
  };
  const metric = () => METRICS[state.metric];
  const sum = (list) => list.reduce((s, a) => s + metric().get(a), 0);
  const tip = (html) => tips.push(html) - 1;

  function setData(data) {
    const tracks = data.tracks || [];
    acts = tracks.filter((t) => t.start).map((t) => {
      const date = new Date(t.start);
      return {
        t, date,
        key: dayKey(date),
        km: t.distance_m / 1000,
        ascent: t.ascent_m,
        durationS: t.duration_s || 0,
        movingS: t.moving_s ?? t.duration_s ?? 0,
        movingH: (t.moving_s ?? t.duration_s ?? 0) / 3600,
      };
    }).sort((a, b) => a.date - b.date);
    untimed = tracks.length - acts.length;
    photoCount = (data.photos || []).length;
    byDay = new Map();
    for (const a of acts) {
      if (!byDay.has(a.key)) byDay.set(a.key, []);
      byDay.get(a.key).push(a);
    }
    loaded = true;
    dirty = true;
    discovery = null;
    discoverySeq++;
    if (isVisible()) render();
  }

  // ---------- Aggregates ----------
  function totals(list) {
    let km = 0, ascent = 0, moving = 0, elapsed = 0, kmMoving = 0, kmElapsed = 0;
    for (const a of list) {
      km += a.km;
      ascent += a.ascent;
      if (a.movingS > 0) { moving += a.movingS; kmMoving += a.km; }
      if (a.durationS > 0) { elapsed += a.durationS; kmElapsed += a.km; }
    }
    return {
      count: list.length, km, ascent, moving, elapsed,
      days: new Set(list.map((a) => a.key)).size,
      speedMoving: moving ? (kmMoving * 1000) / moving : 0,
      speedElapsed: elapsed ? (kmElapsed * 1000) / elapsed : 0,
    };
  }

  function streaks() {
    const days = [...byDay.keys()].sort();
    let longest = 0, run = 0, prev = null, longestEnd = null;
    for (const k of days) {
      const d = new Date(k + "T00:00");
      run = prev && Math.round((d - prev) / DAY_MS) === 1 ? run + 1 : 1;
      if (run > longest) { longest = run; longestEnd = d; }
      prev = d;
    }
    // current streak: ending today or yesterday
    let current = 0;
    let d = new Date();
    if (!byDay.has(dayKey(d))) d = addDays(d, -1);
    while (byDay.has(dayKey(d))) { current++; d = addDays(d, -1); }
    return { longest, longestEnd, current };
  }

  const inYear = (y) => acts.filter((a) => a.date.getFullYear() === y);

  function years() {
    const ys = new Set(acts.map((a) => a.date.getFullYear()));
    ys.add(new Date().getFullYear());
    return [...ys].sort((a, b) => b - a);
  }

  // ---------- Charts ----------
  function niceTicks(max, integer) {
    if (!(max > 0)) max = integer ? 4 : 1;
    const raw = max / 4;
    const p = 10 ** Math.floor(Math.log10(raw));
    const f = raw / p;
    let step = (f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10) * p;
    if (integer) step = Math.max(1, Math.ceil(step));
    const ticks = [];
    for (let v = 0; v < max + step * 0.999; v += step) ticks.push(v);
    return ticks;
  }

  // column with a 4px rounded data end and a square base
  function barPath(x, top, w, h, r = 4) {
    if (h <= 0) return "";
    r = Math.min(r, w / 2, h);
    return `M${x},${top + h}V${top + r}Q${x},${top} ${x + r},${top}H${x + w - r}` +
      `Q${x + w},${top} ${x + w},${top + r}V${top + h}Z`;
  }

  function yAxis(ticks, y, x0, x1, fmt) {
    return ticks.map((v) =>
      `<line class="grid${v === 0 ? " base" : ""}" x1="${x0}" x2="${x1}" y1="${y(v)}" y2="${y(v)}"/>` +
      `<text class="tick" x="${x0 - 6}" y="${y(v)}" text-anchor="end" dominant-baseline="middle">${fmt(v)}</text>`,
    ).join("");
  }

  // groups: [{label, sub, bars: [{value, cls}], tip, key, active}]
  function barChart(width, groups, { height = 220, clickable = false } = {}) {
    const m = { top: 12, right: 8, bottom: groups.some((g) => g.sub) ? 40 : 26, left: 52 };
    const iw = width - m.left - m.right, ih = height - m.top - m.bottom;
    const max = Math.max(0, ...groups.flatMap((g) => g.bars.map((b) => b.value)));
    const ticks = niceTicks(max, metric().integer);
    const top = ticks[ticks.length - 1];
    const y = (v) => m.top + ih - (v / top) * ih;
    const band = iw / groups.length;
    const nb = groups[0]?.bars.length || 1;
    const bw = Math.max(3, Math.min(24, (band * 0.7 - 2 * (nb - 1)) / nb));
    // skip every other label when the columns are too narrow
    const every = band < 34 ? Math.ceil(34 / band) : 1;
    let out = yAxis(ticks, y, m.left, width - m.right, metric().axis);
    groups.forEach((g, i) => {
      const cx = m.left + band * (i + 0.5);
      const gw = nb * bw + 2 * (nb - 1);
      g.bars.forEach((b, j) => {
        const x = cx - gw / 2 + j * (bw + 2);
        out += `<path class="bar ${b.cls}" d="${barPath(x, y(b.value), bw, y(0) - y(b.value))}"/>`;
      });
      if (i % every === 0) {
        out += `<text class="xlabel${g.active ? " active" : ""}" x="${cx}" y="${m.top + ih + 16}" text-anchor="middle">${esc(g.label)}</text>`;
        if (g.sub) out += `<text class="xsub" x="${cx}" y="${m.top + ih + 31}" text-anchor="middle">${esc(g.sub)}</text>`;
      }
      if (g.active) out += `<rect class="band-active" x="${cx - band / 2}" y="${m.top}" width="${band}" height="${ih}"/>`;
      out += `<rect class="hit${clickable ? " clickable" : ""}" x="${cx - band / 2}" y="${m.top}" width="${band}" height="${ih + m.bottom}"` +
        ` data-tip="${tip(g.tip)}"${g.key != null ? ` data-key="${g.key}"` : ""}/>`;
    });
    return `<svg class="chart" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">${out}</svg>`;
  }

  // Cumulative value per day of the year, one line per year.
  function cumulativeChart(id, width, series) {
    const height = 260;
    const m = { top: 14, right: 12, bottom: 26, left: 52 };
    const iw = width - m.left - m.right, ih = height - m.top - m.bottom;
    const max = Math.max(0, ...series.map((s) => s.values[s.upto] || 0));
    const ticks = niceTicks(max, metric().integer);
    const top = ticks[ticks.length - 1];
    const x = (i) => m.left + (i / 365) * iw;
    const y = (v) => m.top + ih - (v / top) * ih;
    let out = yAxis(ticks, y, m.left, width - m.right, metric().axis);
    for (let mo = 0; mo < 12; mo++) {
      const xi = x(dayOfYear(new Date(2001, mo, 1)));
      if (width < 480 && mo % 2) continue;
      out += `<text class="xlabel" x="${xi + 2}" y="${m.top + ih + 16}">${esc(monthName(mo))}</text>`;
    }
    // oldest first, so the selected year is drawn on top
    for (const s of [...series].reverse()) {
      let d = "";
      for (let i = 0; i <= s.upto; i++) d += `${i ? "L" : "M"}${x(i).toFixed(1)},${y(s.values[i]).toFixed(1)}`;
      out += `<path class="line ${s.cls}" d="${d}"/>`;
    }
    const s0 = series[0];
    const ex = x(s0.upto), ey = y(s0.values[s0.upto]);
    out += `<circle class="dot ${s0.cls}" cx="${ex}" cy="${ey}" r="4"/>`;
    const anchor = ex > width - 120 ? "end" : "start";
    out += `<text class="endlabel" x="${ex + (anchor === "end" ? -8 : 8)}" y="${ey - 10}" text-anchor="${anchor}">` +
      `${esc(metric().fmt(s0.values[s0.upto]))}</text>`;
    out += `<line class="crosshair" x1="0" x2="0" y1="${m.top}" y2="${m.top + ih}" visibility="hidden"/>`;
    out += `<rect class="hit-line" data-chart="${id}" x="${m.left}" y="${m.top}" width="${iw}" height="${ih}"/>`;
    lineCharts[id] = { x, series, m, iw };
    return `<svg class="chart" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">${out}</svg>`;
  }

  function heatmap(year, width) {
    const first = new Date(year, 0, 1);
    const start = addDays(first, -((first.getDay() + 6) % 7)); // Monday
    const nDays = daysInYear(year);
    const weeks = Math.ceil((dayOfYear(new Date(year, 11, 31)) + (first.getDay() + 6) % 7 + 1) / 7);
    const left = 30, topPad = 18, gap = 3;
    const cell = Math.max(10, Math.min(16, Math.floor((width - left) / weeks) - gap));
    const w = left + weeks * (cell + gap), h = topPad + 7 * (cell + gap);

    const values = [];
    for (let i = 0; i < nDays; i++) {
      const list = byDay.get(dayKey(new Date(year, 0, 1 + i))) || [];
      values.push({ list, v: sum(list) });
    }
    // levels at the quartiles of all active days
    const nz = values.map((d) => d.v).filter((v) => v > 0).sort((a, b) => a - b);
    const q = (p) => nz[Math.min(nz.length - 1, Math.floor(p * nz.length))];
    const cuts = nz.length ? [q(0.25), q(0.5), q(0.75)] : [];
    const level = (v) => (v <= 0 ? 0 : 1 + cuts.filter((c) => v > c).length);

    let out = "";
    for (let r = 0; r < 7; r += 2) {
      out += `<text class="tick" x="0" y="${topPad + r * (cell + gap) + cell / 2}" dominant-baseline="middle">${esc(weekdayName(r))}</text>`;
    }
    let lastMonth = -1;
    const today = dayKey(new Date());
    for (let i = 0; i < weeks * 7; i++) {
      const d = addDays(start, i);
      if (d.getFullYear() !== year) continue;
      const col = Math.floor(i / 7), row = i % 7;
      const cx = left + col * (cell + gap), cy = topPad + row * (cell + gap);
      // label a column with the month its first day in the year belongs to
      if ((row === 0 || i === dayOfYear(first) + (first.getDay() + 6) % 7) && d.getMonth() !== lastMonth) {
        out += `<text class="xlabel" x="${cx}" y="10">${esc(monthName(d.getMonth()))}</text>`;
        lastMonth = d.getMonth();
      }
      const { list, v } = values[dayOfYear(d)];
      const key = dayKey(d);
      const names = list.slice(0, 4).map((a) => `<div class="tip-row">${esc(a.t.name)} · ${esc(fmtKm(a.km))}</div>`).join("") +
        (list.length > 4 ? `<div class="tip-row muted">+${list.length - 4} more</div>` : "");
      const t = `<b>${esc(fmtDate(d))}</b><div>${list.length ? esc(metric().fmt(v)) : "No tracks"}</div>${names}`;
      const cls = ["cell", `l${level(v)}`, key === today ? "today" : "", key === state.day ? "selected" : ""].join(" ");
      out += `<rect class="${cls}" x="${cx}" y="${cy}" width="${cell}" height="${cell}" rx="2" data-tip="${tip(t)}"` +
        `${list.length ? ` data-day="${key}"` : ""}/>`;
    }
    return `<svg class="chart heat" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">${out}</svg>`;
  }

  // ---------- Sections ----------
  function tile(label, value, sub = "", id = "") {
    return `<div class="tile"${id ? ` id="${id}"` : ""}><div class="tile-label">${esc(label)}</div><div class="tile-value">${value}</div>` +
      (sub ? `<div class="tile-sub">${sub}</div>` : "") + `</div>`;
  }

  function delta(cur, prev) {
    if (!(prev > 0)) return "";
    const p = Math.round(((cur - prev) / prev) * 100);
    const cls = p > 0 ? "up" : p < 0 ? "down" : "";
    return `<span class="delta ${cls}">${p > 0 ? "▲ +" : p < 0 ? "▼ " : "± "}${p}%</span>`;
  }

  const EARTH_KM = 40075; // circumference at the equator
  const EVEREST_M = 8848;
  // "0.26×", "3.4×": two decimals below 1 so small totals don't round to 0
  const times = (v, ref) => num(v / ref, v < ref ? 2 : 1) + "×";

  const fmtPct = (part, total) => {
    const p = total ? Math.min(100, (part / total) * 100) : 0;
    if (p === 0) return "0 %";
    if (p >= 1) return p.toLocaleString(undefined, { maximumFractionDigits: p >= 10 ? 1 : 2 }) + " %";
    return p.toLocaleString(undefined, { maximumSignificantDigits: 2 }) + " %";
  };

  // share of the world's land area uncovered in explorer mode (see tiles.js)
  function discoveryTile() {
    const tiles = GPXTiles.count();
    const tilesText = `${num(tiles)} tile${tiles === 1 ? "" : "s"}`;
    if (!discovery) return tile("Discovered", "…", esc(tilesText), "stats-discovery");
    if (discovery.error) return tile("Discovered", "–", esc(tilesText), "stats-discovery");
    const w = discovery.world;
    return tile("Discovered", esc(fmtPct(w.visited_km2, w.area_km2)),
      `${esc(tilesText)} · ${esc(num(w.visited_km2, w.visited_km2 < 10 ? 1 : 0))} km² of the world's land`, "stats-discovery");
  }

  async function loadDiscovery() {
    const seq = discoverySeq;
    let result;
    try {
      const res = await fetch("api/coverage", { cache: "no-store" });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      result = { world: (await res.json()).world };
    } catch (err) {
      result = { error: err.message };
    }
    if (seq !== discoverySeq) return;
    discovery = result;
    const el = $("stats-discovery");
    if (el) el.outerHTML = discoveryTile();
  }

  function lifetimeSection() {
    const t = totals(acts);
    const st = streaks();
    const since = acts.length ? `since ${esc(fmtDate(acts[0].date))}` : "";
    const record = (label, a, value) => a
      ? `<button class="record" data-track="${esc(a.t.id)}"><span class="record-label">${esc(label)}</span>` +
        `<span class="record-value">${esc(value)}</span><span class="record-name">${esc(a.t.name)} · ${esc(fmtDate(a.date))}</span></button>`
      : "";
    const maxBy = (f, filter = () => true) => acts.filter(filter).reduce((b, a) => (!b || f(a) > f(b) ? a : b), null);
    const longest = maxBy((a) => a.km);
    const longestTime = maxBy((a) => a.movingS);
    const highest = maxBy((a) => a.ascent);
    // speeds of very short recordings are too noisy to be a record
    const fastest = maxBy((a) => a.km / a.movingS, (a) => a.movingS > 600 && a.km >= 2);
    // same for climbing: a short ramp would beat every real mountain tour
    const steepest = maxBy((a) => a.ascent / a.km, (a) => a.km >= 3);
    let bestDay = null;
    for (const [k, list] of byDay) {
      const km = list.reduce((s, a) => s + a.km, 0);
      if (!bestDay || km > bestDay.km) bestDay = { k, km, list };
    }

    return `<section class="card">
      <div class="card-head"><h2>All time</h2><span class="card-sub">${since}</span></div>
      <div class="hero"><span class="hero-value">${esc(fmtKm(t.km))}</span>
        <span class="hero-sub">in ${num(t.count)} track${t.count === 1 ? "" : "s"} on ${num(t.days)} day${t.days === 1 ? "" : "s"}</span>
        <span class="hero-note">${esc(times(t.km, EARTH_KM))} around the Earth</span></div>
      <div class="tiles">
        ${tile("Moving time", esc(fmtHours(t.moving / 3600)))}
        ${tile("Elapsed time", esc(fmtHours(t.elapsed / 3600)), "incl. breaks")}
        ${tile("Ascent", esc(num(t.ascent) + " m"), `${esc(times(t.ascent, EVEREST_M))} Mount Everest`)}
        ${tile("Avg. speed moving", esc(fmtSpeed(t.speedMoving)))}
        ${tile("Avg. speed incl. breaks", esc(fmtSpeed(t.speedElapsed)))}
        ${tile("Avg. per track", esc(t.count ? fmtKm(t.km / t.count) : "–"), t.count ? esc(fmtHours(t.moving / 3600 / t.count)) : "")}
        ${tile("Longest streak", esc(`${st.longest} day${st.longest === 1 ? "" : "s"}`), st.longestEnd ? `until ${esc(fmtDate(st.longestEnd))}` : "")}
        ${tile("Current streak", esc(`${st.current} day${st.current === 1 ? "" : "s"}`))}
        ${tile("Photos", esc(num(photoCount)))}
        ${discoveryTile()}
      </div>
      <h3>Records</h3>
      <div class="records">
        ${record("Longest distance", longest, longest && fmtKm(longest.km))}
        ${record("Longest moving time", longestTime, longestTime && fmtHours(longestTime.movingS / 3600))}
        ${record("Most ascent", highest, highest && num(highest.ascent) + " m")}
        ${record("Steepest", steepest, steepest && num(steepest.ascent / steepest.km) + " m/km")}
        ${record("Fastest (moving)", fastest, fastest && fmtSpeed((fastest.km * 1000) / fastest.movingS))}
        ${bestDay ? `<button class="record" data-tracks="${esc(bestDay.list.map((a) => a.t.id).join("\n"))}">` +
          `<span class="record-label">Biggest day</span><span class="record-value">${esc(fmtKm(bestDay.km))}</span>` +
          `<span class="record-name">${esc(fmtDate(new Date(bestDay.k + "T00:00")))} · ${bestDay.list.length} track(s)</span></button>` : ""}
      </div>
      ${untimed ? `<p class="note">${untimed} track(s) without timestamps (e.g. planned routes) are not included.</p>` : ""}
    </section>`;
  }

  function yearSection(width) {
    const Y = state.year;
    const now = new Date();
    const current = Y === now.getFullYear();
    const cutoff = current ? dayOfYear(now) : 365;
    const ys = [Y, Y - 1, Y - 2];
    const series = ys.map((y, i) => {
      const n = daysInYear(y);
      const values = new Array(366).fill(0);
      for (const a of inYear(y)) values[dayOfYear(a.date)] += metric().get(a);
      for (let d = 1; d < 366; d++) values[d] += values[d - 1];
      const upto = y === now.getFullYear() ? dayOfYear(now) : n - 1;
      return { year: y, cls: `s${i + 1}`, values, upto };
    });
    // same date comparison
    const at = (s) => s.values[Math.min(cutoff, s.upto)];
    const headline = series.map((s, i) =>
      `<span class="cmp"><span class="key ${s.cls}"></span><b>${s.year}</b> ${esc(metric().fmt(at(s)))}` +
      `${i === 0 ? " " + delta(at(s), at(series[1])) : ""}</span>`).join("");

    const rows = [
      ["Tracks", (t) => num(t.count)],
      ["Distance", (t) => fmtKm(t.km)],
      ["Moving time", (t) => fmtHours(t.moving / 3600)],
      ["Elapsed time", (t) => fmtHours(t.elapsed / 3600)],
      ["Ascent", (t) => num(t.ascent) + " m"],
      ["Active days", (t) => num(t.days)],
      ["Avg. speed moving", (t) => fmtSpeed(t.speedMoving)],
      ["Avg. speed incl. breaks", (t) => fmtSpeed(t.speedElapsed)],
      ["Avg. distance per track", (t) => (t.count ? fmtKm(t.km / t.count) : "–")],
    ];
    const tots = ys.map((y) => totals(inYear(y)));
    const table = `<div class="table-wrap"><table class="data"><thead><tr><th></th>` +
      ys.map((y, i) => `<th><span class="key s${i + 1}"></span>${y}</th>`).join("") + `</tr></thead><tbody>` +
      rows.map(([label, f]) => `<tr><th>${esc(label)}</th>${tots.map((t) => `<td>${esc(f(t))}</td>`).join("")}</tr>`).join("") +
      `</tbody></table></div>`;

    return `<section class="card">
      <div class="card-head"><h2>${Y} compared with previous years</h2>
        <span class="card-sub">${esc(metric().label)}, cumulative${current ? ` · by ${esc(fmtDay(now))}` : ""}</span></div>
      <div class="legend">${headline}</div>
      <div class="chart-wrap">${cumulativeChart("years", width, series)}</div>
      ${table}
    </section>`;
  }

  function monthsSection(width) {
    const Y = state.year;
    const now = new Date();
    const groups = [];
    for (let mo = 0; mo < 12; mo++) {
      const cur = inYear(Y).filter((a) => a.date.getMonth() === mo);
      const prev = inYear(Y - 1).filter((a) => a.date.getMonth() === mo);
      const v = sum(cur), pv = sum(prev);
      const future = Y === now.getFullYear() && mo > now.getMonth();
      groups.push({
        label: monthName(mo),
        key: mo,
        active: mo === state.month,
        bars: [{ value: pv, cls: "s2" }, { value: v, cls: "s1" }],
        tip: `<b>${esc(monthName(mo, "long"))}</b>` +
          `<div class="tip-row"><span class="key s1"></span>${Y}: ${future ? "–" : esc(metric().fmt(v))} (${cur.length} tracks) ${future ? "" : delta(v, pv)}</div>` +
          `<div class="tip-row"><span class="key s2"></span>${Y - 1}: ${esc(metric().fmt(pv))} (${prev.length} tracks)</div>` +
          `<div class="tip-row muted">Click to show its weeks</div>`,
      });
    }
    return `<section class="card">
      <div class="card-head"><h2>Months of ${Y}</h2><span class="card-sub">${esc(metric().label)}</span></div>
      <div class="legend"><span class="cmp"><span class="key s1"></span>${Y}</span><span class="cmp"><span class="key s2"></span>${Y - 1}</span></div>
      <div class="chart-wrap">${barChart(width, groups, { clickable: true })}</div>
    </section>`;
  }

  function weeksSection(width) {
    const Y = state.year, mo = state.month;
    const first = new Date(Y, mo, 1), last = new Date(Y, mo + 1, 0);
    let ws = addDays(first, -((first.getDay() + 6) % 7));
    const groups = [];
    while (ws <= last) {
      const we = addDays(ws, 6);
      const list = [];
      for (let d = ws; d <= we; d = addDays(d, 1)) list.push(...(byDay.get(dayKey(d)) || []));
      const v = sum(list);
      const range = ws.getMonth() === we.getMonth()
        ? `${ws.getDate()}–${fmtDay(we)}` : `${fmtDay(ws)} – ${fmtDay(we)}`;
      const t = totals(list);
      groups.push({
        label: `Week ${isoWeek(ws)}`,
        sub: range,
        bars: [{ value: v, cls: "s1" }],
        tip: `<b>Week ${isoWeek(ws)}</b> · ${esc(range)}<div>${esc(metric().fmt(v))}</div>` +
          `<div class="tip-row muted">${t.count} track(s) · ${esc(fmtKm(t.km))} · ${esc(fmtHours(t.moving / 3600))}</div>`,
      });
      ws = addDays(ws, 7);
    }
    const monthTotal = totals(acts.filter((a) => a.date.getFullYear() === Y && a.date.getMonth() === mo));
    return `<section class="card">
      <div class="card-head"><h2>Weeks of ${esc(monthName(mo, "long"))} ${Y}</h2>
        <span class="card-sub">${esc(metric().label)} · Monday to Sunday</span></div>
      <div class="summary">${monthTotal.count} track(s) · ${esc(fmtKm(monthTotal.km))} · ${esc(fmtHours(monthTotal.moving / 3600))} moving · ↑ ${esc(num(monthTotal.ascent))} m</div>
      <div class="chart-wrap">${barChart(width, groups, { height: 200 })}</div>
    </section>`;
  }

  function calendarSection(width) {
    const Y = state.year;
    let detail = "";
    const list = state.day && byDay.get(state.day);
    if (list && state.day.startsWith(String(Y))) {
      detail = `<div class="day-detail"><b>${esc(fmtDate(new Date(state.day + "T00:00")))}</b><ul>` +
        list.map((a) => `<li><button class="link" data-track="${esc(a.t.id)}">${esc(a.t.name)}</button> · ` +
          `${esc(fmtKm(a.km))} · ${esc(fmtHours(a.movingH))} · ↑ ${esc(num(a.ascent))} m</li>`).join("") + `</ul></div>`;
    }
    const active = new Set(inYear(Y).map((a) => a.key)).size;
    return `<section class="card">
      <div class="card-head"><h2>Activity calendar ${Y}</h2><span class="card-sub">${esc(metric().label)} per day · ${active} active day(s)</span></div>
      <div class="heat-wrap">${heatmap(Y, width)}</div>
      <div class="heat-legend">Less <span class="cell-key l0"></span><span class="cell-key l1"></span><span class="cell-key l2"></span>` +
      `<span class="cell-key l3"></span><span class="cell-key l4"></span> More<span class="hint">Click a day to list its tracks</span></div>
      ${detail}
    </section>`;
  }

  function weekdaySection(width) {
    const Y = state.year;
    const list = inYear(Y);
    const groups = [...Array(7)].map((_, i) => {
      const day = list.filter((a) => (a.date.getDay() + 6) % 7 === i);
      const v = sum(day);
      return {
        label: weekdayName(i),
        bars: [{ value: v, cls: "s1" }],
        tip: `<b>${esc(weekdayName(i, "long"))}</b><div>${esc(metric().fmt(v))}</div><div class="tip-row muted">${day.length} track(s) in ${Y}</div>`,
      };
    });
    const hours = [...Array(24)].map((_, h) => {
      const at = list.filter((a) => a.date.getHours() === h);
      const v = sum(at);
      return {
        label: String(h),
        bars: [{ value: v, cls: "s1" }],
        tip: `<b>${h}:00–${h}:59</b><div>${esc(metric().fmt(v))}</div><div class="tip-row muted">${at.length} track(s) started</div>`,
      };
    });
    const half = width >= 760 ? (width - 24) / 2 : width;
    return `<section class="card">
      <div class="card-head"><h2>When you are out</h2><span class="card-sub">${esc(metric().label)} in ${Y}</span></div>
      <div class="split">
        <div><h3>By weekday</h3><div class="chart-wrap">${barChart(half, groups, { height: 180 })}</div></div>
        <div><h3>By start time</h3><div class="chart-wrap">${barChart(half, hours, { height: 180 })}</div></div>
      </div>
    </section>`;
  }

  // ---------- Rendering ----------
  function render() {
    const root = $("stats-content");
    if (!loaded) { root.innerHTML = `<p class="note">Loading…</p>`; return; }
    dirty = false;
    tips = [];
    lineCharts = {};

    const ys = years();
    if (!ys.includes(state.year)) state.year = ys[0];
    $("stats-year").innerHTML = ys.map((y) => `<option${y === state.year ? " selected" : ""}>${y}</option>`).join("");
    for (const b of document.querySelectorAll("#stats-metric button")) {
      b.setAttribute("aria-pressed", String(b.dataset.metric === state.metric));
    }

    if (!acts.length) {
      root.innerHTML = `<section class="card"><p class="note">No GPX tracks with timestamps found.` +
        `${untimed ? ` ${untimed} track(s) without timestamps are not included.` : ""}</p></section>`;
      return;
    }
    // inner width of a card: card padding is 20px on each side
    const width = Math.max(260, Math.min(root.clientWidth, 1100) - 40);
    if (!discovery) loadDiscovery();
    root.innerHTML = lifetimeSection() + yearSection(width) + monthsSection(width) +
      weeksSection(width) + calendarSection(width) + weekdaySection(width);
  }

  // ---------- Tooltip ----------
  const tipEl = () => $("stats-tip");
  function showTip(html, x, y) {
    const el = tipEl();
    el.innerHTML = html;
    el.hidden = false;
    const r = el.getBoundingClientRect();
    let left = x + 14, top = y + 14;
    if (left + r.width > window.innerWidth - 8) left = x - r.width - 14;
    if (top + r.height > window.innerHeight - 8) top = y - r.height - 14;
    el.style.left = Math.max(8, left) + "px";
    el.style.top = Math.max(8, top) + "px";
  }
  function hideTip() {
    tipEl().hidden = true;
    for (const c of document.querySelectorAll("#stats .crosshair")) c.setAttribute("visibility", "hidden");
  }

  function onPointer(e) {
    const line = e.target.closest?.(".hit-line");
    if (line) {
      const c = lineCharts[line.dataset.chart];
      const svg = line.ownerSVGElement;
      const px = e.clientX - svg.getBoundingClientRect().left;
      const i = Math.max(0, Math.min(365, Math.round(((px - c.m.left) / c.iw) * 365)));
      const cross = svg.querySelector(".crosshair");
      cross.setAttribute("x1", c.x(i));
      cross.setAttribute("x2", c.x(i));
      cross.setAttribute("visibility", "visible");
      const date = new Date(c.series[0].year, 0, 1 + i);
      showTip(`<b>By ${esc(fmtDay(date))}</b>` + c.series.map((s) =>
        `<div class="tip-row"><span class="key ${s.cls}"></span>${s.year}: ${i <= s.upto ? esc(metric().fmt(s.values[i])) : "–"}</div>`).join(""),
      e.clientX, e.clientY);
      return;
    }
    const t = e.target.closest?.("[data-tip]");
    if (t) showTip(tips[t.dataset.tip], e.clientX, e.clientY);
    else hideTip();
  }

  function onClick(e) {
    const bar = e.target.closest("[data-key]");
    if (bar) { state.month = Number(bar.dataset.key); render(); return; }
    const day = e.target.closest("[data-day]");
    if (day) { state.day = day.dataset.day; render(); return; }
    const one = e.target.closest("[data-track]");
    if (one) { openMap([one.dataset.track]); return; }
    const many = e.target.closest("[data-tracks]");
    if (many) openMap(many.dataset.tracks.split("\n"));
  }

  function openMap(ids) {
    location.hash = "map";
    showTracks(ids);
  }

  // ---------- View switching ----------
  const isVisible = () => !$("stats").hidden;

  function applyHash() {
    const stats = location.hash === "#stats";
    $("stats").hidden = !stats;
    hideTip();
    if (stats && dirty) render();
  }

  function init(opts) {
    showTracks = opts.showTracks;
    const root = $("stats");
    root.addEventListener("pointermove", onPointer);
    root.addEventListener("pointerdown", onPointer);
    root.addEventListener("pointerleave", hideTip);
    root.addEventListener("scroll", hideTip, { passive: true });
    root.addEventListener("click", onClick);
    $("stats-year").addEventListener("change", (e) => {
      state.year = Number(e.target.value);
      const now = new Date();
      state.month = state.year === now.getFullYear() ? now.getMonth() : 11;
      render();
    });
    $("stats-metric").addEventListener("click", (e) => {
      const b = e.target.closest("button");
      if (!b) return;
      state.metric = b.dataset.metric;
      render();
    });
    let resizeTimer;
    window.addEventListener("resize", () => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => { dirty = true; if (isVisible()) render(); }, 150);
    });
    window.addEventListener("hashchange", applyHash);
    applyHash();
  }

  return { init, setData };
})();

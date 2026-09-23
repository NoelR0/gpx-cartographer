// Gallery view: all photos (with and without a position) as a thumbnail grid,
// newest first and grouped by day. Clicking a photo opens the shared lightbox.
window.GPXGallery = (() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const thumbUrl = (p) => `api/photo/thumb?path=${encodeURIComponent(p.id)}`;
  const ts = (p) => (p.taken ? Date.parse(p.taken) : 0);
  const pad2 = (n) => String(n).padStart(2, "0");
  const dayKey = (d) => `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;

  const FILTERS = {
    all: () => true,
    located: (p) => p.lat != null,
    unlocated: (p) => p.lat == null,
  };

  let all = [];            // all photos, newest first
  let shown = [];          // photos currently in the grid, in display order
  let filter = "all";
  let dirty = true;
  let openLightbox = () => {};

  function setData(data) {
    all = [...data.photos].sort((a, b) => ts(b) - ts(a) || a.id.localeCompare(b.id));
    dirty = true;
    if (isVisible()) render();
  }

  function render() {
    dirty = false;
    for (const b of $("gallery-filter").querySelectorAll("button")) {
      b.setAttribute("aria-pressed", String(b.dataset.filter === filter));
    }
    shown = all.filter(FILTERS[filter]);
    $("gallery-count").textContent = `${shown.length.toLocaleString()} of ${all.length.toLocaleString()} photos`;

    const root = $("gallery-content");
    if (!shown.length) {
      root.innerHTML = `<p class="gallery-empty">${all.length ? "No photos match this filter." : "No photos found."}</p>`;
      return;
    }

    // group consecutive photos by day (the list is already sorted)
    const groups = [];
    shown.forEach((p, i) => {
      const key = p.taken ? dayKey(new Date(p.taken)) : "";
      let g = groups[groups.length - 1];
      if (!g || g.key !== key) groups.push(g = { key, date: p.taken ? new Date(p.taken) : null, items: [] });
      g.items.push(i);
    });

    let html = "";
    for (const g of groups) {
      const title = g.date
        ? g.date.toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "long", year: "numeric" })
        : "Unknown date";
      html += `<section class="gallery-day"><h2>${esc(title)} <span>${g.items.length}</span></h2><div class="gallery-grid">`;
      for (const i of g.items) {
        const p = shown[i];
        const cls = p.lat == null ? " no-pos" : "";
        html += `<button class="gallery-item${cls}" data-i="${i}" title="${esc(p.name)}">` +
          `<img src="${thumbUrl(p)}" loading="lazy" decoding="async" alt="${esc(p.name)}"></button>`;
      }
      html += "</div></section>";
    }
    root.innerHTML = html;
  }

  const isVisible = () => !$("gallery").hidden;

  function applyHash() {
    $("gallery").hidden = location.hash !== "#gallery";
    if (isVisible() && dirty) render();
  }

  function init(opts) {
    openLightbox = opts.openLightbox;
    $("gallery-content").addEventListener("click", (e) => {
      const item = e.target.closest("[data-i]");
      if (item) openLightbox(shown, Number(item.dataset.i));
    });
    $("gallery-filter").addEventListener("click", (e) => {
      const b = e.target.closest("button");
      if (!b || b.dataset.filter === filter) return;
      filter = b.dataset.filter;
      render();
    });
    window.addEventListener("hashchange", applyHash);
    applyHash();
  }

  return { init, setData };
})();

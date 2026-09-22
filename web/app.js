(() => {
  "use strict";

  const TRACK_COLORS = ["#e6194b", "#3cb44b", "#4363d8", "#f58231", "#911eb4",
    "#008080", "#f032e6", "#9a6324", "#800000", "#000075", "#808000", "#46a0e6"];
  const MARKER_SIZE = 52;
  // tracks within this many pixels of a click count as "under the cursor"
  const TRACK_HIT_PX = window.matchMedia("(pointer: coarse)").matches ? 14 : 8;

  const $ = (id) => document.getElementById(id);
  const thumbUrl = (p) => `api/photo/thumb?path=${encodeURIComponent(p.id)}`;
  const fullUrl = (p) => `api/photo/full?path=${encodeURIComponent(p.id)}`;
  const originalUrl = (p) => `api/photo/original?path=${encodeURIComponent(p.id)}`;

  const fmtDate = (iso, withTime = true) => {
    if (!iso) return "";
    const d = new Date(iso);
    return withTime
      ? d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })
      : d.toLocaleDateString(undefined, { dateStyle: "medium" });
  };
  const fmtKm = (m) => (m >= 10000 ? (m / 1000).toFixed(0) : (m / 1000).toFixed(1)) + " km";
  const fmtDuration = (s) => {
    if (s == null) return "";
    const h = Math.floor(s / 3600), m = Math.round((s % 3600) / 60);
    return h ? `${h} h ${String(m).padStart(2, "0")} min` : `${m} min`;
  };
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

  // ---------- Map ----------
  // maxZoom must be set before the photo layer (markercluster) is added
  const map = L.map("map", { zoomControl: false, maxZoom: 19 }).setView([46.8, 8.2], 8);
  L.control.zoom({ position: "topright" }).addTo(map);
  L.control.scale({ imperial: false, position: "bottomright" }).addTo(map);

  const photoLayer = L.markerClusterGroup({
    maxClusterRadius: 70,
    showCoverageOnHover: false,
    spiderfyOnMaxZoom: false,
    zoomToBoundsOnClick: false,
    iconCreateFunction: (cluster) => {
      const markers = cluster.getAllChildMarkers();
      // newest photo as the cover of the stack
      const cover = markers.reduce((a, b) => (ts(b.photo) > ts(a.photo) ? b : a));
      return L.divIcon({
        className: "photo-cluster",
        iconSize: [MARKER_SIZE, MARKER_SIZE],
        html: `<div class="photo-marker"><img src="${thumbUrl(cover.photo)}" loading="lazy" alt="">` +
              `<span class="count">${markers.length}</span></div>`,
      });
    },
  });
  const trackLayer = L.featureGroup();

  photoLayer.on("clusterclick", (e) => {
    const cluster = e.layer;
    const photos = cluster.getAllChildMarkers().map((m) => m.photo);
    const bounds = cluster.getBounds();
    const canZoom = map.getBoundsZoom(bounds) > map.getZoom() && map.getZoom() < map.getMaxZoom();
    // if all photos are at (almost) the same spot, zooming won't help -> open them directly
    const tiny = bounds.getNorthEast().distanceTo(bounds.getSouthWest()) < 15;
    if (!canZoom || tiny) openLightbox(sortByTime(photos), 0);
    else map.fitBounds(bounds, { padding: [40, 40] });
  });

  // ---------- State ----------
  let photos = [];        // all photos with a position
  let unlocated = 0;
  let tracks = [];        // {data, line, color, li}
  let activeTrack = null;

  const ts = (p) => (p.taken ? Date.parse(p.taken) : 0);
  const sortByTime = (list) => [...list].sort((a, b) => ts(a) - ts(b));

  // ---------- Loading data ----------
  async function loadConfig() {
    try {
      const cfg = await fetch("api/config").then((r) => r.json());
      $("version").textContent = `GPX Cartographer ${cfg.version}`;
      map.setMaxZoom(cfg.tile_max_zoom);
      L.tileLayer(cfg.tile_url, { maxZoom: cfg.tile_max_zoom, attribution: cfg.tile_attribution }).addTo(map);
    } catch (err) {
      $("status").innerHTML = `<span class="warn">Could not load configuration: ${esc(err.message)}</span>`;
    }
  }

  async function loadData(fit) {
    const btn = $("reload");
    btn.classList.add("spinning");
    try {
      const res = await fetch("api/data", { cache: "no-store" });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      render(await res.json(), fit);
    } catch (err) {
      console.error(err);
      $("status").innerHTML = `<span class="warn">Error while loading: ${esc(err.message)}</span>`;
    } finally {
      btn.classList.remove("spinning");
    }
  }

  function render(data, fit) {
    // Photos
    photoLayer.clearLayers();
    photos = data.photos.filter((p) => p.lat != null && p.lon != null);
    unlocated = data.photos.length - photos.length;
    photoLayer.addLayers(photos.map(makePhotoMarker));

    // Tracks
    trackLayer.clearLayers();
    activeTrack = null;
    tracks = data.tracks.map((t, i) => {
      const color = TRACK_COLORS[i % TRACK_COLORS.length];
      const line = L.polyline(t.segments, { color, weight: 4, opacity: 0.85 });
      const entry = { data: t, line, color, li: null };
      line.on("click", (e) => { L.DomEvent.stopPropagation(e); onTrackClick(entry, e.latlng); });
      line.on("mouseover", () => line.setStyle({ weight: 7 }));
      line.on("mouseout", () => line.setStyle({ weight: entry === activeTrack ? 7 : 4 }));
      trackLayer.addLayer(line);
      return entry;
    });
    renderTrackList();

    // Status
    $("count-photos").textContent = photos.length;
    $("count-tracks").textContent = tracks.length;
    const notes = [];
    if (!data.dirs.photos) notes.push('<span class="warn">Photo directory not found</span>');
    if (!data.dirs.gpx) notes.push('<span class="warn">GPX directory not found</span>');
    const fromTrack = photos.filter((p) => p.located_by === "track").length;
    if (fromTrack) notes.push(`${fromTrack} photo(s) located via GPX timestamps`);
    if (unlocated) notes.push(`${unlocated} photo(s) without a position`);
    $("status").innerHTML = notes.join("<br>") || `Updated ${new Date().toLocaleTimeString()}`;

    if (fit) fitAll();
  }

  function makePhotoMarker(p) {
    const m = L.marker([p.lat, p.lon], {
      icon: L.divIcon({
        className: "",
        iconSize: [MARKER_SIZE, MARKER_SIZE],
        html: `<div class="photo-marker${p.located_by === "track" ? " from-track" : ""}">` +
              `<img src="${thumbUrl(p)}" loading="lazy" alt=""></div>`,
      }),
      title: p.name,
    });
    m.photo = p;
    m.on("click", () => {
      const all = sortByTime(photos);
      openLightbox(all, all.indexOf(p));
    });
    return m;
  }

  function renderTrackList() {
    const ul = $("track-list");
    ul.innerHTML = "";
    if (!tracks.length) {
      ul.innerHTML = '<li class="empty">No GPX tracks found.</li>';
      return;
    }
    for (const entry of tracks) {
      const t = entry.data;
      const li = document.createElement("li");
      const sub = [fmtDate(t.start, false), fmtKm(t.distance_m), t.ascent_m ? `↑ ${t.ascent_m} m` : ""]
        .filter(Boolean).join(" · ");
      li.innerHTML = `<span class="swatch" style="background:${entry.color}"></span>` +
        `<span class="name" title="${esc(t.file)}">${esc(t.name)}</span><span class="sub">${esc(sub)}</span>`;
      li.addEventListener("click", () => selectTrack(entry, true));
      entry.li = li;
      ul.appendChild(li);
    }
  }

  function photosOfTrack(t) {
    if (!t.start || !t.end) return [];
    const s = Date.parse(t.start), e = Date.parse(t.end);
    return sortByTime(photos.filter((p) => {
      if (!p.taken) return false;
      const x = Date.parse(p.taken);
      return x >= s && x <= e;
    }));
  }

  // all tracks passing within TRACK_HIT_PX of latlng, oldest first
  function tracksAt(latlng) {
    const pt = map.latLngToLayerPoint(latlng);
    return tracks
      .filter((entry) => {
        // cheap bounding-box check before walking all points
        const b = entry.line.getBounds();
        const sw = map.latLngToLayerPoint(b.getSouthWest()), ne = map.latLngToLayerPoint(b.getNorthEast());
        if (pt.x < sw.x - TRACK_HIT_PX || pt.x > ne.x + TRACK_HIT_PX ||
            pt.y > sw.y + TRACK_HIT_PX || pt.y < ne.y - TRACK_HIT_PX) return false;
        const closest = entry.line.closestLayerPoint(pt);
        return closest && closest.distance <= TRACK_HIT_PX;
      })
      .sort((a, b) => (a.data.start ? Date.parse(a.data.start) : 0) - (b.data.start ? Date.parse(b.data.start) : 0));
  }

  function onTrackClick(entry, latlng) {
    const hits = tracksAt(latlng);
    if (!hits.includes(entry)) hits.push(entry);
    if (hits.length === 1) selectTrack(entry, false, latlng);
    else showTrackChooser(hits, latlng);
  }

  // menu for picking one of several overlapping tracks
  function showTrackChooser(entries, latlng) {
    const div = document.createElement("div");
    div.className = "track-chooser";
    div.innerHTML = `<h3>${entries.length} tracks here</h3>`;
    const ul = document.createElement("ul");
    for (const entry of entries) {
      const t = entry.data;
      const li = document.createElement("li");
      const sub = [t.start ? fmtDate(t.start) : "No date", fmtKm(t.distance_m)].join(" · ");
      li.innerHTML = `<span class="swatch" style="background:${entry.color}"></span>` +
        `<span class="name" title="${esc(t.file)}">${esc(t.name)}</span><span class="sub">${esc(sub)}</span>`;
      li.addEventListener("mouseenter", () => entry.line.setStyle({ weight: 7 }).bringToFront());
      li.addEventListener("mouseleave", () => entry.line.setStyle({ weight: entry === activeTrack ? 7 : 4 }));
      li.addEventListener("click", () => selectTrack(entry, false, latlng));
      ul.appendChild(li);
    }
    div.appendChild(ul);
    L.popup({ maxWidth: 280, minWidth: 220 }).setLatLng(latlng).setContent(div).openOn(map);
  }

  function selectTrack(entry, fit, latlng) {
    if (!map.hasLayer(trackLayer)) {
      $("show-tracks").checked = true;
      map.addLayer(trackLayer);
    }
    if (activeTrack && activeTrack !== entry) {
      activeTrack.line.setStyle({ weight: 4, opacity: 0.85 });
      activeTrack.li?.classList.remove("active");
    }
    activeTrack = entry;
    entry.line.setStyle({ weight: 7, opacity: 1 }).bringToFront();
    entry.li?.classList.add("active");
    entry.li?.scrollIntoView({ block: "nearest" });
    if (fit) map.fitBounds(entry.line.getBounds(), { padding: [40, 40] });

    const t = entry.data;
    const inTrack = photosOfTrack(t);
    const stats = [
      t.start ? fmtDate(t.start) : null,
      `${fmtKm(t.distance_m)}${t.duration_s ? " · " + fmtDuration(t.duration_s) : ""}`,
      t.ascent_m || t.descent_m ? `↑ ${t.ascent_m} m · ↓ ${t.descent_m} m` : null,
    ].filter(Boolean).join("<br>");
    const div = document.createElement("div");
    div.className = "track-popup";
    div.innerHTML = `<h3>${esc(t.name)}</h3><div class="stats">${stats}</div><div class="actions">` +
      (inTrack.length ? `<a data-act="photos">View ${inTrack.length} photo(s)</a>` : "") +
      `<a href="api/track/download?file=${encodeURIComponent(t.file)}" download>Download GPX</a></div>`;
    div.querySelector('[data-act="photos"]')?.addEventListener("click", () => openLightbox(inTrack, 0));
    const pos = latlng || entry.line.getBounds().getCenter();
    L.popup({ maxWidth: 260 }).setLatLng(pos).setContent(div).openOn(map);
  }

  map.on("click", () => {
    if (!activeTrack) return;
    activeTrack.line.setStyle({ weight: 4, opacity: 0.85 });
    activeTrack.li?.classList.remove("active");
    activeTrack = null;
  });

  function fitAll() {
    const b = L.latLngBounds([]);
    if (map.hasLayer(photoLayer)) photos.forEach((p) => b.extend([p.lat, p.lon]));
    if (map.hasLayer(trackLayer) && trackLayer.getLayers().length) b.extend(trackLayer.getBounds());
    if (b.isValid()) map.fitBounds(b, { padding: [40, 40], maxZoom: 15 });
  }

  // ---------- Lightbox ----------
  const lb = { list: [], index: 0 };

  function openLightbox(list, index) {
    if (!list.length) return;
    lb.list = list;
    lb.index = Math.max(0, index);
    const strip = $("lb-strip");
    strip.innerHTML = "";
    strip.hidden = list.length < 2;
    list.forEach((p, i) => {
      const img = document.createElement("img");
      img.loading = "lazy";
      img.src = thumbUrl(p);
      img.alt = p.name;
      img.addEventListener("click", () => showPhoto(i));
      strip.appendChild(img);
    });
    $("lightbox").hidden = false;
    showPhoto(lb.index);
  }

  function showPhoto(i) {
    if (i < 0 || i >= lb.list.length) return;
    lb.index = i;
    const p = lb.list[i];
    const img = $("lb-img");
    img.src = thumbUrl(p);       // show something right away …
    const full = new Image();    // … then load the large image
    full.onload = () => { if (lb.list[lb.index] === p) img.src = full.src; };
    full.src = fullUrl(p);
    img.alt = p.name;

    $("lb-title").textContent = p.name;
    const meta = [fmtDate(p.taken), `${i + 1} / ${lb.list.length}`];
    if (p.located_by === "track") meta.push("Position from GPX track");
    $("lb-meta").textContent = meta.filter(Boolean).join(" · ");
    $("lb-download").href = originalUrl(p);
    $("lb-prev").disabled = i === 0;
    $("lb-next").disabled = i === lb.list.length - 1;

    const strip = $("lb-strip");
    strip.querySelector(".current")?.classList.remove("current");
    const cur = strip.children[i];
    if (cur) {
      cur.classList.add("current");
      cur.scrollIntoView({ inline: "center", block: "nearest", behavior: "smooth" });
    }
    // preload neighbors
    [i - 1, i + 1].forEach((j) => { if (lb.list[j]) new Image().src = fullUrl(lb.list[j]); });
  }

  function closeLightbox() {
    $("lightbox").hidden = true;
    $("lb-img").src = "";
  }

  function locateCurrent() {
    const p = lb.list[lb.index];
    closeLightbox();
    if (!p) return;
    const marker = photoLayer.getLayers().find((m) => m.photo === p);
    if (marker) photoLayer.zoomToShowLayer(marker);
    else map.setView([p.lat, p.lon], 17);
  }

  $("lb-close").addEventListener("click", closeLightbox);
  $("lb-prev").addEventListener("click", () => showPhoto(lb.index - 1));
  $("lb-next").addEventListener("click", () => showPhoto(lb.index + 1));
  $("lb-locate").addEventListener("click", locateCurrent);
  $("lightbox").addEventListener("click", (e) => {
    if (e.target.classList.contains("lb-stage")) closeLightbox();
  });
  document.addEventListener("keydown", (e) => {
    if ($("lightbox").hidden) return;
    if (e.key === "Escape") closeLightbox();
    else if (e.key === "ArrowLeft") showPhoto(lb.index - 1);
    else if (e.key === "ArrowRight") showPhoto(lb.index + 1);
  });
  // swiping on touch devices
  let touchX = null;
  $("lb-img").addEventListener("touchstart", (e) => { touchX = e.touches[0].clientX; }, { passive: true });
  $("lb-img").addEventListener("touchend", (e) => {
    if (touchX == null) return;
    const dx = e.changedTouches[0].clientX - touchX;
    touchX = null;
    if (Math.abs(dx) > 50) showPhoto(lb.index + (dx < 0 ? 1 : -1));
  });

  // ---------- Sidebar ----------
  $("show-photos").addEventListener("change", (e) =>
    e.target.checked ? map.addLayer(photoLayer) : map.removeLayer(photoLayer));
  $("show-tracks").addEventListener("change", (e) =>
    e.target.checked ? map.addLayer(trackLayer) : map.removeLayer(trackLayer));
  $("fit-all").addEventListener("click", fitAll);
  $("reload").addEventListener("click", () => loadData(false));
  $("collapse").addEventListener("click", () => $("panel").classList.add("collapsed"));
  $("expand").addEventListener("click", () => $("panel").classList.remove("collapsed"));
  if (window.matchMedia("(max-width: 640px)").matches) $("panel").classList.add("collapsed");

  map.addLayer(trackLayer);
  map.addLayer(photoLayer);
  loadConfig().then(() => loadData(true));
})();

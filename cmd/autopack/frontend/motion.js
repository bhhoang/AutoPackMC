// AutoPack motion and glass material.
//
// This is a web approximation of a "liquid glass" material, not Apple's
// Liquid Glass (which exists only on Apple platforms). It uses what the
// WebView2 (Chromium) engine offers:
//  - a refracting lens: backdrop-filter with an SVG displacement map made for
//    each lens size, bending the backdrop most at the rim like thick glass,
//    with the red, green and blue channels shifted slightly apart
//    (dispersion);
//  - springs and a stretch while a lens travels, so it moves like a drop;
//  - a specular highlight that follows the pointer across cards.
//
// Every duration is multiplied by the animation scale chosen in Settings
// (0 turns motion off). CSS reads the same scale from --anim.
(() => {
  const root = document.documentElement;
  const SVGNS = 'http://www.w3.org/2000/svg';
  let scale = 1;

  const osReduced = () => matchMedia('(prefers-reduced-motion: reduce)').matches;

  // setting: "" follows Windows, otherwise a number such as "0", "1" or "4".
  function setScale(setting){
    const n = setting === '' || setting == null ? (osReduced() ? 0 : 1) : parseFloat(setting);
    scale = Number.isFinite(n) && n >= 0 ? n : 1;
    root.style.setProperty('--anim', String(scale));
    root.dataset.motion = scale === 0 ? 'off' : 'on';
  }
  const ms = d => d * scale;
  const on = () => scale > 0 && typeof Element.prototype.animate === 'function';

  // ------------------------------------------------------------ refraction
  // One hidden <svg> holds a filter per lens size.
  const defs = document.createElementNS(SVGNS, 'svg');
  defs.setAttribute('width', '0'); defs.setAttribute('height', '0');
  defs.setAttribute('aria-hidden', 'true');
  defs.style.position = 'absolute';
  document.body.appendChild(defs);
  const filters = new Map(); // "w×h×r" -> filter id

  // A displacement map for a rounded rectangle: red and green hold the x and
  // y offset (128 = none). Offsets point inward and grow toward the rim, so
  // the backdrop near the edge is pulled in and bent, like a glass bead.
  function displacementMap(w, h, r, bezel){
    const c = document.createElement('canvas');
    c.width = w; c.height = h;
    const g = c.getContext('2d');
    const img = g.createImageData(w, h);
    const cx = w / 2, cy = h / 2;
    const ix = Math.max(0, cx - r), iy = Math.max(0, cy - r); // inner rect half-size
    for (let y = 0; y < h; y++){
      for (let x = 0; x < w; x++){
        const px = x + .5 - cx, py = y + .5 - cy;
        // Closest point on the inner rect; the rim runs r around it.
        const qx = Math.max(-ix, Math.min(ix, px)), qy = Math.max(-iy, Math.min(iy, py));
        let nx = px - qx, ny = py - qy;
        const len = Math.hypot(nx, ny);
        const depth = r - len;                       // distance inside the rim
        let dx = 0, dy = 0;
        if (len > 0 && depth >= 0 && depth < bezel){
          // The slope of a rounded glass edge: steepest at the rim, flat inside.
          const t = 1 - depth / bezel;
          const k = t * t * (3 - 2 * t);
          nx /= len; ny /= len;
          dx = -nx * k; dy = -ny * k;                // pull inward
        }
        const i = (y * w + x) * 4;
        img.data[i] = 128 + dx * 127;
        img.data[i + 1] = 128 + dy * 127;
        img.data[i + 2] = 128;
        img.data[i + 3] = 255;
      }
    }
    g.putImageData(img, 0, 0);
    return c.toDataURL();
  }

  function lensFilter(w, h, r){
    w = Math.max(8, Math.round(w)); h = Math.max(8, Math.round(h)); r = Math.min(Math.round(r), Math.floor(h / 2));
    const key = `${w}x${h}x${r}`;
    if (filters.has(key)) return filters.get(key);
    const id = `lens-${filters.size}`;
    const map = displacementMap(w, h, r, Math.min(r, 11));
    const f = document.createElementNS(SVGNS, 'filter');
    f.setAttribute('id', id);
    f.setAttribute('x', '0'); f.setAttribute('y', '0');
    f.setAttribute('width', String(w)); f.setAttribute('height', String(h));
    f.setAttribute('filterUnits', 'userSpaceOnUse');
    f.setAttribute('color-interpolation-filters', 'sRGB');
    // Three slightly different offsets, one per colour channel, then merged.
    const strength = Math.min(12, h * .3);
    f.innerHTML = `
      <feImage href="${map}" x="0" y="0" width="${w}" height="${h}" result="map" preserveAspectRatio="none"/>
      <feDisplacementMap in="SourceGraphic" in2="map" scale="${strength * .96}" xChannelSelector="R" yChannelSelector="G" result="dr"/>
      <feDisplacementMap in="SourceGraphic" in2="map" scale="${strength}" xChannelSelector="R" yChannelSelector="G" result="dg"/>
      <feDisplacementMap in="SourceGraphic" in2="map" scale="${strength * 1.04}" xChannelSelector="R" yChannelSelector="G" result="db"/>
      <feColorMatrix in="dr" type="matrix" values="1 0 0 0 0  0 0 0 0 0  0 0 0 0 0  0 0 0 1 0" result="r"/>
      <feColorMatrix in="dg" type="matrix" values="0 0 0 0 0  0 1 0 0 0  0 0 0 0 0  0 0 0 1 0" result="g"/>
      <feColorMatrix in="db" type="matrix" values="0 0 0 0 0  0 0 0 0 0  0 0 1 0 0  0 0 0 1 0" result="b"/>
      <feBlend in="r" in2="g" mode="screen" result="rg"/>
      <feBlend in="rg" in2="b" mode="screen"/>`;
    defs.appendChild(f);
    filters.set(key, id);
    return id;
  }

  // ------------------------------------------------------------ liquid lens
  // A glass lens that sits under the active item of a group (tabs,
  // segmented controls, the server list) and flows to the next one.
  const lenses = [];

  function attach(container, itemSelector, isActive){
    if (!container || container.querySelector(':scope > .lens')) return;
    const lens = document.createElement('span');
    lens.className = 'lens';
    lens.setAttribute('aria-hidden', 'true');
    container.classList.add('has-lens');
    container.prepend(lens);
    const L = {container, lens, itemSelector, isActive, rect: null};
    lenses.push(L);
    new ResizeObserver(() => place(L, false)).observe(container);
    place(L, false);
    return L;
  }

  function place(L, animate){
    const {container, lens} = L;
    const item = [...container.querySelectorAll(L.itemSelector)].find(L.isActive);
    if (!item || item.offsetParent === null){ lens.style.opacity = '0'; L.rect = null; return; }
    const r = {x: item.offsetLeft, y: item.offsetTop, w: item.offsetWidth, h: item.offsetHeight};
    const radius = parseFloat(getComputedStyle(item).borderTopLeftRadius) || 12;
    const prev = L.rect;
    L.rect = r;
    lens.style.opacity = '1';
    lens.style.width = r.w + 'px';
    lens.style.height = r.h + 'px';
    lens.style.borderRadius = radius + 'px';
    lens.style.transform = `translate(${r.x}px, ${r.y}px)`;
    const id = lensFilter(r.w, r.h, radius);
    lens.style.setProperty('--lens-filter', `url(#${id})`);

    if (!animate || !prev || !on() || (prev.x === r.x && prev.y === r.y && prev.w === r.w)) return;
    // Travel with a stretch: halfway, the drop spans part of both spots and
    // thins a little; then it springs into its new shape.
    const horizontal = Math.abs(r.x - prev.x) >= Math.abs(r.y - prev.y);
    const mid = horizontal
      ? {x: Math.min(prev.x, r.x) + Math.abs(r.x - prev.x) * .18, y: r.y, w: Math.abs(r.x - prev.x) * .64 + (prev.w + r.w) / 2, h: r.h * .9}
      : {x: r.x, y: Math.min(prev.y, r.y) + Math.abs(r.y - prev.y) * .18, w: r.w * .96, h: Math.abs(r.y - prev.y) * .64 + (prev.h + r.h) / 2};
    const frame = q => ({transform: `translate(${q.x}px, ${q.y + (horizontal ? (r.h - q.h) / 2 : 0)}px)`, width: q.w + 'px', height: q.h + 'px'});
    lens.animate([frame(prev), {...frame(mid), offset: .42}, frame(r)], {
      duration: ms(520), easing: 'cubic-bezier(.25,1.25,.45,1)',
    });
  }

  function refresh(animate = true){ lenses.forEach(L => place(L, animate)); }

  // Watch groups for the attributes that mark the active item.
  new MutationObserver(records => {
    const touched = new Set();
    for (const rec of records){
      for (const L of lenses) if (L.container.contains(rec.target)) touched.add(L);
    }
    touched.forEach(L => place(L, true));
  }).observe(document.body, {subtree: true, attributes: true, attributeFilter: ['aria-selected', 'aria-pressed', 'aria-checked', 'aria-current', 'hidden'], childList: true});

  // ------------------------------------------------------------ specular light
  // The card under the pointer catches a soft light where the pointer is.
  let lit = null, pending = null, queued = false;
  addEventListener('pointermove', e => {
    pending = e;
    if (queued) return; // one update per frame
    queued = true;
    requestAnimationFrame(() => {
      queued = false;
      const ev = pending; if (!ev) return;
      pending = null;
      const card = ev.target.closest && ev.target.closest('.card, .btn, .lens-host');
      if (lit && lit !== card) lit.classList.remove('lit');
      lit = card;
      if (!card || scale === 0) return;
      const b = card.getBoundingClientRect();
      card.style.setProperty('--mx', `${ev.clientX - b.left}px`);
      card.style.setProperty('--my', `${ev.clientY - b.top}px`);
      card.classList.add('lit');
    });
  }, {passive: true});
  addEventListener('pointerleave', () => { if (lit) lit.classList.remove('lit'); lit = null; });

  // ------------------------------------------------------------ entrances
  // Items settle in: a short rise while the frost clears. They are only
  // hidden during their own delay, so the page is readable if anything
  // interrupts the animation.
  function enter(container, selector, opts = {}){
    if (!on() || !container) return;
    const items = selector ? [...container.querySelectorAll(selector)] : [container];
    const max = opts.max ?? 14;
    items.slice(0, max).forEach((el, i) => {
      if (el.offsetParent === null) return;
      el.animate([
        {opacity: 0, transform: `translateY(${opts.rise ?? 10}px) scale(${opts.from ?? .985})`, filter: 'blur(6px)'},
        {opacity: 1, transform: 'none', filter: 'blur(0)'},
      ], {duration: ms(opts.duration ?? 420), delay: ms(i * (opts.stagger ?? 34)), easing: 'cubic-bezier(.16,1,.3,1)', fill: 'backwards'});
    });
  }

  // A dialog grows out of the glass behind it.
  function pop(el){
    if (!on() || !el) return;
    el.animate([
      {opacity: 0, transform: 'translateY(8px) scale(.94)', filter: 'blur(8px)'},
      {opacity: 1, transform: 'none', filter: 'blur(0)'},
    ], {duration: ms(460), easing: 'cubic-bezier(.2,1.3,.4,1)'});
  }

  // Swap text with a quick crossfade, for status changes.
  function swap(el){
    if (!on() || !el) return;
    el.animate([{opacity: 0, transform: 'translateY(6px)', filter: 'blur(4px)'}, {opacity: 1, transform: 'none', filter: 'blur(0)'}],
      {duration: ms(360), easing: 'cubic-bezier(.16,1,.3,1)'});
  }

  matchMedia('(prefers-reduced-motion: reduce)').addEventListener('change', () => {
    if (root.dataset.motionSetting === '') setScale('');
  });

  window.Motion = {
    setScale(setting){ root.dataset.motionSetting = setting || ''; setScale(setting); },
    scale: () => scale, ms, attach, refresh, enter, pop, swap,
  };
  window.Motion.setScale('');
})();

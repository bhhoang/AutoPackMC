// Blocky Minecraft-style landscape behind the glass panels. It is drawn
// once per size or theme change, not animated.
//
// The frost on the panels is made here too. Because the landscape never
// moves, it is blurred once, with a fine grain, into an image that the
// panels show fixed to the window (--frost-layer in style.css), which looks
// like frosted glass over it. Blurring what is behind each panel live, with
// backdrop-filter, made some Windows graphics drivers flicker.
(() => {
  const cv = document.getElementById('scene'), cx = cv.getContext('2d');

  function rng(seed){
    return () => {
      seed |= 0; seed = seed + 0x6D2B79F5 | 0;
      let t = Math.imul(seed ^ seed >>> 15, 1 | seed);
      t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t;
      return ((t ^ t >>> 14) >>> 0) / 4294967296;
    };
  }

  function draw(){
    const css = getComputedStyle(document.documentElement), v = n => css.getPropertyValue(n).trim();
    const dpr = Math.min(devicePixelRatio || 1, 2), w = innerWidth, h = innerHeight;
    cv.width = w * dpr; cv.height = h * dpr; cx.setTransform(dpr, 0, 0, dpr, 0, 0);
    const g = cx.createLinearGradient(0, 0, 0, h);
    g.addColorStop(0, v('--sky-top')); g.addColorStop(1, v('--sky-bottom'));
    cx.fillStyle = g; cx.fillRect(0, 0, w, h);
    const blob = (x, y, r, c) => {
      const rg = cx.createRadialGradient(x, y, 0, x, y, r);
      rg.addColorStop(0, c); rg.addColorStop(1, 'transparent');
      cx.fillStyle = rg; cx.fillRect(0, 0, w, h);
    };
    blob(w * .12, h * .85, Math.max(w, h) * .45, v('--blob-a'));
    blob(w * .95, h * .55, Math.max(w, h) * .4, v('--blob-b'));
    // A square sun, like the game's.
    const sx = w * .8, sy = h * .2, ss = Math.max(56, w * .05);
    blob(sx, sy, ss * 3.2, v('--sun-glow'));
    cx.fillStyle = v('--sun'); cx.fillRect(sx - ss / 2, sy - ss / 2, ss, ss);
    const B = Math.max(14, Math.round(w / 80));
    const r = rng(7);
    cx.fillStyle = 'rgba(255,255,255,.2)';
    for (let i = 0; i < 6; i++){
      const x = r() * w, y = h * (.08 + r() * .25), cw = B * (4 + Math.floor(r() * 6));
      cx.fillRect(Math.round(x / B) * B, Math.round(y / B) * B, cw, B);
      cx.fillRect(Math.round(x / B) * B + B, Math.round(y / B) * B - B, cw - B * 2, B);
    }
    const layers = [[v('--hill-far'), .58, .09, 1.3], [v('--hill-mid'), .7, .07, 2.1], [v('--hill-near'), .82, .05, 3.4]];
    layers.forEach(([col, base, amp, f], li) => {
      const ph = r() * 10;
      for (let x = 0; x < w + B; x += B){
        const t = x / w;
        const n = Math.sin(t * f * Math.PI * 2 + ph) * .6 + Math.sin(t * f * 5.3 + ph * 2) * .3 + (r() - .5) * .25;
        const top = Math.round((h * base - n * h * amp) / B) * B;
        cx.fillStyle = col; cx.fillRect(x, top, B, h - top);
        cx.fillStyle = v('--grass-top'); cx.fillRect(x, top, B, Math.max(3, B * .3));
        if (li === 2 && r() < .06){ // a tree
          cx.fillStyle = col; cx.fillRect(x, top - B * 3, B, B * 3);
          cx.fillRect(x - B, top - B * 5, B * 3, B * 2); cx.fillRect(x, top - B * 6, B, B);
        }
      }
    });
    frost();
  }

  // Grain: a small tile of random grey dots, laid over the frost.
  let grainTile;
  function grain(){
    if (grainTile) return grainTile;
    const n = 128, c = document.createElement('canvas');
    c.width = c.height = n;
    const g = c.getContext('2d'), img = g.createImageData(n, n), r = rng(11);
    for (let i = 0; i < img.data.length; i += 4){
      const v = r() * 255;
      img.data[i] = img.data[i + 1] = img.data[i + 2] = v; img.data[i + 3] = 255;
    }
    g.putImageData(img, 0, 0);
    return (grainTile = c);
  }

  let frostURL = '', frostJob = 0;
  function frost(){
    const job = ++frostJob;
    // Half size is plenty for an image this blurry.
    const w = Math.max(1, Math.ceil(innerWidth / 2)), h = Math.max(1, Math.ceil(innerHeight / 2));
    const blur = 12, pad = blur * 3;
    // Copy the landscape with its edges stretched outward, so the blur does
    // not fade to dark at the sides of the window.
    const p = document.createElement('canvas');
    p.width = w + pad * 2; p.height = h + pad * 2;
    const pg = p.getContext('2d');
    pg.drawImage(cv, pad, pad, w, h);
    pg.drawImage(cv, 0, 0, 1, cv.height, 0, pad, pad, h);
    pg.drawImage(cv, cv.width - 1, 0, 1, cv.height, pad + w, pad, pad, h);
    pg.drawImage(p, 0, pad, p.width, 1, 0, 0, p.width, pad);
    pg.drawImage(p, 0, pad + h - 1, p.width, 1, 0, pad + h, p.width, pad);

    const o = document.createElement('canvas');
    o.width = w; o.height = h;
    const g = o.getContext('2d');
    g.filter = `blur(${blur}px) saturate(165%)`;
    g.drawImage(p, -pad, -pad);
    g.filter = 'none';
    g.globalCompositeOperation = 'overlay'; g.globalAlpha = .07;
    g.fillStyle = g.createPattern(grain(), 'repeat');
    g.fillRect(0, 0, w, h);

    o.toBlob(b => {
      if (!b || job !== frostJob) return;
      const url = URL.createObjectURL(b);
      document.documentElement.style.setProperty('--frost-layer', `url("${url}") 0 0 / 100vw 100vh no-repeat fixed`);
      if (frostURL) URL.revokeObjectURL(frostURL);
      frostURL = url;
    });
  }

  let rz;
  addEventListener('resize', () => { clearTimeout(rz); rz = setTimeout(draw, 120); });
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', draw);
  new MutationObserver(draw).observe(document.documentElement, {attributes: true, attributeFilter: ['data-theme']});
  (document.fonts ? document.fonts.ready : Promise.resolve()).then(draw);
  draw();
})();

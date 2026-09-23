// Maple page logic. Go methods are reached through window.go.app.Service
// (the app) and window.go.main.Window (the title bar); updates arrive as
// runtime events: setup, server, server-log, files-dropped, close-requested.
(() => {
  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => [...r.querySelectorAll(s)];
  const api = () => window.go.app.Service;
  const win = () => window.go.main.Window;
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;

  /* ------------------------------------------------ language */
  let lang = 'en';
  function t(key, vars){
    let s = (STR[lang] && STR[lang][key]) ?? STR.en[key] ?? key;
    if (vars) s = s.replace(/\{(\w+)\}/g, (m, k) => (k in vars && vars[k] != null ? vars[k] : m));
    return s;
  }
  const esc = x => String(x ?? '').replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));

  function applyStatic(){
    document.documentElement.lang = lang;
    $$('[data-i18n]').forEach(el => { el.textContent = t(el.dataset.i18n); });
    $$('[data-i18n-ph]').forEach(el => { el.placeholder = t(el.dataset.i18nPh); });
    $$('[data-i18n-aria]').forEach(el => el.setAttribute('aria-label', t(el.dataset.i18nAria)));
    $$('[data-lang-set]').forEach(b => b.setAttribute('aria-pressed', b.dataset.langSet === lang));
  }

  // Go errors arrive as "code" or "code: detail".
  function parseErr(e){
    const msg = String(e && e.message ? e.message : e ?? '');
    const m = msg.match(/^([a-z_]+)(?::\s([\s\S]*))?$/);
    if (m && STR.en['err_' + m[1]]) return {code: m[1], detail: m[2] || ''};
    return {code: 'unknown', detail: msg};
  }
  const errText = (err, vars) => t('err_' + err.code, vars);

  /* ------------------------------------------------ state */
  const S = {
    app: null,
    servers: new Map(),     // id -> ServerView
    current: null,
    view: 'welcome',
    tab: 'overview',
    filter: 'all',
    mods: [],
    logs: {},               // id -> lines
    publicIp: null,
    stopping: new Set(),    // servers being stopped from this window
    quiet: new Set(),       // servers restarting: no "stopped" toast
    form: null,
    setup: null,
    props: null,            // Server settings tab: edited values
    propsSaved: null,       // ... and the values on disk
    update: null,           // result of the last update check
    updating: false,
  };
  const srv = () => S.servers.get(S.current);
  const packVars = s => ({mc: s?.mc || '?', loader: loaderName(s?.loader)});
  const loaderName = l => ({forge:'Forge', neoforge:'NeoForge', fabric:'Fabric', quilt:'Quilt'}[String(l || '').toLowerCase()] || l || '?');

  /* ------------------------------------------------ toast */
  let toastTimer;
  function toast(msg, bad, ms = 3200){
    $('#toastMsg').textContent = msg;
    $('#toastIcon').textContent = bad ? 'error' : 'check_circle';
    $('#toastIcon').style.color = bad ? 'var(--danger)' : '';
    const el = $('#toast'); el.classList.add('show');
    clearTimeout(toastTimer); toastTimer = setTimeout(() => el.classList.remove('show'), ms);
  }
  const toastErr = (e, vars) => toast(errText(parseErr(e), vars), true);

  /* ------------------------------------------------ tiles */
  function hue(name){ let h = 0; for (const c of String(name)) h = (h * 31 + c.charCodeAt(0)) % 360; return h; }
  function tile(el, name, logo){
    el.style.background = `hsl(${hue(name)} 35% 42%)`;
    el.textContent = (String(name || '?').trim()[0] || '?').toUpperCase();
    if (logo){
      const img = new Image(); img.alt = ''; img.referrerPolicy = 'no-referrer';
      img.onload = () => { el.textContent = ''; el.appendChild(img); };
      img.src = logo;
    }
  }
  // The letter is always there, under the picture, so a picture that fails to
  // load still leaves a readable tile.
  const tileHTML = (name, logo) => `<div class="pack-tile" style="background:hsl(${hue(name)} 35% 42%)">${esc((String(name || '?').trim()[0] || '?').toUpperCase())}${logo ? `<img class="over" alt="" referrerpolicy="no-referrer" src="${esc(logo)}" onerror="this.remove()">` : ''}</div>`;

  function face(name){ // an 8x8 pixel face per player name
    const c = document.createElement('canvas'); c.width = c.height = 8; const x = c.getContext('2d');
    let seed = hue(name) + 7; const r = () => (seed = (seed * 9301 + 49297) % 233280) / 233280;
    const skin = ['#c68b62','#8d5a3b','#e0b08a'][Math.floor(r()*3)], hair = ['#3b2a1a','#6a3a1a','#1a1a1a','#c9a24a'][Math.floor(r()*4)];
    x.fillStyle = skin; x.fillRect(0,0,8,8); x.fillStyle = hair; x.fillRect(0,0,8,2); x.fillRect(0,2,1,2); x.fillRect(7,2,1,2);
    x.fillStyle = '#fff'; x.fillRect(1,4,2,1); x.fillRect(5,4,2,1); x.fillStyle = '#3a4a8c'; x.fillRect(2,4,1,1); x.fillRect(5,4,1,1);
    x.fillStyle = '#6a3a2a'; x.fillRect(3,6,2,1); return c.toDataURL();
  }

  /* ------------------------------------------------ navigation */
  function show(v){
    const changed = S.view !== v;
    S.view = v;
    ['welcome','server','new','progress','settings'].forEach(k => { $('#v-' + k).hidden = k !== v; });
    if (v === 'server') renderServer();
    if (v === 'settings') renderSettings();
    renderSidebar();
    $('#main').scrollTop = 0;
    if (changed){
      const sec = $('#v-' + v);
      Motion.enter(sec, v === 'server' ? ':scope > .head' : '.card, .head, .form-foot, .update-note', {max: 12});
    }
  }
  const home = () => { if (S.current && S.servers.has(S.current)) show('server'); else if (S.servers.size) { S.current = S.servers.keys().next().value; show('server'); } else show('welcome'); };

  function setTab(x){
    const changed = S.tab !== x || S.tabShownFor !== S.current;
    S.tab = x; S.tabShownFor = S.current;
    $$('[role=tab]').forEach(b => b.setAttribute('aria-selected', b.dataset.tab === x));
    ['overview','mods','props','console'].forEach(k => { $('#p-' + k).hidden = k !== x; });
    // The mod list animates itself once it has loaded, so the panel only
    // animates its fixed parts.
    if (changed) Motion.enter($('#p-' + x), x === 'mods' ? '.mods-top, .chips' : '.card, .console > *, details.adv, .form-foot', {max: 12});
    if (x === 'mods') loadMods();
    if (x === 'props') loadProps();
    if (x === 'console') loadLog();
  }

  function openServer(id, tab = S.tab){
    if (S.current !== id){ S.current = id; S.modsFor = null; S.props = S.propsSaved = null; }
    show('server'); setTab(tab);
  }

  function renderSidebar(){
    const list = $('#srvList'); list.querySelectorAll('.srv-row').forEach(n => n.remove());
    for (const s of S.servers.values()){
      const b = document.createElement('button'); b.type = 'button'; b.className = 'srv';
      b.setAttribute('aria-current', S.view === 'server' && s.id === S.current ? 'true' : 'false');
      const st = {
        stopped: esc(t('stStopped')),
        starting: `<span class="pulse">${esc(t('sideStarting'))}</span>`,
        running: `<span class="live">${esc(t('sideOnline'))}</span>${s.players.length ? ', ' + esc(t('sidePlaying', {n: s.players.length})) : ''}`,
        crashed: `<span class="bad">${esc(t('sideAttention'))}</span>`,
      }[s.state] || '';
      b.innerHTML = `${tileHTML(s.name, s.icon || s.logoUrl)}<div><div class="t">${esc(s.name)}</div><div class="s">${st}</div></div>`;
      b.onclick = () => openServer(s.id);
      b.oncontextmenu = e => { e.preventDefault(); openMenu(s.id, e.clientX, e.clientY, b); };
      b.onkeydown = e => {
        if (e.key !== 'ContextMenu' && !(e.shiftKey && e.key === 'F10')) return;
        e.preventDefault();
        const r = b.getBoundingClientRect();
        openMenu(s.id, r.left + 24, r.bottom - 6, b);
      };
      // The ⋯ button opens the same menu, for anyone who doesn't right-click.
      const more = document.createElement('button');
      more.type = 'button'; more.className = 'srv-more';
      more.setAttribute('aria-label', t('moreActions', {name: s.name}));
      more.setAttribute('aria-haspopup', 'menu'); more.setAttribute('aria-expanded', 'false');
      more.title = t('moreActionsTip');
      more.innerHTML = '<span class="ms">more_horiz</span>';
      more.onclick = () => {
        if (menuClosedBy === more){ menuClosedBy = null; return; } // this click closed it
        const r = more.getBoundingClientRect();
        openMenu(s.id, r.right, r.bottom + 4, more, true);
      };
      const row = document.createElement('div'); row.className = 'srv-row';
      row.append(b, more);
      list.appendChild(row);
    }
    $('#settingsBtn').setAttribute('aria-current', S.view === 'settings');
  }

  /* ------------------------------------------------ server overview */
  const btn = (cls, attrs, icon, label, fill) => `<button class="btn ${cls}" type="button" ${attrs}><span class="ms${fill ? ' fill' : ''}">${icon}</span><span>${esc(label)}</span></button>`;

  function renderServer(){
    const s = srv(); if (!s) return home();
    tile($('#hTile'), s.name, s.icon || s.logoUrl);
    $('#hName').textContent = s.name; $('#hName').title = s.name;
    $('#hMeta').textContent = t('mcWith', packVars(s));
    const card = $('#statusCard'), pill = $('#sPill'), acts = $('#sActions'), extra = $('#sExtra');
    const P = (cls, icon, key) => { pill.className = 'pill ' + cls; pill.innerHTML = `<span class="ms fill ${cls === 'warn' ? 'pulse' : ''}">${icon}</span><span>${esc(t(key))}</span>`; };
    extra.innerHTML = ''; acts.innerHTML = '';
    const stopping = S.stopping.has(s.id);
    const stateKey = s.id + ':' + s.state;
    const stateChanged = S.lastState !== stateKey;
    S.lastState = stateKey;
    if (s.state === 'starting'){
      card.style.setProperty('--state-glow', 'var(--accent)');
      P('warn', 'progress_activity', 'stStarting');
      $('#sTitle').textContent = t('startingTitle');
      $('#sLead').textContent = t('startingLead', {n: s.modsOnServer});
      extra.innerHTML = `<div class="bar indeterminate"><i></i></div>`;
      acts.innerHTML = btn('big', 'data-act="stop"' + (stopping ? ' disabled' : ''), 'close', stopping ? t('stopping') : t('cancel'));
    } else if (s.state === 'running'){
      card.style.setProperty('--state-glow', 'var(--ok)');
      P('ok', 'check_circle', 'stOnline');
      $('#sTitle').textContent = t('runningTitle');
      $('#sLead').textContent = s.players.length ? t('runningLead', {n: s.players.length, max: s.maxPlayers}) : t('runningLeadNone');
      if (s.players.length) extra.innerHTML = `<div class="players">${s.players.map(p => `<span class="player"><img class="face" alt="" src="${face(p)}">${esc(p)}</span>`).join('')}</div>`;
      acts.innerHTML = btn('big', 'data-act="stop"' + (stopping ? ' disabled' : ''), 'stop', stopping ? t('stopping') : t('stopSave'), true)
        + btn('big ghost', 'data-act="restart"' + (stopping ? ' disabled' : ''), 'restart_alt', t('restart'));
    } else if (s.state === 'crashed'){
      card.style.setProperty('--state-glow', 'var(--danger)');
      P('bad', 'error', 'stProblem');
      const mods = (s.crash && s.crash.mods) || [];
      if (mods.length){
        $('#sTitle').textContent = t('crashTitle');
        $('#sLead').textContent = mods.length === 1 ? t('crashLead', {mod: mods[0].name}) : t('crashLeadMany', {mods: mods.map(m => m.name).join(', ')});
        const files = mods.filter(m => m.file).map(m => 'mods\\' + m.file);
        if (files.length) extra.innerHTML = `<div class="jarpath">${files.map(esc).join('<br>')}</div>`;
        acts.innerHTML = btn('primary big', 'data-act="fix"', 'healing', t(mods.length === 1 ? 'fixStart' : 'fixStartMany'))
          + btn('big ghost', 'data-tab="console"', 'terminal', t('seeWhat'));
      } else {
        $('#sTitle').textContent = t('crashAnyTitle');
        $('#sLead').textContent = t('crashAnyLead');
        acts.innerHTML = btn('primary big', 'data-act="start"', 'play_arrow', t('startAgain'), true)
          + btn('big ghost', 'data-tab="console"', 'terminal', t('seeWhat'));
      }
    } else {
      card.style.setProperty('--state-glow', 'var(--accent)');
      P('', 'stop_circle', 'stStopped');
      $('#sTitle').textContent = t('readyTitle');
      $('#sLead').textContent = t('readyLead');
      acts.innerHTML = btn('primary big', 'data-act="start"', 'play_arrow', t('startServer'), true)
        + btn('big', 'data-act="update"', 'update', t('updatePack'));
    }
    if (stateChanged){ Motion.swap(pill); Motion.swap($('#sTitle')); Motion.swap($('#sLead')); Motion.enter(acts, '.btn', {rise: 6, stagger: 50}); }
    renderAddresses(); renderMemory(); renderLog();
    const on = s.state === 'running';
    $('#cmdIn').disabled = $('#cmdBtn').disabled = !on;
    $('#cmdHint').textContent = t(on ? 'cmdOn' : 'cmdOff');
    if (s.state !== 'running'){ $('#restartNote').hidden = true; $('#propsNote').hidden = true; }
  }

  function renderAddresses(){
    const s = srv(), port = s.port && s.port !== 25565 ? ':' + s.port : '';
    $('#addrLan').textContent = (S.app.lanIp || 'localhost') + port;
    const wan = $('#addrWan');
    if (S.publicIp){
      wan.textContent = S.publicIp + port; $('#wanCopy').hidden = false;
    } else {
      $('#wanCopy').hidden = true;
      wan.innerHTML = `<button class="link" type="button" id="findWan">${esc(t('findAddress'))}</button>`;
      $('#findWan').onclick = async e => {
        e.target.textContent = t('finding'); e.target.disabled = true;
        try { S.publicIp = await api().PublicAddress(); renderAddresses(); }
        catch { e.target.textContent = t('addrFail'); e.target.disabled = false; }
      };
    }
  }

  function renderMemory(){
    const s = srv(), total = S.app.memoryGb || 0;
    const r = $('#mem'); r.min = 2; r.max = Math.max(4, total ? total - 2 : 16);
    r.value = s.ramGb;
    $('#memVal').textContent = s.ramGb; $('#memMin').textContent = r.min + ' GB'; $('#memMax').textContent = r.max + ' GB';
    $('#memNote').textContent = total ? t('pcHas', {gb: total}) : '';
    memHint();
  }
  function memHint(){
    const v = +$('#mem').value, total = S.app.memoryGb || 0, s = srv();
    const [cls, icon, key] = v < 4 ? ['warn','warning','memLow']
      : total && v > total - 4 ? ['warn','warning','memHigh']
      : ['ok','check_circle', s && (s.state === 'running' || s.state === 'starting') ? 'memNext' : 'memGood'];
    const h = $('#memHint'); h.className = 'mem-hint ' + cls; h.innerHTML = `<span class="ms">${icon}</span><span>${esc(t(key))}</span>`;
  }
  let memTimer;
  $('#mem').addEventListener('input', e => {
    const v = +e.target.value; $('#memVal').textContent = v; memHint();
    const s = srv(); if (!s) return; s.ramGb = v;
    clearTimeout(memTimer); memTimer = setTimeout(() => api().SetMemory(s.id, v).catch(toastErr), 400);
  });

  /* ------------------------------------------------ server log */
  const logClass = l => /^> /.test(l) ? 'me' : /Done \(|joined the game/.test(l) ? 'g' : /\b(ERROR|FATAL)\b|Exception/.test(l) ? 'e' : /\bWARN\b/.test(l) ? 'w' : '';
  const lineHTML = l => `<span class="${logClass(l)}">${esc(l)}</span>`;
  async function loadLog(){
    const s = srv(); if (!s) return;
    try { S.logs[s.id] = await api().ServerLog(s.id); } catch { S.logs[s.id] = S.logs[s.id] || []; }
    renderLog();
  }
  function renderLog(){
    const s = srv(); if (!s) return;
    const lines = S.logs[s.id] || [];
    const full = $('#logFull');
    full.innerHTML = lines.length ? lines.map(lineHTML).join('\n') : `<span class="d">${esc(t('noMessages'))}</span>`;
    full.scrollTop = full.scrollHeight;
    $('#logMini').innerHTML = lines.length ? lines.slice(-3).map(lineHTML).join('\n') : `<span class="d">${esc(t('noMessages'))}</span>`;
  }
  function appendLog(id, lines){
    const all = (S.logs[id] = (S.logs[id] || []).concat(lines));
    if (all.length > 2000) S.logs[id] = all.slice(-2000);
    if (id !== S.current || S.view !== 'server') return;
    const full = $('#logFull'), atEnd = full.scrollHeight - full.scrollTop - full.clientHeight < 40;
    if (!full.querySelector('span:not(.d)')) full.innerHTML = '';
    full.insertAdjacentHTML('beforeend', (full.childElementCount ? '\n' : '') + lines.map(lineHTML).join('\n'));
    while (full.childElementCount > 2000) full.firstElementChild.remove();
    if (atEnd) full.scrollTop = full.scrollHeight;
    $('#logMini').innerHTML = S.logs[id].slice(-3).map(lineHTML).join('\n');
  }
  $('#cmdForm').addEventListener('submit', async e => {
    e.preventDefault();
    const v = $('#cmdIn').value.trim(); if (!v) return;
    $('#cmdIn').value = '';
    try { await api().SendCommand(S.current, v); } catch (err) { toastErr(err); }
  });

  /* ------------------------------------------------ server actions */
  async function act(a, id = S.current){
    const s = S.servers.get(id); if (!s) return;
    try {
      if (a === 'start'){ $('#restartNote').hidden = true; await api().StartServer(s.id); }
      if (a === 'stop'){ S.stopping.add(s.id); renderServer(); await api().StopServer(s.id); }
      if (a === 'restart'){
        $('#restartNote').hidden = true; S.stopping.add(s.id); S.quiet.add(s.id); renderServer();
        try { await api().RestartServer(s.id); } finally { S.quiet.delete(s.id); S.stopping.delete(s.id); }
      }
      if (a === 'fix'){ await api().FixCrash(s.id); }
      if (a === 'update'){ openForm(s.id); }
      if (a === 'folder'){ api().OpenServerFolder(s.id); }
    } catch (err){ S.stopping.delete(s.id); renderServer(); toastErr(err); }
  }

  /* ------------------------------------------------ mods */
  // A list already loaded for this server shows at once and refreshes
  // quietly; the first load shows placeholder rows, never "no mods".
  async function loadMods(){
    const s = srv(); if (!s) return;
    $('#modUrlHelp').textContent = t('onlyCompatible', packVars(s));
    const first = S.modsFor !== s.id;
    if (first){
      S.mods = []; S.modsFor = null;
      $('#modList').innerHTML = '<div class="sk mod-sk"></div>'.repeat(6);
    } else renderMods();
    const token = (S.modsToken = (S.modsToken || 0) + 1);
    let mods;
    try { mods = await api().Mods(s.id); } catch (err){ mods = []; toastErr(err); }
    if (token !== S.modsToken || S.current !== s.id) return; // switched away meanwhile
    S.mods = mods; S.modsFor = s.id;
    renderMods(first);
  }
  function renderMods(animate){
    const q = $('#modSearch').value.trim().toLowerCase();
    const match = m => S.filter === 'all' || m.state === S.filter;
    const list = S.mods.filter(m => match(m) && (!q || m.name.toLowerCase().includes(q) || m.fileName.toLowerCase().includes(q)));
    const box = $('#modList');
    if (!list.length){
      const msg = q ? esc(t('noModsMatch', {q})) : S.filter === 'mine' ? t('noneAdded') : esc(t('noModsYet'));
      box.innerHTML = `<div class="card empty" style="grid-column:1/-1">${msg}</div>`;
      return;
    }
    const why = {client: 'whyClient', list: 'whyList', you: 'whyYou'};
    const rows = list.map((m, i) => {
      // A mod added only because another added mod needs it goes away with
      // that mod, so it has no button of its own.
      const dep = m.state === 'mine' && m.reason === 'dep';
      const status = dep ? `<div class="why mine">${esc(t('neededBy', {names: (m.neededBy || []).join(', ')}))}</div>`
        : m.state === 'mine' ? `<div class="why mine">${esc(t('addedByYou'))}</div>`
        : `<div class="why ${m.state === 'on' ? 'on' : 'off'}">${esc(m.state === 'on' ? t('onServer') : t('leftOffWhy', {why: t(why[m.reason] || 'whyYou')}))}</div>`;
      const label = t(m.state === 'mine' ? 'remove' : m.state === 'on' ? 'leaveOff' : 'keepAnyway');
      const action = dep ? '<span></span>' : `<button class="btn sm" type="button" data-mod="${i}">${esc(label)}</button>`;
      return `<div class="card mod${m.state === 'off' ? ' removed' : ''}">${tileHTML(m.name)}
        <div><b>${esc(m.name)}</b><div class="f" title="${esc(m.fileName)}">${esc(m.fileName)}</div>${status}</div>
        ${action}</div>`;
    });
    // The first screenful now; the rest once the rows have settled in, a
    // few at a time, so a pack with hundreds of mods never holds up a frame.
    S.modRows = list;
    const token = (S.modRender = (S.modRender || 0) + 1);
    box.innerHTML = rows.slice(0, 16).join('');
    if (animate === true) Motion.enter(box, '.mod', {max: 12, stagger: 22});
    let at = 16;
    const more = () => {
      if (token !== S.modRender || at >= rows.length) return;
      box.insertAdjacentHTML('beforeend', rows.slice(at, at + 30).join(''));
      at += 30;
      requestAnimationFrame(() => setTimeout(more));
    };
    if (rows.length > at) setTimeout(more, animate === true ? Motion.ms(500) : 0);
  }
  // One click handler for every row's button.
  $('#modList').addEventListener('click', async e => {
    const b = e.target.closest('[data-mod]'); if (!b || b.disabled) return;
    const m = S.modRows[+b.dataset.mod], id = S.current;
    if (!m) return;
    {
      b.disabled = true;
      try {
        if (m.state === 'mine'){
          const deps = await api().RemoveMod(id, m.fileName);
          toast(deps && deps.length ? t('toastRemovedDeps', {name: m.name, deps: deps.join(', ')}) : t('toastRemoved', {name: m.name}));
        }
        else if (m.state === 'on'){ await api().SetModOff(id, m.fileName); toast(t('toastWillBeOff', {name: m.name})); }
        else { b.textContent = t('adding'); await api().SetModOn(id, m.fileName); toast(t('toastWillBeOn', {name: m.name})); }
        modsChanged();
      } catch (err){ toastErr(err); b.disabled = false; }
      loadMods();
    }
  });
  function modsChanged(){
    const s = srv();
    $('#restartNote').hidden = !(s && s.state === 'running');
  }
  $('#modSearch').addEventListener('input', renderMods);
  $$('.chip').forEach(c => c.onclick = () => { S.filter = c.dataset.filter; $$('.chip').forEach(x => x.setAttribute('aria-pressed', x === c)); renderMods(true); });

  /* Add mods panel */
  const addPanel = $('#addPanel');
  let searchTimer, searchToken = 0;
  function toggleAdd(open){
    addPanel.hidden = !open; $('#addModBtn').setAttribute('aria-expanded', open);
    if (open) Motion.enter(addPanel, null, {rise: 8});
    if (open){ $('#modUrl').value = ''; runSearch(); $('#modUrl').focus(); }
  }
  $('#addModBtn').onclick = () => toggleAdd(addPanel.hidden);
  $('#addClose').onclick = () => toggleAdd(false);

  function setModErr(msg){
    $('#modUrlErrTxt').textContent = msg || ''; $('#modUrlErr').hidden = !msg;
    $('#modUrl').setAttribute('aria-invalid', msg ? 'true' : 'false');
  }
  const fmtCount = n => { try { return new Intl.NumberFormat(lang, {notation: 'compact', maximumFractionDigits: 1}).format(n); } catch { return String(n); } };

  function resultRow(p){
    const action = p.onServer
      ? `<span class="state"><span class="ms">check_circle</span>${esc(t('onServer'))}</span>`
      : `<button class="btn sm" type="button" data-add="${p.id}" data-name="${esc(p.name)}">${esc(t(p.clientOnly ? 'addAnyway' : 'add'))}</button>`;
    const sum = p.clientOnly ? `<div class="sum warn">${esc(t('clientOnly'))}</div>` : `<div class="sum" title="${esc(p.summary)}">${esc(p.summary)}</div>`;
    return `<div class="result" role="listitem">${tileHTML(p.name, p.logoUrl)}
      <div style="min-width:0"><div class="top"><b>${esc(p.name)}</b><span class="by">${esc(t('byDownloads', {by: p.author || '?', dl: fmtCount(p.downloadCount)}))}</span></div>${sum}</div>${action}</div>`;
  }
  function showResults(head, html){
    $('#modResultsWrap').hidden = false;
    $('#modResultsHead').textContent = head;
    $('#modResults').innerHTML = html;
    Motion.enter($('#modResults'), '.result', {max: 10, stagger: 26, rise: 6});
  }
  function runSearch(){
    const q = $('#modUrl').value.trim(), s = srv(), token = ++searchToken;
    $('#modFound').hidden = true; setModErr('');
    clearTimeout(searchTimer);
    if (/^https?:/i.test(q)){ $('#modResultsWrap').hidden = true; resolveLink(q, token); return; }
    showResults(q ? t('searching') : t('popular', packVars(s)), '<div class="sk"></div><div class="sk"></div><div class="sk"></div>');
    searchTimer = setTimeout(async () => {
      try {
        const found = await api().SearchMods(s.id, q);
        if (token !== searchToken) return;
        if (!found.length){ showResults(t('noMatches'), `<div class="results-empty">${esc(t('noSuchMod', {q, ...packVars(s)}))}</div>`); return; }
        showResults(q ? t(found.length === 1 ? 'resultsOne' : 'resultsCount', {n: found.length, ...packVars(s)}) : t('popular', packVars(s)), found.map(resultRow).join(''));
      } catch (err){
        if (token === searchToken){ $('#modResultsWrap').hidden = true; setModErr(errText(parseErr(err))); }
      }
    }, q ? (reduce ? 0 : 450) : 0);
  }
  async function resolveLink(link, token){
    const s = srv(), out = $('#modFound');
    try {
      const p = await api().ModFromLink(s.id, link);
      if (token !== searchToken) return;
      out.className = 'found-mod' + (p.clientOnly ? ' warn' : ''); out.hidden = false;
      out.innerHTML = `${tileHTML(p.name, p.logoUrl)}
        <div><b>${esc(p.name)}</b><span>${esc(p.clientOnly ? t('foundClientOnly') : t('foundFor', packVars(s)))}</span></div>
        <div class="acts">${p.onServer ? `<span class="small">${esc(t('onServer'))}</span>` : `<button class="btn sm ${p.clientOnly ? '' : 'primary'}" type="button" data-add="${p.id}" data-name="${esc(p.name)}" ${p.clientOnly ? 'data-force="1"' : ''}>${esc(t(p.clientOnly ? 'addAnyway' : 'addToServer'))}</button>`}</div>`;
    } catch (err){
      if (token !== searchToken) return;
      const e = parseErr(err);
      setModErr(errText(e, {name: e.detail, ...packVars(s)}));
    }
  }
  $('#modUrl').addEventListener('input', runSearch);
  $('#modUrl').addEventListener('keydown', e => { if (e.key === 'Enter'){ e.preventDefault(); runSearch(); } });

  // Add buttons in results and in the pasted-link card.
  addPanel.addEventListener('click', async e => {
    const b = e.target.closest('[data-add]'); if (!b) return;
    const s = srv(), id = +b.dataset.add, name = b.dataset.name, force = b.dataset.force === '1';
    const label = b.textContent; b.disabled = true; b.textContent = t('adding');
    try {
      const r = await api().AddMod(s.id, id, name, force);
      if (r.status === 'added' || r.status === 'already'){
        b.outerHTML = `<span class="state"><span class="ms">check_circle</span>${esc(t(r.status === 'added' ? 'added' : 'onServer'))}</span>`;
        toast(r.status === 'already' ? t('toastAlready', {name})
          : r.deps && r.deps.length ? t('toastAddedDeps', {name, deps: r.deps.join(', ')}) : t('toastAdded', {name}));
        modsChanged(); loadMods();
      } else if (r.status === 'client_only'){
        b.disabled = false; b.dataset.force = '1'; b.textContent = t('addAnyway');
        const row = b.closest('.result'); const sum = row && row.querySelector('.sum');
        if (sum){ sum.className = 'sum warn'; sum.textContent = t('clientOnly'); }
      } else if (r.status === 'no_version'){
        b.disabled = false; b.textContent = label;
        toast(t('err_no_version', {name, ...packVars(s)}), true);
      } else if (r.status === 'dep_missing'){
        b.disabled = false; b.textContent = label;
        toast(t('depMissing', {name, dep: r.missing, ...packVars(s)}), true);
      }
    } catch (err){
      b.disabled = false; b.textContent = label;
      const e = parseErr(err);
      toast(errText(e, {name: e.detail || name}), true);
    }
  });

  async function addJars(paths){
    try {
      const skipped = paths ? await api().AddJarFiles(S.current, paths) : await api().PickJarFiles(S.current);
      if (skipped && skipped.length) toast(t('toastSkipped', {files: skipped.join(', ')}), true);
      modsChanged(); loadMods();
    } catch (err){ toastErr(err); }
  }
  $('#jarDrop').onclick = () => addJars(null);

  /* ------------------------------------------------ new server / update form */
  // Same rule as defaultRAMGB in internal/app: a third of the PC's memory,
  // rounded up to an even number, between 4 and 8 GB.
  function suggestedRAM(){
    const total = S.app.memoryGb || 0;
    if (!total) return 6;
    const gb = Math.floor(total / 3);
    return Math.max(4, Math.min(8, gb + gb % 2));
  }
  function renderRAM(){
    const total = S.app.memoryGb || 0, rec = S.form.recRam;
    const options = [4, 6, 8, 10].filter(v => !total || v <= Math.max(4, total - 2));
    const small = {4: 'ramSmall', 6: 'ramSmall', 8: 'ramBig', 10: 'ramMany'};
    $('#ramSeg').innerHTML = options.map(v => `<label><input type="radio" name="ram" value="${v}" ${v === S.form.ram ? 'checked' : ''}><span>${v} GB<small>${esc(t(v === rec ? 'ramRec' : small[v]))}</small></span></label>`).join('');
    $$('#ramSeg input').forEach(i => i.onchange = () => { S.form.ram = +i.value; });
    $('#ramHelp').textContent = total ? t('ramHelp', {gb: total}) : '';
  }

  function openForm(updateId){
    if (S.setup && S.setup.running){ show('progress'); return; }
    const up = updateId ? S.servers.get(updateId) : null;
    const recOpt = suggestedRAM();
    S.form = {update: updateId || null, preview: null, looking: false, dir: up ? up.dir : '', dirTouched: !!up, ram: up ? up.ramGb : recOpt, recRam: recOpt, lookupToken: 0};
    $('#newTitle').textContent = up ? t('updateTitle', {name: up.name}) : t('newServer');
    $('#newMeta').textContent = up ? t('updateMeta') : t('newMeta');
    $('#packUrl').value = up ? up.source : '';
    $('#outDir').value = S.form.dir;
    $('#folderField').hidden = !!up;
    $('#st3').hidden = !!up;
    $('#updateNote').hidden = !up;
    $('#eula').checked = !!up;
    $('#optJava').value = 'auto'; $('#optLoader').value = 'auto'; $('#optClean').checked = !(up && up.keepClient); $('#optExcl').value = ''; $('#optIncl').value = '';
    $('#advanced').open = false;
    $('#setupBtnTxt').textContent = t(up ? 'updateBtn' : 'setUp');
    $('#packFound').hidden = true; $('#packErr').hidden = true;
    renderRAM();
    show('new');
    if ($('#packUrl').value) lookupPack(); else validate();
  }

  let lookupTimer;
  function lookupPack(){
    const v = $('#packUrl').value.trim(), f = S.form, token = ++f.lookupToken;
    clearTimeout(lookupTimer);
    f.preview = null; $('#packErr').hidden = true; $('#packUrl').setAttribute('aria-invalid', 'false');
    if (!v){ $('#packFound').hidden = true; validate(); return; }
    f.looking = true;
    $('#packFound').hidden = false;
    $('#packFound').innerHTML = `<div class="pack-tile"></div><div><b>${esc(t('packLooking'))}</b></div>`;
    validate();
    lookupTimer = setTimeout(async () => {
      try {
        const p = await api().LookupPack(v);
        if (token !== f.lookupToken) return;
        f.preview = p; f.looking = false;
        const sub = p.kind === 'curseforge' ? (p.author ? t('packBy', {author: p.author}) : '') + (p.summary ? (p.author ? '. ' : '') + p.summary : '')
          : p.kind === 'drive' ? t('packDrive') : p.kind === 'folder' ? t('packFolder') : t('packFile');
        $('#packFound').innerHTML = `${tileHTML(p.name || 'G', p.logoUrl)}<div><b>${esc(p.name || 'Google Drive')}</b><span>${esc(sub)}</span></div>`;
        if (!f.dirTouched && !f.update){
          f.dir = await api().SuggestServerDir(p.name || 'Minecraft server');
          $('#outDir').value = f.dir;
        }
      } catch (err){
        if (token !== f.lookupToken) return;
        f.looking = false; $('#packFound').hidden = true;
        $('#packErrTxt').textContent = errText(parseErr(err));
        $('#packErr').hidden = false; $('#packUrl').setAttribute('aria-invalid', 'true');
      }
      validate();
    }, 450);
  }

  function validate(){
    const f = S.form; if (!f) return;
    const packOk = !!f.preview, eula = $('#eula').checked, dir = !!f.dir;
    const ready = packOk && eula && dir;
    $('#st1').classList.toggle('done', packOk);
    $('#st2').classList.toggle('done', dir);
    $('#st3').classList.toggle('done', eula);
    $('#setupBtn').disabled = !ready;
    $('#setupWhy').textContent = ready ? t('whyReady') : f.looking ? t('packLooking')
      : !packOk && !eula ? t('whyBoth') : !packOk ? t('whyPack') : !eula ? t('whyEula') : t('whyFolder');
  }
  $('#packUrl').addEventListener('input', lookupPack);
  $('#eula').addEventListener('change', validate);
  $('#drop').onclick = async () => {
    try { const p = await api().PickPackFile(); if (p){ $('#packUrl').value = p; lookupPack(); } } catch (err){ toastErr(err); }
  };
  $('#pickDir').onclick = async () => {
    try {
      const d = await api().PickFolder(S.form.dir);
      if (!d) return;
      const name = (S.form.preview && S.form.preview.name) || 'Minecraft server';
      S.form.dir = await api().ServerDirIn(d, name);
      S.form.dirTouched = true; $('#outDir').value = S.form.dir; validate();
    } catch (err){ toastErr(err); }
  };
  $('#optJava').addEventListener('change', async e => {
    if (e.target.value !== 'pick') return;
    try {
      const p = await api().PickJavaExe();
      if (!p){ e.target.value = 'auto'; return; }
      let o = e.target.querySelector('option[data-path]');
      if (!o){ o = document.createElement('option'); o.dataset.path = '1'; e.target.insertBefore(o, e.target.lastElementChild); }
      o.value = p; o.textContent = p; e.target.value = p;
    } catch (err){ e.target.value = 'auto'; toastErr(err); }
  });

  $('#newForm').addEventListener('submit', async e => {
    e.preventDefault();
    if ($('#setupBtn').disabled) return;
    const f = S.form;
    const req = {
      serverId: f.update || '', input: $('#packUrl').value.trim(), dir: f.dir, ramGb: f.ram, acceptEula: $('#eula').checked,
      java: $('#optJava').value, loader: $('#optLoader').value, keepClient: !$('#optClean').checked,
      exclude: $('#optExcl').value, include: $('#optIncl').value,
    };
    // Events can arrive before StartSetup returns, so the progress state
    // has to exist first.
    startProgress(f.preview ? f.preview.name : '', f.preview ? f.preview.logoUrl : '', !!f.update, req);
    try {
      await api().StartSetup(req);
    } catch (err){
      S.setup = null; show('new');
      $('#setupWhy').textContent = errText(parseErr(err));
    }
  });

  /* ------------------------------------------------ setup progress */
  const STAGES = ['find-pack', 'mods', 'clean', 'java', 'loader', 'finish'];
  const WEIGHT = {'find-pack': 8, mods: 52, clean: 6, java: 12, loader: 18, finish: 4};

  function startProgress(name, logo, update, req){
    S.setup = {running: true, name, logo, update, req, stage: 'find-pack', seen: new Set(['find-pack']), info: null, done: 0, total: 0, result: null, error: null, cancelling: false};
    paintSetup(); show('progress');
  }

  function paintSetup(){
    const st = S.setup; if (!st) return;
    const name = (st.info && st.info.Name) || st.name || t('thePack');
    tile($('#pTile'), name, st.logo);
    const finished = !!st.result, failed = !!st.error;
    $('#pCard').hidden = failed;
    $('#pError').hidden = !failed;
    $('#pFailed').hidden = !(finished && st.result.failed && st.result.failed.length);
    $('#pTitle').textContent = finished ? t('readyServer') : t(st.update ? 'updating' : 'settingUp', {name});
    $('#pSub').textContent = finished
      ? (st.result.leftOff ? t('readySub', {n: st.result.server.modsOnServer, off: st.result.leftOff}) : t('readySubNone', {n: st.result.server.modsOnServer}))
      : failed ? '' : t('setupSub');

    // Stages the setup skipped (no Java download, no clean) do not count.
    const cur = STAGES.indexOf(st.stage);
    let done = 0, total = 0;
    STAGES.forEach((k, i) => {
      if (i < cur && !st.seen.has(k)) return;
      total += WEIGHT[k];
      if (i < cur) done += WEIGHT[k];
      else if (i === cur && k === 'mods' && st.total) done += WEIGHT[k] * st.done / st.total;
    });
    const pct = finished ? 100 : Math.min(99, Math.round(done / total * 100));
    $('#pBar').style.width = pct + '%';
    $('#pPct').textContent = t('pctDone', {p: pct});
    $('#pEta').textContent = finished ? t('finished') : '';

    const info = st.info || {};
    $('#tasks').innerHTML = STAGES.map((k, i) => {
      const skipped = !st.seen.has(k) && (i < cur || finished);
      if (skipped) return '';
      const state = finished || i < cur ? 'done' : i === cur ? 'active' : 'pending';
      const icon = state === 'done' ? 'check' : state === 'active' ? 'progress_activity' : '';
      let title = t('task_' + ({'find-pack': 'find'}[k] || k)), detail = '', right = '', bar = '';
      if (k === 'java') title = info.JavaVersion ? t('task_java', {v: info.JavaVersion}) : t('task_java_any');
      if (k === 'loader') title = info.Loader ? t('task_loader', {loader: loaderName(info.Loader), v: info.LoaderVersion || ''}).trim() : t('task_loader_any');
      if (k === 'mods'){
        detail = state === 'done' ? t('task_mods_done', {n: st.total || info.ModCount || 0}) : t('task_mods_wait');
        if (state === 'active' && st.total){ right = t('nOfTotal', {n: st.done, total: st.total}); bar = `<div class="bar"><i style="width:${st.done / st.total * 100}%"></i></div>`; }
      } else detail = t('task_' + ({'find-pack': 'find'}[k] || k) + '_d');
      return `<li class="task ${state}"><div class="ic"><span class="ms">${icon}</span></div><div><div class="tl">${esc(title)}</div>${state !== 'pending' ? `<div class="dt">${esc(detail)}</div>` : ''}${bar}</div><div class="rt num">${esc(right)}</div></li>`;
    }).join('');

    if (failed){
      $('#pErrMsg').textContent = errText(st.error);
      $('#pErrDetail').textContent = st.error.detail || '';
      $('#pErrDetail').parentElement.hidden = !st.error.detail;
    }
    if (finished && st.result.failed && st.result.failed.length){
      const n = st.result.failed.length;
      $('#pFailedTitle').textContent = n === 1 ? t('failedOne') : t('failedTitle', {n});
      $('#pFailedList').innerHTML = st.result.failed.map(f => `<button class="btn sm" type="button" data-open="${esc(f.pageUrl)}"><span>${esc(f.name)}</span><span class="ms">open_in_new</span></button>`).join('');
    }

    const foot = $('#pFoot');
    if (finished){
      foot.innerHTML = btn('primary big', 'id="pStart"', 'play_arrow', t('startServer'), true) + btn('big ghost', 'id="pOpen"', 'folder_open', t('openFolder'));
      $('#pStart').onclick = async () => { S.setup = null; show('server'); setTab('overview'); await act('start'); };
      $('#pOpen').onclick = () => api().OpenServerFolder(st.result.server.id);
    } else if (failed){
      foot.innerHTML = btn('primary big', 'id="pRetry"', 'restart_alt', t('tryAgain')) + btn('big ghost', 'id="pBack"', 'close', t('back'));
      $('#pRetry').onclick = () => { S.setup = null; show('new'); validate(); };
      $('#pBack').onclick = () => { S.setup = null; home(); };
    } else {
      foot.innerHTML = btn('', 'id="pCancel"' + (st.cancelling ? ' disabled' : ''), 'close', st.cancelling ? t('cancelling') : t('cancelSetup')) + `<span class="small">${esc(t('cancelSetupNote'))}</span>`;
      $('#pCancel').onclick = () => { st.cancelling = true; api().CancelSetup(); paintSetup(); };
    }
  }

  function onSetup(ev){
    const st = S.setup; if (!st) return;
    if (ev.phase === 'stage'){ st.stage = ev.stage; st.seen.add(ev.stage); if (ev.info) st.info = ev.info; }
    if (ev.phase === 'mods'){ st.done = ev.modsDone || 0; st.total = ev.modsTotal || 0; }
    if (ev.phase === 'done'){
      st.running = false; st.result = ev;
      upsert(ev.server); S.current = ev.server.id; renderSidebar();
    }
    if (ev.phase === 'error'){
      st.running = false;
      st.error = ev.error || {code: 'unknown'};
      if (st.error.code === 'cancelled'){ S.setup = null; toast(t('err_cancelled')); if (S.view === 'progress') show('new'); return; }
    }
    if (S.view === 'progress') paintSetup();
  }

  /* ------------------------------------------------ settings */
  function renderSettings(){
    const st = S.app.settings;
    $$('[data-theme-set]').forEach(b => b.setAttribute('aria-pressed', b.dataset.themeSet === (st.theme || 'system')));
    $('#setJava').checked = st.autoJava;
    $('#setDir').value = st.serversDir || '';
    $('#setKey').value = st.apiKey || '';
    $('#setUpdCheck').checked = !st.skipUpdateCheck;
    $$('[data-anim-set]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.animSet === (st.animation || ''))));
    $$('[data-fx-set]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.fxSet === (st.effects || ''))));
    $('#fxHelp').textContent = t(!st.effects && st.lightDetected ? 'fxAutoLight' : 'fxHelp');
    $('#verText').textContent = S.update && S.update.dev ? t('versionDev') : t('versionIs', {v: S.app.version});
  }
  async function saveSettings(change){
    Object.assign(S.app.settings, change);
    try { await api().SaveSettings(S.app.settings); } catch (err){ toastErr(err); }
  }
  function applyTheme(x){
    if (!x || x === 'system') document.documentElement.removeAttribute('data-theme');
    else document.documentElement.setAttribute('data-theme', x);
  }
  $$('[data-theme-set]').forEach(b => b.onclick = () => { applyTheme(b.dataset.themeSet); saveSettings({theme: b.dataset.themeSet}); renderSettings(); });
  $('#setJava').addEventListener('change', e => saveSettings({autoJava: e.target.checked}));
  $('#setUpdCheck').addEventListener('change', e => saveSettings({skipUpdateCheck: !e.target.checked}));
  // Glass effects: "full", "light", or "" to decide from how this PC draws.
  function lightLook(){
    const st = S.app.settings;
    return st.effects === 'light' || (!st.effects && !!st.lightDetected);
  }
  function applyEffects(){
    const root = document.documentElement, was = root.dataset.effects;
    root.dataset.effects = lightLook() ? 'light' : 'full';
    Motion.setScale(S.app.settings.animation || '');
    if (was && was !== root.dataset.effects) requestAnimationFrame(() => Motion.refresh(false));
  }
  // True when Windows is drawing the app without the graphics card, as on
  // Remote Desktop, in virtual machines or with a broken driver.
  function drawnWithoutGpu(){
    try {
      const gl = document.createElement('canvas').getContext('webgl');
      if (!gl) return true;
      const info = gl.getExtension('WEBGL_debug_renderer_info');
      const name = info ? String(gl.getParameter(info.UNMASKED_RENDERER_WEBGL)) : '';
      const lose = gl.getExtension('WEBGL_lose_context'); if (lose) lose.loseContext();
      return /swiftshader|basic render|llvmpipe|software/i.test(name);
    } catch { return false; }
  }
  // In Auto, watch the frames while the page settles in. A PC that cannot
  // keep up with the frosted glass gets the light look, now and next time.
  function checkEffects(){
    const st = S.app.settings;
    if (st.effects || st.lightDetected) return;
    if (drawnWithoutGpu()) return goLight();
    if (!Motion.scale() || document.visibilityState !== 'visible') return; // nothing moves to measure
    Motion.enter($('#v-' + S.view), '.card, .head', {max: 12});
    const frames = []; let last = 0;
    const until = performance.now() + 1200;
    const tick = now => {
      if (last) frames.push(now - last);
      last = now;
      if (now < until) return requestAnimationFrame(tick);
      if (frames.length < 8 || S.app.settings.effects) return;
      // The middle frame, so one slow start-up frame does not count.
      const mid = [...frames].sort((a, b) => a - b)[frames.length >> 1];
      if (mid > 30) goLight();
    };
    requestAnimationFrame(tick);
  }
  function goLight(){
    S.app.settings.lightDetected = true;
    applyEffects();
    saveSettings({lightDetected: true});
    toast(t('toastLightLook'), false, 8000);
    if (S.view === 'settings') renderSettings();
  }
  $$('[data-fx-set]').forEach(b => b.onclick = () => {
    const effects = b.dataset.fxSet;
    S.app.settings.effects = effects;
    // Choosing Auto again checks this PC afresh.
    if (!effects) S.app.settings.lightDetected = false;
    applyEffects();
    saveSettings({effects, lightDetected: S.app.settings.lightDetected});
    renderSettings();
    if (!effects) requestAnimationFrame(checkEffects);
  });
  $$('[data-anim-set]').forEach(b => b.onclick = () => {
    Motion.setScale(b.dataset.animSet);
    saveSettings({animation: b.dataset.animSet});
    renderSettings();
  });
  $('#checkUpd').onclick = () => checkUpdate(true);
  $('#setKey').addEventListener('change', e => { saveSettings({apiKey: e.target.value.trim()}); toast(t('toastSaved')); });
  $('#pickSetDir').onclick = async () => {
    try { const d = await api().PickFolder(S.app.settings.serversDir); if (d){ await saveSettings({serversDir: d}); renderSettings(); } } catch (err){ toastErr(err); }
  };

  function setLang(l){
    lang = l;
    saveSettings({language: l});
    applyStatic();
    renderSidebar();
    renderUpdate();
    if (S.view === 'server'){ renderServer(); if (S.tab === 'props' && S.props){ buildAdvanced(); renderProps(); } if (S.tab === 'mods'){ renderMods(); $('#modUrlHelp').textContent = t('onlyCompatible', packVars(srv())); if (!addPanel.hidden) runSearch(); } }
    if (S.view === 'new'){ $('#setupBtnTxt').textContent = t(S.form.update ? 'updateBtn' : 'setUp'); renderRAM(); validate();
      const up = S.form.update && S.servers.get(S.form.update);
      $('#newTitle').textContent = up ? t('updateTitle', {name: up.name}) : t('newServer'); $('#newMeta').textContent = up ? t('updateMeta') : t('newMeta'); }
    if (S.view === 'progress') paintSetup();
    if (S.view === 'settings') renderSettings();
    requestAnimationFrame(() => Motion.refresh(false));
  }
  $$('[data-lang-set]').forEach(b => b.onclick = () => setLang(b.dataset.langSet));

  /* ------------------------------------------------ server settings tab */
  const GAMEMODES = [['survival', 'gmSurvival'], ['creative', 'gmCreative'], ['adventure', 'gmAdventure']];
  const DIFFICULTIES = [['peaceful', 'dPeaceful'], ['easy', 'dEasy'], ['normal', 'dNormal'], ['hard', 'dHard']];

  async function loadProps(){
    const s = srv(); if (!s) return;
    if (S.props && S.props.id === s.id) return renderProps(); // keep unsaved edits
    try {
      const p = await api().ServerProperties(s.id);
      p.other = p.other || {};
      S.propsSaved = {...p, other: {...p.other}}; S.props = {...p, other: {...p.other}, id: s.id};
      buildAdvanced();
      renderProps();
    } catch (err){ toastErr(err); }
  }
  function propsDirty(){
    if (!S.props || !S.propsSaved) return false;
    const basic = Object.keys(S.propsSaved).filter(k => k !== 'other').some(k => S.props[k] !== S.propsSaved[k]);
    const a = S.props.other, b = S.propsSaved.other;
    return basic || Object.keys({...a, ...b}).some(k => a[k] !== b[k]);
  }

  // Builds the option buttons once and then only updates them, so the glass
  // lens behind the chosen option can flow to the new one.
  function fillSeg(el, options, value){
    let btns = [...el.querySelectorAll('button[data-v]')];
    if (btns.length !== options.length){
      btns.forEach(b => b.remove());
      options.forEach(([v]) => {
        const b = document.createElement('button');
        b.type = 'button'; b.setAttribute('role', 'radio'); b.dataset.v = v;
        el.appendChild(b);
      });
      btns = [...el.querySelectorAll('button[data-v]')];
    }
    btns.forEach((b, i) => {
      b.textContent = t(options[i][1]);
      b.setAttribute('aria-checked', String(options[i][0] === value));
    });
  }
  function renderProps(){
    const p = S.props; if (!p) return;
    $('#pOnline').checked = p.onlineMode;
    $('#pOnlineWarn').hidden = p.onlineMode;
    $('#pMax').value = p.maxPlayers;
    $('#pFlight').checked = p.allowFlight;
    $('#pPvp').checked = p.pvp;
    fillSeg($('#pMode'), GAMEMODES, p.gamemode);
    fillSeg($('#pDiff'), DIFFICULTIES, p.difficulty);
    if (document.activeElement !== $('#pMotd')) $('#pMotd').value = p.motd;
    $('#pMotdCount').textContent = `${[...p.motd].length}/59`;
    $('#pSpawn').value = p.spawnProtection;
    renderIcon();
    renderAdvanced();
    const dirty = propsDirty();
    $('#propsSave').disabled = !dirty; $('#propsUndo').disabled = !dirty;
  }
  function setProp(k, v){ S.props[k] = v; renderProps(); }
  $('#pOnline').addEventListener('change', e => setProp('onlineMode', e.target.checked));
  $('#pFlight').addEventListener('change', e => setProp('allowFlight', e.target.checked));
  $('#pPvp').addEventListener('change', e => setProp('pvp', e.target.checked));
  $('#pMotd').addEventListener('input', e => setProp('motd', e.target.value));
  $('#pMax').addEventListener('change', e => setProp('maxPlayers', Math.max(1, Math.min(1000, parseInt(e.target.value, 10) || 1))));
  $$('#p-props [data-step]').forEach(b => b.onclick = () => setProp('maxPlayers', Math.max(1, Math.min(1000, S.props.maxPlayers + +b.dataset.step))));
  $('#pSpawn').addEventListener('change', e => setProp('spawnProtection', Math.max(0, Math.min(100000, parseInt(e.target.value, 10) || 0))));
  $$('#p-props [data-spawn]').forEach(b => b.onclick = () => setProp('spawnProtection', Math.max(0, Math.min(100000, S.props.spawnProtection + +b.dataset.spawn))));

  /* Server picture */
  function renderIcon(){
    const s = srv(); if (!s || !S.props) return;
    const box = $('#pIcon');
    box.classList.toggle('has', !!s.icon);
    // Only rebuild the picture when it changes, not on every settings edit.
    if (box.dataset.src !== (s.icon || '')){
      box.dataset.src = s.icon || '';
      box.innerHTML = s.icon ? `<img alt="" src="${s.icon}">` : '<span class="ms">image</span>';
    }
    $('#pvName').textContent = s.name;
    $('#pvCount').textContent = `${s.players.length}/${S.props.maxPlayers}`;
    $('#pvCountWrap').title = t('playerCountTip', {n: s.players.length, max: S.props.maxPlayers});
    $('#pvMotd').textContent = S.props.motd;
    $('#iconRemove').hidden = !s.icon;
    $('#iconLogo').hidden = !s.logoUrl;
  }
  async function changeIcon(run, removed){
    const s = srv(); if (!s) return;
    try {
      const url = await run(s.id);
      if (url === '' && !removed) return; // the user cancelled the dialog
      s.icon = removed ? '' : url;
      renderIcon(); renderSidebar(); tile($('#hTile'), s.name, s.icon || s.logoUrl);
      toast(t(removed ? 'toastIconRemoved' : 'toastIconSaved'));
      if (s.state === 'running' || s.state === 'starting'){ $('#propsNote').hidden = false; }
    } catch (err){ toastErr(err); }
  }
  $('#iconPick').onclick = $('#pIcon').onclick = () => changeIcon(id => api().PickServerIcon(id));
  $('#iconLogo').onclick = () => changeIcon(id => api().UseModpackLogoAsIcon(id));
  $('#iconRemove').onclick = () => changeIcon(id => api().RemoveServerIcon(id).then(() => ''), true);

  /* Advanced settings: every other key in server.properties */
  const schemaByKey = Object.fromEntries(PROP_SCHEMA.map(k => [k.key, k]));
  // The rows shown for this server: every known key, plus keys it has that
  // are not known (added by mods or a newer Minecraft).
  function advancedKeys(){
    const unknown = Object.keys(S.propsSaved ? S.propsSaved.other : {}).filter(k => !schemaByKey[k]).sort()
      .map(k => ({key: k, group: 'other', type: 'text', def: '', unknown: true}));
    return PROP_SCHEMA.concat(unknown);
  }
  const advValue = k => (k.key in S.props.other ? S.props.other[k.key] : k.def);
  function controlHTML(k, id){
    const v = advValue(k);
    if (k.type === 'bool') return `<span class="switch"><input type="checkbox" id="${id}" ${v === 'true' ? 'checked' : ''}><i></i></span>`;
    if (k.type === 'int') return `<input class="input num-in" id="${id}" type="number" inputmode="numeric" min="${k.min}" max="${k.max}" value="${esc(v)}">`;
    if (k.type === 'enum') return `<select class="input" id="${id}">${k.options.map(o => `<option ${o === v ? 'selected' : ''}>${esc(o)}</option>`).join('')}</select>`;
    const list = k.suggest ? ` list="${id}-list"` : '';
    const opts = k.suggest ? `<datalist id="${id}-list">${k.suggest.map(o => `<option value="${esc(o)}">`).join('')}</datalist>` : '';
    return `<input class="input" id="${id}" type="${k.type === 'secret' ? 'password' : 'text'}" value="${esc(v)}" autocomplete="off"${list}>${opts}`;
  }
  function buildAdvanced(){
    const keys = advancedKeys();
    $('#advGroups').innerHTML = PROP_GROUPS.map(g => {
      const rows = keys.filter(k => k.group === g.id);
      if (!rows.length) return '';
      return `<section class="adv-group" data-group="${g.id}"><h4><span class="ms">${g.icon}</span><span>${esc(g[lang] || g.en)}</span></h4>${rows.map(k => {
        const [label, help] = k.unknown ? [k.key, t('advUnknown')] : (k[lang] || k.en);
        const id = 'adv-' + k.key.replace(/[^a-z0-9]/gi, '_');
        const wide = k.type === 'text' || k.type === 'secret';
        const search = (label + ' ' + k.key + ' ' + help).toLowerCase();
        return `<div class="prow${wide ? ' wide' : ''}" data-key="${esc(k.key)}" data-search="${esc(search)}">
          <div><label for="${id}"><b>${esc(label)}</b></label><span class="pkey">${esc(k.key)}</span><div class="help">${esc(help)}</div></div>
          <div>${controlHTML(k, id)}</div></div>`;
      }).join('')}</section>`;
    }).join('');
    filterAdvanced();
  }
  // Refreshes values and the changed markers without rebuilding the rows.
  function renderAdvanced(){
    if (!S.props) return;
    $$('#advGroups .prow').forEach(row => {
      const k = schemaByKey[row.dataset.key] || {key: row.dataset.key, type: 'text', def: ''};
      const el = row.querySelector('input,select');
      const v = advValue(k);
      if (document.activeElement !== el){
        if (k.type === 'bool') el.checked = v === 'true'; else el.value = v;
      }
      const saved = k.key in S.propsSaved.other ? S.propsSaved.other[k.key] : k.def;
      row.classList.toggle('changed', v !== saved);
    });
  }
  function setAdvanced(key, value){
    const k = schemaByKey[key] || {key, def: ''};
    // Leave keys that are not in the file out of it while they hold the default.
    if (!(key in S.propsSaved.other) && value === k.def) delete S.props.other[key];
    else S.props.other[key] = value;
    renderProps();
  }
  function onAdvancedInput(e){
    const row = e.target.closest('.prow'); if (!row) return;
    const k = schemaByKey[row.dataset.key] || {type: 'text'};
    let v = k.type === 'bool' ? String(e.target.checked) : e.target.value;
    if (k.type === 'int'){
      if (e.type === 'input') return; // wait for the whole number
      const n = parseInt(v, 10);
      v = String(Number.isNaN(n) ? k.def : Math.max(k.min, Math.min(k.max, n)));
      e.target.value = v;
    }
    setAdvanced(row.dataset.key, v);
  }
  $('#advGroups').addEventListener('input', onAdvancedInput);
  $('#advGroups').addEventListener('change', onAdvancedInput);
  function filterAdvanced(){
    const q = $('#propsSearch').value.trim().toLowerCase();
    let shown = 0;
    $$('#advGroups .adv-group').forEach(g => {
      let n = 0;
      g.querySelectorAll('.prow').forEach(r => { const hit = !q || r.dataset.search.includes(q); r.hidden = !hit; if (hit) n++; });
      g.hidden = n === 0; shown += n;
    });
    $('#advEmpty').hidden = shown > 0;
    $('#advEmpty').textContent = t('advNone', {q});
    if (q) $('#propsAdv').open = true;
  }
  $('#propsSearch').addEventListener('input', filterAdvanced);
  $('#pMode').addEventListener('click', e => { const b = e.target.closest('[data-v]'); if (b) setProp('gamemode', b.dataset.v); });
  $('#pDiff').addEventListener('click', e => { const b = e.target.closest('[data-v]'); if (b) setProp('difficulty', b.dataset.v); });
  $('#propsUndo').onclick = () => {
    S.props = {...S.propsSaved, other: {...S.propsSaved.other}, id: S.props.id};
    $('#pMotd').value = S.props.motd;
    $$('#advGroups .prow input, #advGroups .prow select').forEach(el => el.blur());
    renderProps();
  };
  $('#propsFile').onclick = () => api().OpenServerProperties(S.current);
  $('#propsForm').addEventListener('submit', async e => {
    e.preventDefault();
    if (!propsDirty()) return;
    const {id, ...values} = S.props;
    // Only changed advanced keys are sent; the server keeps every other line.
    const changed = {};
    for (const [k, v] of Object.entries(values.other)) if (S.propsSaved.other[k] !== v) changed[k] = v;
    $('#propsSave').disabled = true;
    try {
      await api().SetServerProperties(id, {...values, other: changed});
      S.propsSaved = {...values, other: {...values.other}};
      toast(t('toastPropsSaved'));
      const s = srv();
      $('#propsNote').hidden = !(s && s.state === 'running');
    } catch (err){ toastErr(err); }
    renderProps();
  });

  /* ------------------------------------------------ Maple updates */
  async function checkUpdate(manual){
    if (manual){ $('#updStatus').textContent = t('checking'); $('#checkUpd').disabled = true; }
    try {
      S.update = await api().CheckForUpdate();
      if (manual) $('#updStatus').textContent = S.update.dev ? t('versionDev') : S.update.available ? t('updAvailable', {v: S.update.latest}) : t('upToDate');
    } catch (err){
      if (manual) $('#updStatus').textContent = errText(parseErr(err));
    }
    if (manual) $('#checkUpd').disabled = false;
    renderUpdate();
    if (S.view === 'settings') renderSettings();
  }
  function renderUpdate(){
    const u = S.update, box = $('#updBox');
    box.hidden = !(u && u.available);
    if (box.hidden) return;
    if (!S.updating){
      $('#updText').textContent = t('updAvailable', {v: u.latest});
      $('#updBar').hidden = true;
      $('#updBtn').hidden = false; $('#updBtn').disabled = false;
    }
  }
  async function startUpdate(){
    if (S.updating) return;
    S.updating = true;
    $('#updBtn').hidden = true; $('#updBar').hidden = false; $('#updBar').firstElementChild.style.width = '0%';
    $('#updText').textContent = t('downloading', {p: 0});
    try {
      await api().DownloadUpdate();
      $('#updText').textContent = t('restarting');
      await win().RestartIntoUpdate();
    } catch (err){
      S.updating = false;
      const e = parseErr(err);
      toast(errText(e), true);
      if (e.code === 'update_no_permission' && S.update && S.update.pageUrl) api().OpenURL(S.update.pageUrl);
      renderUpdate();
    }
  }
  $('#updBtn').onclick = () => {
    const running = [...S.servers.values()].some(s => s.state === 'running' || s.state === 'starting');
    if (running){ $('#updDlg').hidden = false; Motion.pop($('#updDlg .modal')); $('#updDlgNo').focus(); } else startUpdate();
  };
  $('#updDlgNo').onclick = () => { $('#updDlg').hidden = true; };
  $('#updDlgYes').onclick = () => { $('#updDlg').hidden = true; startUpdate(); };

  /* ------------------------------------------------ server updates */
  function upsert(v){
    const prev = S.servers.get(v.id);
    S.servers.set(v.id, v);
    if (prev && prev.state !== v.state){
      if (v.state === 'running' && prev.state === 'starting') toast(t('toastOnline'));
      if (v.state === 'stopped' && S.stopping.has(v.id)){ S.stopping.delete(v.id); if (!S.quiet.has(v.id)) toast(t('toastStopped')); }
      if (v.state !== 'running' && v.state !== 'starting') S.stopping.delete(v.id);
    }
  }

  /* ------------------------------------------------ clicks, title bar, close */
  document.addEventListener('click', e => {
    const a = e.target.closest('[data-act]'); if (a && !a.disabled) act(a.dataset.act);
    const tb = e.target.closest('[data-tab]'); if (tb){ if (S.view !== 'server') show('server'); setTab(tb.dataset.tab); }
    const g = e.target.closest('[data-go]'); if (g){ if (g.dataset.go === 'new') openForm(null); else home(); }
    const o = e.target.closest('[data-open]'); if (o){ e.preventDefault(); api().OpenURL(o.dataset.open); }
    const c = e.target.closest('[data-copy]');
    if (c){
      const txt = $('#' + c.dataset.copy).textContent.trim();
      const done = () => toast(t('toastCopied', {a: txt}));
      (window.runtime && window.runtime.ClipboardSetText ? window.runtime.ClipboardSetText(txt) : navigator.clipboard.writeText(txt)).then(done, done);
    }
    const w = e.target.closest('[data-win]');
    if (w){
      if (w.dataset.win === 'min') win().Minimise();
      if (w.dataset.win === 'max') win().ToggleMaximise().then(syncMax);
      if (w.dataset.win === 'close') win().Close();
    }
  });
  async function syncMax(){ try { $('#maxIcon').textContent = await win().IsMaximised() ? 'filter_none' : 'crop_square'; } catch {} }
  addEventListener('resize', () => { clearTimeout(syncMax.t); syncMax.t = setTimeout(syncMax, 200); });

  $('#newBtn').onclick = () => openForm(null);
  $('#settingsBtn').onclick = () => show('settings');

  $$('[role=tab]').forEach(b => b.addEventListener('keydown', e => {
    const tabs = $$('[role=tab]'), i = tabs.indexOf(b);
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft'){ const n = tabs[(i + (e.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length]; n.focus(); setTab(n.dataset.tab); }
  }));

  $('#closeKeep').onclick = () => { $('#closeDlg').hidden = true; };
  $('#closeStop').onclick = () => {
    const b = $('#closeStop'); b.disabled = true; b.lastElementChild.textContent = t('stopping');
    win().StopServersAndQuit();
  };
  addEventListener('keydown', e => {
    if (e.key !== 'Escape') return;
    $('#closeDlg').hidden = true; $('#updDlg').hidden = true; $('#rmDlg').hidden = true; $('#renDlg').hidden = true;
  });

  /* ------------------------------------------------ remove a server */
  // Asks first. The folder stays unless the user picks the Recycle Bin or
  // permanent deletion; the button says so when it deletes for good.
  $('#rmBtn').onclick = () => openRemove(S.current);
  function openRemove(id){
    const s = S.servers.get(id); if (!s) return;
    S.rmId = id;
    if (s.state === 'running' || s.state === 'starting' || S.stopping.has(s.id)) return toast(t('rmStopFirst'), true);
    $('#rmDlgTitle').textContent = t('rmDlgTitle', {name: s.name});
    $('#rmPath').textContent = t('rmPath', {dir: s.dir});
    $('#rmDlg input[value=keep]').checked = true;
    rmChoiceChanged();
    $('#rmDlg').hidden = false; Motion.pop($('#rmDlg .modal')); $('#rmNo').focus();
  }
  const rmChoice = () => $('#rmDlg input[name=rmFolder]:checked').value;
  function rmChoiceChanged(){
    const del = rmChoice() === 'delete';
    $('#rmYesTxt').textContent = t(del ? 'rmYesDelete' : 'rmYes');
    $('#rmYes').classList.toggle('solid', del);
  }
  $$('#rmDlg input[name=rmFolder]').forEach(r => r.addEventListener('change', rmChoiceChanged));
  $('#rmNo').onclick = () => { $('#rmDlg').hidden = true; };
  $('#rmYes').onclick = async () => {
    const s = S.servers.get(S.rmId); if (!s) return;
    const folder = rmChoice(), btn = $('#rmYes');
    btn.disabled = true;
    try {
      await api().RemoveServer(s.id, folder);
      $('#rmDlg').hidden = true;
      S.servers.delete(s.id);
      if (S.current === s.id) S.current = null;
      // Forget what was loaded for the removed server, as switching does.
      S.modsFor = null; S.props = S.propsSaved = null;
      home();
      if (S.view === 'server') setTab('overview');
      toast(t({keep: 'toastServerRemoved', trash: 'toastServerTrashed', delete: 'toastServerDeleted'}[folder], {name: s.name}));
    } catch (err){
      toastErr(err);
    } finally {
      btn.disabled = false;
    }
  };

  /* ------------------------------------------------ server menu */
  // Right-click a server in the list (or press the menu key) for its
  // actions. Items that need a stopped server are greyed out while it runs.
  const menu = $('#ctxMenu');
  let menuFor = null, menuReturn = null, menuClosedBy = null;

  function menuItems(s){
    const busy = s.state === 'running' || s.state === 'starting' || S.stopping.has(s.id);
    const hint = busy ? t('stopFirstHint') : '';
    return [
      busy ? {icon: 'stop', label: t('menuStop'), run: () => act('stop', s.id), off: S.stopping.has(s.id)}
           : {icon: 'play_arrow', label: t('startServer'), run: () => act('start', s.id)},
      {icon: 'folder_open', label: t('openFolder'), run: () => api().OpenServerFolder(s.id)},
      {icon: 'tune', label: t('menuSettings'), run: () => openServer(s.id, 'props')},
      null,
      {icon: 'edit', label: t('menuRename'), run: () => openRename(s.id)},
      {icon: 'drive_file_move', label: t('menuMove'), run: () => moveServer(s.id), off: busy, hint},
      null,
      {icon: 'delete', label: t('menuRemove'), run: () => openRemove(s.id), off: busy, hint, danger: true},
    ];
  }

  // x, y is the menu's top-left corner, or its top-right with alignRight.
  function openMenu(id, x, y, from, alignRight = false){
    const s = S.servers.get(id); if (!s) return;
    closeMenu(false);
    menuFor = id; menuReturn = from;
    if (from && from.hasAttribute('aria-expanded')) from.setAttribute('aria-expanded', 'true');
    menu.setAttribute('aria-label', t('menuLabel', {name: s.name}));
    menu.innerHTML = '';
    const items = menuItems(s);
    for (const it of items){
      if (!it){ const hr = document.createElement('div'); hr.className = 'sep'; hr.setAttribute('role', 'separator'); menu.appendChild(hr); continue; }
      const b = document.createElement('button');
      b.type = 'button'; b.setAttribute('role', 'menuitem'); b.tabIndex = -1;
      if (it.danger) b.classList.add('danger');
      if (it.off){ b.disabled = true; b.setAttribute('aria-disabled', 'true'); }
      if (it.off && it.hint) b.title = it.hint;
      b.innerHTML = `<span class="ms">${it.icon}</span><span>${esc(it.label)}</span>`;
      b.onclick = () => { closeMenu(false); it.run(); };
      menu.appendChild(b);
    }
    menu.hidden = false;
    // Keep it on screen: open up or left when there is no room.
    const w = menu.offsetWidth, h = menu.offsetHeight, pad = 8;
    if (alignRight) x -= w;
    const left = Math.min(x, innerWidth - w - pad), top = y + h + pad > innerHeight ? Math.max(pad, y - h) : y;
    menu.style.left = Math.max(pad, left) + 'px';
    menu.style.top = top + 'px';
    menu.style.transformOrigin = `${alignRight ? w : x - left}px ${y >= top ? 0 : h}px`;
    if (Motion.scale()) menu.animate([{opacity: 0, transform: 'scale(.94)'}, {opacity: 1, transform: 'none'}],
      {duration: Motion.ms(170), easing: 'cubic-bezier(.2,1.2,.4,1)'});
    const first = menu.querySelector('[role=menuitem]:not(:disabled)');
    if (first) first.focus();
  }

  function closeMenu(restoreFocus = true){
    if (menu.hidden) return;
    menu.hidden = true; menuFor = null;
    if (menuReturn && menuReturn.hasAttribute('aria-expanded')) menuReturn.setAttribute('aria-expanded', 'false');
    if (restoreFocus && menuReturn && document.contains(menuReturn)) menuReturn.focus();
    menuReturn = null;
  }

  menu.addEventListener('keydown', e => {
    const items = [...menu.querySelectorAll('[role=menuitem]:not(:disabled)')];
    const i = items.indexOf(document.activeElement);
    const go = n => { e.preventDefault(); items[(n + items.length) % items.length].focus(); };
    if (e.key === 'ArrowDown') go(i + 1);
    else if (e.key === 'ArrowUp') go(i - 1);
    else if (e.key === 'Home') go(0);
    else if (e.key === 'End') go(items.length - 1);
    else if (e.key === 'Escape'){ e.preventDefault(); e.stopPropagation(); closeMenu(); }
    else if (e.key === 'Tab'){ e.preventDefault(); closeMenu(); }
  });
  document.addEventListener('pointerdown', e => {
    if (menu.hidden || menu.contains(e.target)) return;
    // A press on the ⋯ button that opened the menu closes it; its click
    // must not open it again.
    menuClosedBy = menuReturn && menuReturn.contains(e.target) && menuReturn.classList.contains('srv-more') ? menuReturn : null;
    closeMenu(false);
  }, true);
  document.addEventListener('contextmenu', e => { if (!menu.hidden && !e.target.closest('.srv-row')) closeMenu(false); });
  addEventListener('blur', () => closeMenu(false));
  addEventListener('resize', () => closeMenu(false));
  $('#srvList').addEventListener('scroll', () => closeMenu(false), {passive: true});

  /* rename */
  function openRename(id){
    const s = S.servers.get(id); if (!s) return;
    S.renId = id;
    $('#renName').value = s.name;
    $('#renDlg').hidden = false; Motion.pop($('#renDlg .modal'));
    $('#renName').focus(); $('#renName').select();
  }
  $('#renNo').onclick = () => { $('#renDlg').hidden = true; };
  $('#renForm').addEventListener('submit', async e => {
    e.preventDefault();
    const id = S.renId, btn = $('#renYes');
    btn.disabled = true;
    try {
      const v = await api().RenameServer(id, $('#renName').value);
      $('#renDlg').hidden = true;
      refreshServer(v);
      toast(t('toastRenamed', {name: v.name}));
    } catch (err){
      toastErr(err);
    } finally {
      btn.disabled = false;
    }
  });

  /* move */
  async function moveServer(id){
    const s = S.servers.get(id); if (!s || S.moving) return;
    let dest;
    try { dest = await api().PickFolder(s.dir.replace(/[\\/][^\\/]+$/, '')); } catch (err){ return toastErr(err); }
    if (!dest) return;
    S.moving = true;
    toast(t('moving', {name: s.name}), false, 60000);
    try {
      const v = await api().MoveServer(id, dest);
      refreshServer(v);
      toast(t('toastMoved', {name: v.name, dir: v.dir}), false, 6000);
    } catch (err){
      toastErr(err);
    } finally {
      S.moving = false;
    }
  }

  // Shows a server's new details wherever they appear.
  function refreshServer(v){
    const prev = S.servers.get(v.id);
    // The list's live state (running, players) is newer than this copy.
    S.servers.set(v.id, prev ? {...v, state: prev.state, players: prev.players} : v);
    renderSidebar();
    if (S.view === 'server' && S.current === v.id){ renderServer(); if (S.props) renderIcon(); }
  }

  /* ------------------------------------------------ start */
  async function init(){
    S.app = await api().State();
    const saved = S.app.settings.language;
    lang = saved === 'en' || saved === 'vi' ? saved : (navigator.language || '').toLowerCase().startsWith('vi') ? 'vi' : 'en';
    applyEffects();
    applyTheme(S.app.settings.theme);
    applyStatic();
    for (const s of S.app.servers) S.servers.set(s.id, s);
    S.current = S.app.servers.length ? S.app.servers[0].id : null;
    home(); setTab('overview'); syncMax();
    const pressed = b => b.getAttribute('aria-pressed') === 'true';
    Motion.attach($('.tabs'), '[role=tab]', b => b.getAttribute('aria-selected') === 'true');
    Motion.attach($('#srvList'), '.srv-row', r => r.querySelector('.srv').getAttribute('aria-current') === 'true');
    Motion.attach($('.lang-seg'), 'button', pressed);
    Motion.attach($('#p-mods .chips'), '.chip', pressed);
    $$('#v-settings .theme-seg').forEach(g => { g.classList.add('lens-accent'); Motion.attach(g, 'button', pressed); });
    ['#pMode', '#pDiff'].forEach(sel => { $(sel).classList.add('lens-accent'); Motion.attach($(sel), 'button', b => b.getAttribute('aria-checked') === 'true'); });
    document.fonts.ready.then(() => setTimeout(checkEffects, 600));

    const on = window.runtime.EventsOn;
    on('setup', onSetup);
    on('server', v => {
      upsert(v); renderSidebar();
      if (S.view === 'server' && v.id === S.current) renderServer();
    });
    on('server-log', ev => appendLog(ev.id, ev.lines || []));
    on('update', ev => {
      const p = ev.total ? Math.floor(ev.done / ev.total * 100) : 0;
      $('#updBar').firstElementChild.style.width = p + '%';
      $('#updText').textContent = t('downloading', {p});
    });
    if (!S.app.settings.skipUpdateCheck) checkUpdate(false);
    on('files-dropped', paths => {
      if (!paths || !paths.length) return;
      if (S.view === 'new'){ $('#packUrl').value = paths[0]; lookupPack(); return; }
      if (S.view === 'server' && S.tab === 'mods') addJars(paths);
      if (S.view === 'server' && S.tab === 'props'){
        const pic = paths.find(p => /\.(png|jpe?g|gif|webp|bmp|tiff?)$/i.test(p));
        if (pic) changeIcon(id => api().SetServerIconFromFile(id, pic));
        else toast(t('err_bad_picture'), true);
      }
    });
    on('close-requested', why => {
      const setup = why && why.setup && !(why.servers > 0);
      $('#closeTitle').textContent = t(setup ? 'closeSetupTitle' : 'closeTitle');
      $('#closeText').textContent = t(setup ? 'closeSetupText' : 'closeText');
      const b = $('#closeStop'); b.disabled = false; b.lastElementChild.textContent = t(setup ? 'closeSetupStop' : 'closeStop');
      $('#closeDlg').hidden = false; Motion.pop($('#closeDlg .modal')); $('#closeKeep').focus();
    });
  }

  // The Wails runtime and bindings load before this script; wait if not.
  (function boot(tries){
    if (window.go && window.go.app && window.runtime) init().catch(err => { document.body.insertAdjacentHTML('beforeend', `<pre class="details" style="position:fixed;bottom:12px;left:12px;right:12px;z-index:30">${esc(err)}</pre>`); });
    else if (tries < 100) setTimeout(() => boot(tries + 1), 50);
  })(0);
})();

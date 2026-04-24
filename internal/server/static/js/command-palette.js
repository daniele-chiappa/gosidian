// Command palette — Cmd+K / Ctrl+K fuzzy jump across notes, projects, tags
// and built-in actions. Zero dependencies; dataset fetched once from
// /api/command-palette and cached for the session.
(function () {
  const LS_KEY = 'gosidian.cmdp.recent';
  const MAX_RESULTS = 30;
  const MAX_RECENT = 5;

  let cached = null; // {notes, projects, tags}
  let overlay = null;
  let input = null;
  let list = null;
  let selectedIdx = 0;
  let currentResults = [];

  // i18n — strings injected by layout.html via window.GOSIDIAN_I18N.palette.
  // Fallback values kept in English so the palette stays usable if the
  // server-render of the dict ever breaks.
  const I18N = (window.GOSIDIAN_I18N && window.GOSIDIAN_I18N.palette) || {};
  function t(key, fallback) { return I18N[key] || fallback; }

  // Icons are Lucide SVG names. Fetched lazily from /static/icons/<name>.svg and
  // cached in iconCache at palette open. See gosidian/memory/conventions.md.
  const ACTIONS = [
    { label: t('action_graph_label',    'Go to Graph'),        desc: t('action_graph_desc',    'Open the graph view'),              icon: 'network',   run: () => location.href = '/graph' },
    { label: t('action_audit_label',    'Go to Audit log'),    desc: t('action_audit_desc',    'Chronological mutation log'),       icon: 'history',   run: () => location.href = '/audit' },
    { label: t('action_tags_label',     'Go to Tags'),         desc: t('action_tags_desc',     'Tag list with count'),              icon: 'tag',       run: () => location.href = '/tags' },
    { label: t('action_projects_label', 'Go to Projects'),     desc: t('action_projects_desc', 'Projects list'),                    icon: 'folder',    run: () => location.href = '/projects' },
    { label: t('action_trash_label',    'Trash'),              desc: t('action_trash_desc',    'Deleted notes'),                    icon: 'trash-2',   run: () => location.href = '/trash' },
    { label: t('action_settings_label', 'Settings'),           desc: t('action_settings_desc', 'Git, theme, config'),               icon: 'settings',  run: () => location.href = '/settings' },
    { label: t('action_focus_label',    'Toggle focus mode'),  desc: t('action_focus_desc',    'Hide topbar + sidebar'),            icon: 'maximize',  run: () => {
        document.body.classList.toggle('focus-mode');
        try { localStorage.setItem('gosidian.focus-mode', document.body.classList.contains('focus-mode') ? '1' : '0'); } catch (_) {}
    } },
    { label: t('action_new_note_label', 'New note'),           desc: t('action_new_note_desc', 'Create an empty note'),             icon: 'file-plus', run: () => location.href = '/notes/new' },
  ];

  // Format a "%d notes" style string with the actual count. Catalog values
  // use Go-style %d which we swap at render time.
  function fmtNotesCount(n) {
    const tmpl = t('notes_count', '%d notes');
    return tmpl.replace('%d', n);
  }

  const iconCache = Object.create(null);
  async function preloadIcon(name) {
    if (iconCache[name] !== undefined) return;
    try {
      const resp = await fetch('/static/icons/' + encodeURIComponent(name) + '.svg');
      iconCache[name] = resp.ok ? await resp.text() : '';
    } catch (_) {
      iconCache[name] = '';
    }
  }
  async function preloadIcons(names) {
    await Promise.all([...new Set(names)].map(preloadIcon));
  }

  function loadRecent() {
    try { return JSON.parse(localStorage.getItem(LS_KEY) || '[]'); } catch (_) { return []; }
  }
  function saveRecent(arr) {
    try { localStorage.setItem(LS_KEY, JSON.stringify(arr.slice(0, MAX_RECENT))); } catch (_) {}
  }
  function pushRecent(entry) {
    const filtered = loadRecent().filter((e) => e.key !== entry.key);
    filtered.unshift(entry);
    saveRecent(filtered);
  }

  async function loadDataset() {
    if (cached) return cached;
    try {
      const resp = await fetch('/api/command-palette');
      if (!resp.ok) throw new Error('bad status');
      cached = await resp.json();
    } catch (_) {
      cached = { notes: [], projects: [], tags: [] };
    }
    return cached;
  }

  function score(text, q) {
    if (!q) return 0;
    const t = text.toLowerCase();
    const idx = t.indexOf(q);
    if (idx < 0) return -1;
    // Prefer matches near the start and shorter strings.
    return 1000 - idx - Math.min(500, t.length);
  }

  function buildEntries(data) {
    const entries = [];
    const catAction  = t('category_action',  'action');
    const catProject = t('category_project', 'project');
    const catTag     = t('category_tag',     'tag');
    const catNote    = t('category_note',    'note');
    for (const a of ACTIONS) {
      entries.push({
        key: 'action:' + a.label,
        icon: a.icon,
        title: a.label,
        subtitle: a.desc,
        category: catAction,
        run: a.run,
      });
    }
    for (const p of data.projects || []) {
      entries.push({
        key: 'project:' + p.name,
        icon: 'folder',
        title: p.name,
        subtitle: fmtNotesCount(p.noteCount),
        category: catProject,
        run: () => location.href = '/projects/' + encodeURIComponent(p.name),
      });
    }
    for (const tg of data.tags || []) {
      entries.push({
        key: 'tag:' + tg.tag,
        icon: 'tag',
        title: '#' + tg.tag,
        subtitle: fmtNotesCount(tg.count),
        category: catTag,
        run: () => location.href = '/tags/' + encodeURIComponent(tg.tag),
      });
    }
    for (const n of data.notes || []) {
      entries.push({
        key: 'note:' + n.path,
        icon: 'file',
        title: n.title || n.path,
        subtitle: n.path,
        category: catNote,
        run: () => location.href = '/notes/' + encodeURI(n.path),
      });
    }
    return entries;
  }

  function filterEntries(entries, q) {
    if (!q) {
      // Recent first, then first N entries.
      const recent = loadRecent();
      const seen = new Set(recent.map((e) => e.key));
      const result = recent
        .map((r) => entries.find((e) => e.key === r.key))
        .filter(Boolean);
      for (const e of entries) {
        if (result.length >= MAX_RESULTS) break;
        if (seen.has(e.key)) continue;
        result.push(e);
      }
      return result;
    }
    const ql = q.toLowerCase();
    const scored = [];
    for (const e of entries) {
      const s = Math.max(score(e.title, ql), score(e.subtitle, ql) - 200);
      if (s >= 0) scored.push({ e, s });
    }
    scored.sort((a, b) => b.s - a.s);
    return scored.slice(0, MAX_RESULTS).map((x) => x.e);
  }

  function renderResults() {
    if (!list) return;
    list.innerHTML = '';
    currentResults.forEach((e, i) => {
      const el = document.createElement('div');
      el.className = 'cmdp-item' + (i === selectedIdx ? ' selected' : '');
      el.setAttribute('data-idx', String(i));
      el.innerHTML =
        `<span class="cmdp-icon">${iconCache[e.icon] || ''}</span>` +
        `<div class="cmdp-text">` +
        `<div class="cmdp-title">${escapeHTML(e.title)}</div>` +
        `<div class="cmdp-sub">${escapeHTML(e.subtitle || '')}</div>` +
        `</div>` +
        `<span class="cmdp-cat">${escapeHTML(e.category)}</span>`;
      el.addEventListener('mousemove', () => {
        if (selectedIdx !== i) {
          selectedIdx = i;
          renderResults();
        }
      });
      el.addEventListener('click', () => run(i));
      list.appendChild(el);
    });
    // Scroll selection into view.
    const sel = list.querySelector('.cmdp-item.selected');
    sel?.scrollIntoView({ block: 'nearest' });
  }

  function run(idx) {
    const entry = currentResults[idx];
    if (!entry) return;
    pushRecent({ key: entry.key });
    close();
    entry.run();
  }

  async function update() {
    const data = await loadDataset();
    const entries = buildEntries(data);
    currentResults = filterEntries(entries, input.value.trim().toLowerCase());
    await preloadIcons(currentResults.map((e) => e.icon));
    selectedIdx = 0;
    renderResults();
  }

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c]));
  }

  function build() {
    overlay = document.createElement('div');
    overlay.className = 'cmdp-overlay';
    const ph   = escapeHTML(t('placeholder',    'Search notes, projects, tags, actions…'));
    const hNav = escapeHTML(t('hint_navigate',  '↑↓ navigate'));
    const hRun = escapeHTML(t('hint_execute',   '↵ execute'));
    const hClo = escapeHTML(t('hint_close',     'Esc close'));
    const hRel = escapeHTML(t('hint_reload',    'Ctrl+⇧+K reload'));
    overlay.innerHTML =
      `<div class="cmdp-box" role="dialog" aria-label="Command palette">
        <input type="text" class="cmdp-input" placeholder="${ph}" autocomplete="off" spellcheck="false">
        <div class="cmdp-list" role="listbox"></div>
        <div class="cmdp-hint">
          <span>${hNav}</span><span>${hRun}</span><span>${hClo}</span><span>${hRel}</span>
        </div>
      </div>`;
    overlay.addEventListener('click', (e) => {
      if (e.target === overlay) close();
    });
    document.body.appendChild(overlay);
    input = overlay.querySelector('.cmdp-input');
    list = overlay.querySelector('.cmdp-list');
    input.addEventListener('input', update);
    input.addEventListener('keydown', (e) => {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        selectedIdx = Math.min(currentResults.length - 1, selectedIdx + 1);
        renderResults();
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        selectedIdx = Math.max(0, selectedIdx - 1);
        renderResults();
      } else if (e.key === 'Enter') {
        e.preventDefault();
        run(selectedIdx);
      } else if (e.key === 'Escape') {
        e.preventDefault();
        close();
      }
    });
  }

  async function open() {
    if (!overlay) build();
    overlay.classList.add('open');
    input.value = '';
    await update();
    input.focus();
  }

  function close() {
    if (overlay) overlay.classList.remove('open');
  }

  function init() {
    window.addEventListener('keydown', (e) => {
      const meta = e.metaKey || e.ctrlKey;
      if (meta && e.key === 'k' && !e.shiftKey && !e.altKey) {
        e.preventDefault();
        if (overlay && overlay.classList.contains('open')) close();
        else open();
        return;
      }
      if (meta && e.shiftKey && (e.key === 'K' || e.key === 'k')) {
        e.preventDefault();
        cached = null; // force reload on next open
        open();
      }
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();

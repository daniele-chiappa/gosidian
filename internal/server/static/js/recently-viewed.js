// Track recently-viewed notes in localStorage and render a widget on the
// home page. Privacy: purely client-side, nothing sent to the server.
(function () {
  const LS_KEY = 'gosidian.recent';
  const MAX = 10;

  function load() {
    try {
      return JSON.parse(localStorage.getItem(LS_KEY) || '[]');
    } catch (_) {
      return [];
    }
  }
  function save(arr) {
    try { localStorage.setItem(LS_KEY, JSON.stringify(arr.slice(0, MAX))); } catch (_) {}
  }

  function currentNotePath() {
    const p = window.location.pathname;
    if (!p.startsWith('/notes/')) return '';
    // Exclude sub-routes like /edit, /history, /rename, etc.
    const rest = p.slice('/notes/'.length);
    const tail = rest.split('/').pop() || '';
    if (!tail.endsWith('.md') && !rest.endsWith('.md')) return '';
    // Drop trailing /edit, /history etc by finding the .md segment.
    const idx = rest.lastIndexOf('.md');
    if (idx < 0) return '';
    return decodeURIComponent(rest.slice(0, idx + 3));
  }

  function track() {
    const path = currentNotePath();
    if (!path) return;
    const title = (document.querySelector('article.note h1')?.textContent || path).trim();
    const list = load().filter((e) => e.path !== path);
    list.unshift({ path, title, ts: Date.now() });
    save(list);
  }

  function renderWidget() {
    const target = document.getElementById('recently-viewed');
    if (!target) return;
    const list = load();
    if (!list.length) {
      target.innerHTML = '';
      return;
    }
    const items = list
      .map((e) =>
        `<li><a href="/notes/${encodeURI(e.path)}">${escapeHTML(e.title)}</a>` +
        `<span class="muted"> ${escapeHTML(e.path)}</span></li>`
      )
      .join('');
    target.innerHTML =
      '<h2>Aperte di recente</h2><ul class="note-list">' + items + '</ul>';
  }

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c]));
  }

  function init() {
    track();
    renderWidget();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();

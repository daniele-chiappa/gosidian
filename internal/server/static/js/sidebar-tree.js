// Sidebar tree v2 — behaviour:
//
//   1. Auto-expand the path of the currently-viewed note + add .active class
//      to the matching <a>.
//   2. Persist expanded state in localStorage so the chosen branches stay
//      open across navigation + refresh.
//   3. Live filter: type to hide non-matching items; matches auto-expand
//      their ancestors.
//   4. Expand-all / collapse-all buttons in the toolbar.
//
// Loaded globally via layout.html and re-invoked on HTMX swap so the
// partial tree (fetched by `hx-get="/api/tree" hx-trigger="load"`) gets
// wired up as soon as it lands in the DOM.
(function () {
  const LS_KEY = 'gosidian.tree.expanded';

  function loadExpanded() {
    try {
      return new Set(JSON.parse(localStorage.getItem(LS_KEY) || '[]'));
    } catch (_) {
      return new Set();
    }
  }
  function saveExpanded(set) {
    try {
      localStorage.setItem(LS_KEY, JSON.stringify([...set]));
    } catch (_) { /* quota / private mode */ }
  }

  function currentNotePath() {
    const p = window.location.pathname;
    if (p.startsWith('/notes/')) {
      return decodeURIComponent(p.slice('/notes/'.length));
    }
    return '';
  }

  function init(root) {
    if (!root || root.dataset.treeInit === '1') return;
    root.dataset.treeInit = '1';

    const expanded = loadExpanded();

    // Apply saved expand state.
    root.querySelectorAll('li.tree-item[data-path]').forEach((li) => {
      const d = li.querySelector(':scope > details');
      if (d && expanded.has(li.dataset.path)) d.open = true;
    });

    // Highlight + auto-expand ancestors of the current note.
    const currentPath = window.location.pathname;
    const activeLink = root.querySelector(`a.tree-link[href="${CSS.escape(currentPath)}"]`);
    if (activeLink) {
      activeLink.classList.add('active');
      let d = activeLink.closest('details');
      while (d) {
        d.open = true;
        const li = d.closest('li.tree-item');
        if (li && li.dataset.path) expanded.add(li.dataset.path);
        d = d.parentElement ? d.parentElement.closest('details') : null;
      }
      saveExpanded(expanded);
      activeLink.scrollIntoView({ block: 'nearest', behavior: 'auto' });
    }

    // Persist on toggle.
    root.addEventListener('toggle', (e) => {
      if (e.target.tagName !== 'DETAILS') return;
      const li = e.target.closest('li.tree-item');
      if (!li) return;
      const path = li.dataset.path;
      if (!path) return;
      const cur = loadExpanded();
      if (e.target.open) cur.add(path);
      else cur.delete(path);
      saveExpanded(cur);
    }, true);

    // Expand-all / collapse-all.
    const expandAllBtn = root.querySelector('[data-act="expand-all"]');
    const collapseAllBtn = root.querySelector('[data-act="collapse-all"]');
    expandAllBtn?.addEventListener('click', () => {
      const cur = loadExpanded();
      root.querySelectorAll('details').forEach((d) => {
        d.open = true;
        const li = d.closest('li.tree-item');
        if (li && li.dataset.path) cur.add(li.dataset.path);
      });
      saveExpanded(cur);
    });
    collapseAllBtn?.addEventListener('click', () => {
      root.querySelectorAll('details').forEach((d) => { d.open = false; });
      saveExpanded(new Set());
    });

    // Filter.
    const filterInput = document.getElementById('tree-filter-input');
    filterInput?.addEventListener('input', (e) => {
      const q = e.target.value.trim().toLowerCase();
      const all = root.querySelectorAll('li.tree-item');
      if (!q) {
        all.forEach((li) => li.classList.remove('filter-hidden'));
        return;
      }
      // Hide everything first.
      all.forEach((li) => li.classList.add('filter-hidden'));
      // Show items whose data-name (lowercase filename) includes the query,
      // plus all their ancestors (expanded).
      const escQ = q.replace(/["\\]/g, '\\$&');
      root.querySelectorAll(`li.tree-item[data-name*="${escQ}"]`).forEach((li) => {
        li.classList.remove('filter-hidden');
        let el = li.parentElement;
        while (el && el !== root) {
          if (el.classList && el.classList.contains('tree-item')) {
            el.classList.remove('filter-hidden');
            const d = el.querySelector(':scope > details');
            if (d) d.open = true;
          }
          el = el.parentElement;
        }
      });
    });
  }

  function findAndInit() {
    const root = document.querySelector('.tree-wrap');
    if (root) init(root);
  }

  // HTMX inserts the tree partial on load — re-init when that happens.
  document.addEventListener('htmx:afterSwap', () => findAndInit());
  // Also for direct page load in case the tree is pre-rendered inline.
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', findAndInit);
  } else {
    findAndInit();
  }
})();

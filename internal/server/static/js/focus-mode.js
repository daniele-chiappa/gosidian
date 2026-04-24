// Focus / reading mode: hide topbar + sidebar, widen content. Toggle via F
// keyboard shortcut (outside inputs) or the floating button. State persisted
// in localStorage so it sticks across navigation.
(function () {
  const LS_KEY = 'gosidian.focus-mode';

  function apply(on) {
    document.body.classList.toggle('focus-mode', !!on);
    try { localStorage.setItem(LS_KEY, on ? '1' : '0'); } catch (_) {}
    const btn = document.querySelector('.focus-toggle');
    if (btn) btn.setAttribute('aria-pressed', on ? 'true' : 'false');
  }

  function init() {
    let on = false;
    try { on = localStorage.getItem(LS_KEY) === '1'; } catch (_) {}
    if (on) apply(true);

    const btn = document.querySelector('.focus-toggle');
    btn?.addEventListener('click', () => {
      apply(!document.body.classList.contains('focus-mode'));
    });

    window.addEventListener('keydown', (e) => {
      // Only global shortcuts; ignore when typing.
      if (e.target.matches('input, textarea, select, [contenteditable="true"]')) return;
      if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
      if (e.key === 'f' || e.key === 'F') {
        // /graph uses F for fit; if a cytoscape canvas is on the page, skip.
        if (document.querySelector('#cy')) return;
        e.preventDefault();
        apply(!document.body.classList.contains('focus-mode'));
      }
      if (e.key === 'Escape' && document.body.classList.contains('focus-mode')) {
        apply(false);
      }
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();

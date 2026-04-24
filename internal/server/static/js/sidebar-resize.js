// Draggable sidebar width. Persists in localStorage. Double-click the
// resizer handle to reset to the default. Width is clamped to [180, 560]px.
(function () {
  const LS_KEY = 'gosidian.sidebar-width';
  const DEFAULT = 260;
  const MIN = 180;
  const MAX = 560;

  function clamp(n) {
    return Math.max(MIN, Math.min(MAX, n));
  }

  function apply(w) {
    document.documentElement.style.setProperty('--sidebar-width', w + 'px');
  }

  function loadStored() {
    try {
      const v = parseInt(localStorage.getItem(LS_KEY) || '', 10);
      if (!Number.isFinite(v)) return null;
      return clamp(v);
    } catch (_) {
      return null;
    }
  }

  function save(w) {
    try { localStorage.setItem(LS_KEY, String(w)); } catch (_) {}
  }

  function init() {
    // Restore saved width on page load.
    const stored = loadStored();
    if (stored != null) apply(stored);

    const handle = document.querySelector('.sidebar-resizer');
    if (!handle) return;

    let dragging = false;
    let rafId = 0;
    let pendingWidth = stored || DEFAULT;

    handle.addEventListener('mousedown', (e) => {
      if (e.button !== 0) return;
      e.preventDefault();
      dragging = true;
      handle.classList.add('dragging');
      document.body.classList.add('resizing-sidebar');
    });

    window.addEventListener('mousemove', (e) => {
      if (!dragging) return;
      pendingWidth = clamp(e.clientX);
      if (rafId) return;
      rafId = requestAnimationFrame(() => {
        rafId = 0;
        apply(pendingWidth);
      });
    });

    window.addEventListener('mouseup', () => {
      if (!dragging) return;
      dragging = false;
      handle.classList.remove('dragging');
      document.body.classList.remove('resizing-sidebar');
      save(pendingWidth);
    });

    // Double-click resets to default.
    handle.addEventListener('dblclick', () => {
      apply(DEFAULT);
      save(DEFAULT);
    });

    // Touch support (basic).
    handle.addEventListener('touchstart', (e) => {
      if (e.touches.length !== 1) return;
      dragging = true;
      handle.classList.add('dragging');
      document.body.classList.add('resizing-sidebar');
    }, { passive: true });

    window.addEventListener('touchmove', (e) => {
      if (!dragging || e.touches.length !== 1) return;
      pendingWidth = clamp(e.touches[0].clientX);
      if (rafId) return;
      rafId = requestAnimationFrame(() => {
        rafId = 0;
        apply(pendingWidth);
      });
    }, { passive: true });

    window.addEventListener('touchend', () => {
      if (!dragging) return;
      dragging = false;
      handle.classList.remove('dragging');
      document.body.classList.remove('resizing-sidebar');
      save(pendingWidth);
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();

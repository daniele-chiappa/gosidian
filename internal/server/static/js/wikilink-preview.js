// Hover preview for [[wiki-links]]. Attaches to any <a.wikilink> that has a
// data-preview-path attribute — i.e. resolved links. Unresolved links skip.
//
// Uses event delegation on document so it survives HTMX swaps.
(function () {
  const CACHE = new Map(); // path → { title, excerpt }
  let popoverEl = null;
  let hoverTimer = null;
  let lastTarget = null;

  function createPopover() {
    if (popoverEl) return popoverEl;
    popoverEl = document.createElement('div');
    popoverEl.className = 'wikilink-preview';
    popoverEl.setAttribute('role', 'tooltip');
    popoverEl.style.display = 'none';
    document.body.appendChild(popoverEl);
    return popoverEl;
  }

  function hidePopover() {
    if (popoverEl) popoverEl.style.display = 'none';
    lastTarget = null;
  }

  function positionPopover(targetEl) {
    const pop = popoverEl;
    if (!pop) return;
    const rect = targetEl.getBoundingClientRect();
    const popRect = pop.getBoundingClientRect();
    const margin = 8;

    let top = rect.bottom + window.scrollY + margin;
    let left = rect.left + window.scrollX;

    // Flip to above if it would overflow the viewport bottom.
    if (rect.bottom + popRect.height + margin > window.innerHeight) {
      top = rect.top + window.scrollY - popRect.height - margin;
    }
    // Clamp to viewport right edge.
    const maxLeft = window.scrollX + window.innerWidth - popRect.width - margin;
    if (left > maxLeft) left = maxLeft;
    if (left < window.scrollX + margin) left = window.scrollX + margin;

    pop.style.top = top + 'px';
    pop.style.left = left + 'px';
  }

  function showPopover(targetEl, data) {
    const pop = createPopover();
    pop.innerHTML =
      '<strong class="wlp-title"></strong>' +
      '<div class="wlp-path"></div>' +
      '<div class="wlp-excerpt"></div>';
    pop.querySelector('.wlp-title').textContent = data.title || '';
    pop.querySelector('.wlp-path').textContent = data.path || '';
    pop.querySelector('.wlp-excerpt').textContent = data.excerpt || '';
    pop.style.display = 'block';
    positionPopover(targetEl);
  }

  async function fetchPreview(path) {
    if (CACHE.has(path)) return CACHE.get(path);
    try {
      const resp = await fetch('/api/note-excerpt?path=' + encodeURIComponent(path));
      if (!resp.ok) return null;
      const data = await resp.json();
      CACHE.set(path, data);
      return data;
    } catch (_) {
      return null;
    }
  }

  document.addEventListener('mouseover', (e) => {
    const a = e.target.closest('a.wikilink[data-preview-path]');
    if (!a) return;
    if (a === lastTarget) return;
    lastTarget = a;
    clearTimeout(hoverTimer);
    hoverTimer = setTimeout(async () => {
      const path = a.getAttribute('data-preview-path');
      const data = await fetchPreview(path);
      if (!data) return;
      // Only show if the cursor is still on this link.
      if (lastTarget !== a) return;
      showPopover(a, data);
    }, 280);
  });

  document.addEventListener('mouseout', (e) => {
    const a = e.target.closest('a.wikilink[data-preview-path]');
    if (!a) return;
    clearTimeout(hoverTimer);
    hidePopover();
  });

  // Hide on scroll / navigation.
  window.addEventListener('scroll', hidePopover, { passive: true });
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') hidePopover();
  });
})();

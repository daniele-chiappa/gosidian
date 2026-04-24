(async function () {
  // Register the f-CoSE layout plugin (no-op if already registered).
  if (typeof cytoscapeFcose !== 'undefined') {
    try { cytoscape.use(cytoscapeFcose); } catch (_) { /* already registered */ }
  }

  const container = document.getElementById('cy');
  const countEl   = document.getElementById('graph-count');
  const selectEl  = document.getElementById('graph-project-filter');

  // Midnight Luxury palette — keep in sync with :root{} in app.css.
  const C = {
    bgBase:    '#0B0C10',
    bgElev1:   '#1F2833',
    bgElev2:   '#2A3340',
    textMuted: '#C5C6C7',
    cool:      '#66FCF1',
    gold:      '#C5A021',
    edgeDim:   '#3A4350',
  };

  function currentProject() {
    return new URLSearchParams(window.location.search).get('project') || '';
  }

  function populateSelect(projects, selected) {
    if (!selectEl) return;
    // Reset options, keep the "all projects" placeholder.
    while (selectEl.options.length > 1) selectEl.remove(1);
    (projects || []).forEach((p) => {
      const opt = document.createElement('option');
      opt.value = p;
      opt.textContent = p;
      if (p === selected) opt.selected = true;
      selectEl.appendChild(opt);
    });
    if (!selected) selectEl.value = '';
  }

  function buildStyle(maxDegree, maxCount) {
    const degRange = Math.max(1, maxDegree);
    const cntRange = Math.max(1, maxCount);
    return [
      { selector: 'node', style: {
          'background-color':  C.bgElev1,
          'border-color':      C.cool,
          'border-width':      1,
          'label':             'data(label)',
          'color':             C.textMuted,
          'font-size':         '11px',
          'text-valign':       'bottom',
          'text-margin-y':     4,
          'width':             `mapData(degree, 0, ${degRange}, 10, 44)`,
          'height':            `mapData(degree, 0, ${degRange}, 10, 44)`,
          'text-outline-color': C.bgBase,
          'text-outline-width': 2,
          'text-opacity':      `mapData(degree, 0, ${degRange}, 0.55, 1)`
      }},
      { selector: 'edge', style: {
          'width':             `mapData(count, 1, ${cntRange}, 1, 4)`,
          'line-color':        `mapData(count, 1, ${cntRange}, ${C.edgeDim}, ${C.cool})`,
          'target-arrow-shape':'none',
          'curve-style':       'straight',
          'opacity':           0.8
      }},
      { selector: '.faded', style: { 'opacity': 0.08, 'text-opacity': 0.08 } },
      { selector: 'node.highlight', style: {
          'background-color':  C.gold,
          'border-color':      '#F5F6F7',
          'border-width':      2,
          'color':             '#F5F6F7'
      }},
      { selector: 'edge.highlight', style: {
          'line-color':        C.gold,
          'opacity':           1,
          'width':             3
      }}
    ];
  }

  function layoutConfig(animate) {
    const hasFcose = typeof cytoscapeFcose !== 'undefined';
    if (hasFcose) {
      return {
        name:                  'fcose',
        quality:               'proof',
        randomize:             true,
        animate:               !!animate,
        animationDuration:     animate ? 400 : 0,
        nodeSeparation:        90,
        idealEdgeLength:       100,
        nodeRepulsion:         15000,
        gravity:               0.25,
        gravityRangeCompound:  1.5,
        packComponents:        true,
        tile:                  true
      };
    }
    // Fallback if the CDN script didn't load: tune vanilla cose.
    return {
      name:           'cose',
      animate:        !!animate,
      nodeRepulsion:  20000,
      idealEdgeLength:100,
      nodeOverlap:    24,
      gravity:        40,
      numIter:        2000
    };
  }

  function setStats(n, e) {
    if (countEl) countEl.textContent = `${n} nodi · ${e} archi`;
  }

  // Initial load.
  let cy = null;

  async function loadGraph(project) {
    const q = project ? `?project=${encodeURIComponent(project)}` : '';
    const resp = await fetch('/api/graph' + q);
    const payload = await resp.json();
    const elements = Array.isArray(payload.elements) ? payload.elements : [];
    populateSelect(payload.projects || [], payload.selected || '');

    if (elements.length === 0) {
      container.innerHTML =
        '<p style="padding:1rem;color:#8A8E91">Nessun nodo nel grafo.</p>';
      setStats(0, 0);
      return;
    }
    container.innerHTML = '';

    cy = cytoscape({
      container,
      elements,
      minZoom: 0.2,
      maxZoom: 3,
      wheelSensitivity: 0.2,
      style: buildStyle(payload.maxDegree || 1, payload.maxCount || 1),
      layout: layoutConfig(false)
    });

    setStats(cy.nodes().length, cy.edges().length);
    cy.ready(() => cy.fit(null, 40));

    // Highlight neighborhood on hover.
    cy.on('mouseover', 'node', (evt) => {
      const n = evt.target;
      cy.elements().addClass('faded');
      const nb = n.closedNeighborhood();
      nb.removeClass('faded').addClass('highlight');
    });
    cy.on('mouseout', 'node', () => {
      cy.elements().removeClass('faded highlight');
    });

    // Double-click navigates to the note.
    cy.on('dblclick', 'node', (evt) => {
      window.location.href = '/notes/' + evt.target.id();
    });
  }

  function centerRenderedPos() {
    const bb = container.getBoundingClientRect();
    return { x: bb.width / 2, y: bb.height / 2 };
  }
  function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)); }
  function zoomBy(factor) {
    if (!cy) return;
    cy.zoom({
      level: clamp(cy.zoom() * factor, cy.minZoom(), cy.maxZoom()),
      renderedPosition: centerRenderedPos()
    });
  }
  function fit() { if (cy) cy.fit(null, 40); }
  function relayout() {
    if (!cy) return;
    cy.layout(layoutConfig(true)).run();
  }

  // Toolbar actions.
  const toolbar = document.querySelector('.graph-toolbar');
  if (toolbar) {
    toolbar.addEventListener('click', (e) => {
      const btn = e.target.closest('button');
      if (!btn) return;
      switch (btn.dataset.act) {
        case 'zoom-in':  zoomBy(1.25); break;
        case 'zoom-out': zoomBy(1 / 1.25); break;
        case 'fit':      fit(); break;
        case 'relayout': relayout(); break;
      }
    });
  }

  // Project filter — reload graph via fetch and update URL (no full page reload).
  if (selectEl) {
    selectEl.addEventListener('change', async () => {
      const p = selectEl.value;
      const url = p ? `?project=${encodeURIComponent(p)}` : window.location.pathname;
      history.pushState({}, '', p ? url : window.location.pathname);
      await loadGraph(p);
    });
  }

  // Browser back/forward.
  window.addEventListener('popstate', () => { loadGraph(currentProject()); });

  // Keyboard shortcuts (ignore when typing in inputs).
  window.addEventListener('keydown', (e) => {
    if (e.target.matches('input, textarea, select, [contenteditable="true"]')) return;
    switch (e.key) {
      case '+':
      case '=': zoomBy(1.25); break;
      case '-': zoomBy(1 / 1.25); break;
      case '0':
      case 'f':
      case 'F': fit(); break;
      case 'l':
      case 'L': relayout(); break;
    }
  });

  // Refit on resize (debounced).
  let rt;
  window.addEventListener('resize', () => {
    clearTimeout(rt);
    rt = setTimeout(() => { if (cy) { cy.resize(); fit(); } }, 150);
  });

  // Kick off.
  loadGraph(currentProject()).catch((err) => {
    console.error('graph load failed', err);
    container.innerHTML =
      '<p style="padding:1rem;color:#E06A6A">Errore caricamento grafo: ' +
      (err && err.message ? err.message : 'unknown') + '</p>';
  });
})();

import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

// The words plancia-diagrams shows, in the language of the app (IMP-169):
// the view's, the editor's, and those of the templates a new diagram
// starts from.

const SHAPES = ['box', 'pill', 'decision', 'data', 'doc', 'db', 'circle', 'note'] as const
const CLASSES = ['user', 'data', 'ext', 'planned', 'off'] as const
const STYLES = ['solid', 'dashed', 'dotted'] as const
const STATUSES = ['done', 'active', 'critical', 'milestone'] as const
export const DIAGRAM_KINDS = [
  'flow',
  'tree',
  'mindmap',
  'fishbone',
  'venn',
  'matrix',
  'gantt',
] as const

/** The ready templates of plancia-diagrams, in its order. */
export const READY_IDS = [
  'process',
  'architecture',
  'org',
  'brainstorm',
  'sixm',
  'three-sets',
  'eisenhower',
  'impact-effort',
  'swot',
  'project',
] as const

export function useDiagramLabels() {
  const { t } = useI18n()
  const view = computed(() => ({
    problems: t('diagram.problems'),
    line: t('diagram.line'),
    pending: t('diagram.pending'),
    loading: t('diagram.loading'),
    failed: t('diagram.failed'),
    links: t('diagram.links'),
    zoomIn: t('diagram.zoom_in'),
    zoomOut: t('diagram.zoom_out'),
    fit: t('diagram.fit'),
    open: t('diagram.open'),
  }))
  const editor = computed(() => ({
    ...view.value,
    drawing: t('diagram.drawing'),
    yaml: t('diagram.yaml'),
    undo: t('diagram.undo'),
    redo: t('diagram.redo'),
    addNode: t('diagram.add_node'),
    addGroup: t('diagram.add_group'),
    remove: t('diagram.remove'),
    relayout: t('diagram.relayout'),
    newNode: t('diagram.new_node'),
    newGroup: t('diagram.new_group'),
    newBranch: t('diagram.new_branch'),
    node: t('diagram.node'),
    edge: t('diagram.edge'),
    group: t('diagram.group'),
    label: t('diagram.label'),
    shape: t('diagram.shape'),
    class: t('diagram.class'),
    link: t('diagram.link'),
    linkHint: t('diagram.link_hint'),
    style: t('diagram.style'),
    none: t('diagram.none'),
    connectHint: t('diagram.connect_hint'),
    addChild: t('diagram.add_child'),
    addSibling: t('diagram.add_sibling'),
    indent: t('diagram.indent'),
    outdent: t('diagram.outdent'),
    moveUp: t('diagram.move_up'),
    moveDown: t('diagram.move_down'),
    outlineHint: t('diagram.outline_hint'),
    broken: t('diagram.broken'),
    noProblems: t('diagram.no_problems'),
    sets: t('diagram.sets'),
    areas: t('diagram.areas'),
    addSet: t('diagram.add_set'),
    addArea: t('diagram.add_area'),
    newSet: t('diagram.new_set'),
    size: t('diagram.size'),
    sizeHint: t('diagram.size_hint'),
    axisX: t('diagram.axis_x'),
    axisY: t('diagram.axis_y'),
    axisName: t('diagram.axis_name'),
    low: t('diagram.low'),
    high: t('diagram.high'),
    quadrants: t('diagram.quadrants'),
    quadrantNames: [1, 2, 3, 4].map((i) => t(`diagram.quadrant_${i}`)) as [
      string,
      string,
      string,
      string,
    ],
    items: t('diagram.items'),
    addItem: t('diagram.add_item'),
    newItem: t('diagram.new_item'),
    dragHint: t('diagram.drag_hint'),
    rows: t('diagram.rows'),
    columns: t('diagram.columns'),
    addRow: t('diagram.add_row'),
    addColumn: t('diagram.add_column'),
    newRow: t('diagram.new_row'),
    newColumn: t('diagram.new_column'),
    removeRow: t('diagram.remove_row'),
    removeColumn: t('diagram.remove_column'),
    sections: t('diagram.sections'),
    addSection: t('diagram.add_section'),
    newSection: t('diagram.new_section'),
    addTask: t('diagram.add_task'),
    newTask: t('diagram.new_task'),
    task: t('diagram.task'),
    starts: t('diagram.starts'),
    onDate: t('diagram.on_date'),
    afterTask: t('diagram.after_task'),
    lasts: t('diagram.lasts'),
    untilDate: t('diagram.until_date'),
    days: t('diagram.days'),
    status: t('diagram.status'),
    statuses: Object.fromEntries(STATUSES.map((s) => [s, t(`diagram.statuses.${s}`)])) as Record<
      (typeof STATUSES)[number],
      string
    >,
    import: t('diagram.import'),
    importHint: t('diagram.import_hint'),
    importApply: t('diagram.import_apply'),
    importCancel: t('diagram.import_cancel'),
    importWarnings: t('diagram.import_warnings'),
    shapes: Object.fromEntries(SHAPES.map((s) => [s, t(`diagram.shapes.${s}`)])) as Record<
      (typeof SHAPES)[number],
      string
    >,
    classes: Object.fromEntries(CLASSES.map((c) => [c, t(`diagram.classes.${c}`)])) as Record<
      (typeof CLASSES)[number],
      string
    >,
    styles: Object.fromEntries(STYLES.map((s) => [s, t(`diagram.styles.${s}`)])) as Record<
      (typeof STYLES)[number],
      string
    >,
  }))
  const template = computed(() => ({
    start: t('diagram.template.start'),
    step: t('diagram.template.step'),
    end: t('diagram.template.end'),
    root: t('diagram.template.root'),
    branch: t('diagram.template.branch'),
    leaf: t('diagram.template.leaf'),
    effect: t('diagram.template.effect'),
    categories: [1, 2, 3, 4, 5, 6].map((i) => t(`diagram.template.category_${i}`)) as [
      string,
      string,
      string,
      string,
      string,
      string,
    ],
    cause: t('diagram.template.cause'),
    sets: [t('diagram.template.set_a'), t('diagram.template.set_b')] as [string, string],
    both: t('diagram.template.both'),
    x: t('diagram.template.x'),
    y: t('diagram.template.y'),
    low: t('diagram.template.low'),
    high: t('diagram.template.high'),
    quadrants: [1, 2, 3, 4].map((i) => t(`diagram.template.quadrant_${i}`)) as [
      string,
      string,
      string,
      string,
    ],
    task: t('diagram.template.task'),
  }))
  // The ready templates' words (readyTemplates), and their names.
  const READY = [
    'start',
    'collect',
    'complete',
    'yes',
    'no',
    'process',
    'ask',
    'end',
    'user',
    'webUi',
    'api',
    'database',
    'service',
    'server',
    'board',
    'admin',
    'accounts',
    'people',
    'production',
    'teamA',
    'teamB',
    'sales',
    'idea',
    'goals',
    'resources',
    'risks',
    'nextSteps',
    'setA',
    'setB',
    'setC',
    'all',
    'effort',
    'impact',
    'internal',
    'external',
    'helpful',
    'harmful',
    'analysis',
    'requirements',
    'development',
    'build',
    'test',
    'launch',
    'golive',
  ] as const
  const ready = computed(() => ({
    ...(Object.fromEntries(READY.map((k) => [k, t(`diagram.ready.${k}`)])) as Record<
      (typeof READY)[number],
      string
    >),
    effortQuadrants: [1, 2, 3, 4].map((i) => t(`diagram.ready.effort_${i}`)) as [
      string,
      string,
      string,
      string,
    ],
    swot: [1, 2, 3, 4].map((i) => t(`diagram.ready.swot_${i}`)) as [string, string, string, string],
  }))
  const readyNames = computed(() =>
    Object.fromEntries(READY_IDS.map((id) => [id, t(`diagram.ready_names.${id}`)])),
  )
  const kinds = computed(
    () =>
      Object.fromEntries(DIAGRAM_KINDS.map((k) => [k, t(`diagram.kinds.${k}`)])) as Record<
        (typeof DIAGRAM_KINDS)[number],
        string
      >,
  )
  return { view, editor, template, kinds, ready, readyNames }
}

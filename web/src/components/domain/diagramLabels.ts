import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

// The words plancia-diagrams shows, in the language of the app (IMP-169):
// the view's, the editor's, and those of the templates a new diagram
// starts from.

const SHAPES = ['box', 'pill', 'decision', 'data', 'doc', 'db', 'circle', 'note'] as const
const CLASSES = ['user', 'data', 'ext', 'planned', 'off'] as const
const STYLES = ['solid', 'dashed', 'dotted'] as const
export const DIAGRAM_KINDS = [
  'flow',
  'tree',
  'mindmap',
  'fishbone',
  'venn',
  'matrix',
  'gantt',
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
    yamlOnly: t('diagram.yaml_only'),
    noProblems: t('diagram.no_problems'),
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
  const kinds = computed(
    () =>
      Object.fromEntries(DIAGRAM_KINDS.map((k) => [k, t(`diagram.kinds.${k}`)])) as Record<
        (typeof DIAGRAM_KINDS)[number],
        string
      >,
  )
  return { view, editor, template, kinds }
}

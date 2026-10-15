<script setup lang="ts">
/**
 * The cut view of a note (IMP-170): its sections and paragraphs as
 * note.sections cuts them for an archive of vectors, each with a bar on its
 * left — blue a section, cyan a paragraph — and its id and size above it;
 * its images small beside the text a model reads, those without a
 * description in red. Compared with a collection, a bar says what the
 * archive holds: green the same text, orange another (to cut again), grey
 * nothing. The server renders each block; its HTML is sanitized here, as
 * the preview's is.
 *
 * The marks the pipelines put on the note (IMP-188) sit on the blocks they
 * name: a bar on the right in the mark's colour and its label, its text on
 * hover; each set can be hidden, and one made on an older text says so.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CutBlock, CutView } from '@/api/preview'
import type { Mark, MarkColor, MarkSet } from '@/api/marks'
import { sanitizePreviewHtml } from './sanitizePreview'
import { usePreviewLinks } from './usePreviewLinks'

// notePath: the note cut, which relative links start from.
const props = defineProps<{ view: CutView; compact?: boolean; marks?: MarkSet[]; notePath?: string }>()
const { t } = useI18n()

interface Group {
  block: CutBlock
  paras: CutBlock[]
}

// The sections, each with its paragraphs, in the note's order.
const groups = computed<Group[]>(() => {
  const out: Group[] = []
  const byId = new Map<string, Group>()
  for (const b of props.view.blocks) {
    if (b.level === 1) {
      const g = { block: b, paras: [] }
      out.push(g)
      byId.set(b.id, g)
    } else byId.get(b.parent ?? '')?.paras.push(b)
  }
  return out
})

const compared = computed(() => !!props.view.states)
// Nothing as stored though the archive holds the note: the rules are
// likely not the pipeline's.
const noneSame = computed(() => compared.value && !props.view.states?.same && props.view.stored > 0)

function bar(b: CutBlock): string {
  if (compared.value)
    return b.state === 'same' ? 'border-success' : b.state === 'changed' ? 'border-warning' : 'border-text-muted'
  return b.level === 1 ? 'border-accent' : 'border-info'
}

function meta(b: CutBlock): string[] {
  const out = [b.id.split('.').slice(1).join('.') || b.id, t('cut.chars', { n: b.chars }, b.chars)]
  if (b.part) out.push(t('cut.part', { n: b.part }))
  if (b.state) out.push(t(`cut.state.${b.state}`))
  return out
}

// The marks of the shown sets, by the block they name.
const hidden = ref<Set<string>>(new Set())
function toggleSet(name: string) {
  const next = new Set(hidden.value)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  hidden.value = next
}
interface Placed extends Mark {
  set: string
}
const placed = computed(() => {
  const byBlock = new Map<string, Placed[]>()
  const lost = new Map<string, number>()
  // The paragraphs of each section, in order, across its parts.
  const paras = new Map<string, CutBlock[]>()
  for (const b of props.view.blocks) {
    if (b.level !== 2) continue
    const key = b.section ?? ''
    paras.set(key, [...(paras.get(key) ?? []), b])
  }
  for (const s of props.marks ?? []) {
    if (hidden.value.has(s.set)) continue
    for (const m of s.marks) {
      let targets: CutBlock[] = []
      if (m.base) targets = props.view.blocks.filter((b) => b.id === m.base)
      else if (m.block) {
        const p = paras.get(m.section ?? '')?.[m.block - 1]
        targets = p ? [p] : []
      } else targets = props.view.blocks.filter((b) => b.level === 1 && (b.section ?? '') === (m.section ?? ''))
      if (!targets.length) lost.set(s.set, (lost.get(s.set) ?? 0) + 1)
      for (const b of targets) byBlock.set(b.id, [...(byBlock.get(b.id) ?? []), { ...m, set: s.set }])
    }
  }
  return { byBlock, lost }
})
const marksOf = (b: CutBlock) => placed.value.byBlock.get(b.id) ?? []

// The theme's colours of a mark: its bar and its label.
const MARK_BAR: Record<MarkColor, string> = {
  blue: 'border-r-accent',
  green: 'border-r-success',
  orange: 'border-r-warning',
  red: 'border-r-danger',
  cyan: 'border-r-info',
  grey: 'border-r-text-muted',
}
const MARK_CHIP: Record<MarkColor, string> = {
  blue: 'bg-accent/15 text-accent',
  green: 'bg-success/15 text-success',
  orange: 'bg-warning/15 text-warning',
  red: 'bg-danger/15 text-danger',
  cyan: 'bg-info/15 text-info',
  grey: 'bg-text-muted/15 text-text-muted',
}
function markBar(b: CutBlock): string {
  const m = marksOf(b)[0]
  return m ? `border-r-4 pr-2 ${MARK_BAR[m.color] ?? ''}` : ''
}

const html = (s: string) => sanitizePreviewHtml(s)
const proseClass = computed(
  () =>
    `gosidian-preview ${props.compact ? 'prose prose-sm' : 'prose'} max-w-none prose-pre:bg-bg-elevated prose-pre:border prose-pre:border-border prose-code:before:hidden prose-code:after:hidden`,
)

// A mark's set and text, which its chip shows on hover and says when focused.
const markTitle = (m: Placed) => [m.set, m.text].filter(Boolean).join(': ')

// The links open as in the preview.
const root = ref<HTMLElement | null>(null)
const { onClick } = usePreviewLinks(root, () => props.notePath)
</script>

<template>
  <div ref="root" class="gosidian-cut space-y-3" data-cut @click="onClick">
    <div class="space-y-1 text-xs text-text-muted" data-cut-summary>
      <p>
        {{ t('cut.sections', { n: view.sections }, view.sections) }} ·
        {{ t('cut.paragraphs', { n: view.paragraphs }, view.paragraphs) }} ·
        {{ t('cut.images', { ...view.media }) }}
      </p>
      <p v-if="compared" data-cut-states>
        {{
          t('cut.states', {
            same: view.states?.same ?? 0,
            changed: view.states?.changed ?? 0,
            missing: view.states?.missing ?? 0,
            orphans: view.orphans,
          })
        }}
      </p>
      <p class="flex flex-wrap gap-3">
        <template v-if="compared">
          <span class="border-l-4 border-success pl-1">{{ t('cut.state.same') }}</span>
          <span class="border-l-4 border-warning pl-1">{{ t('cut.state.changed') }}</span>
          <span class="border-l-4 border-text-muted pl-1">{{ t('cut.state.missing') }}</span>
        </template>
        <template v-else>
          <span class="border-l-4 border-accent pl-1">{{ t('cut.section') }}</span>
          <span class="border-l-4 border-info pl-1">{{ t('cut.paragraph') }}</span>
        </template>
        <span class="gosidian-cut-missing">{{ t('cut.missing_image') }}</span>
      </p>
      <p v-if="view.error" class="text-danger" role="alert" data-cut-error>{{ t('cut.error', { error: view.error }) }}</p>
      <p v-else-if="noneSame" class="text-warning" data-cut-hint>{{ t('cut.settings_hint') }}</p>
      <div v-if="marks?.length" class="flex flex-wrap items-center gap-3" data-cut-marks>
        <span>{{ t('marks.title') }}</span>
        <label v-for="s in marks" :key="s.set" class="inline-flex items-center gap-1" :data-mark-set="s.set">
          <input type="checkbox" :checked="!hidden.has(s.set)" @change="toggleSet(s.set)" />
          <span class="font-mono">{{ s.set }}</span>
          <span>({{ s.marks.length }})</span>
          <span v-if="s.stale" class="text-warning" data-mark-stale>{{ t('marks.stale') }}</span>
          <span v-if="placed.lost.get(s.set)" class="text-danger" data-mark-lost>
            {{ t('marks.lost', { n: placed.lost.get(s.set) }, placed.lost.get(s.set) ?? 0) }}
          </span>
        </label>
      </div>
    </div>

    <section
      v-for="g in groups"
      :key="g.block.id"
      class="border-l-4 pl-3"
      :class="[bar(g.block), markBar(g.block)]"
      :data-cut-state="g.block.state"
      data-cut-section
    >
      <p class="flex flex-wrap gap-2 font-mono text-[11px] text-text-muted">
        <span v-for="(m, i) in meta(g.block)" :key="i">{{ m }}</span>
        <span v-if="g.block.over" class="text-danger">{{ t('cut.over', { n: view.max_chars }) }}</span>
        <span
          v-for="(m, i) in marksOf(g.block)"
          :key="`m${i}`"
          class="rounded px-1 font-sans"
          :class="MARK_CHIP[m.color]"
          :title="markTitle(m)"
          :aria-label="`${m.label || m.set} — ${markTitle(m)}`"
          tabindex="0"
          data-mark
        >{{ m.label || m.set }}</span>
      </p>
      <div v-if="g.block.head" :class="proseClass" v-html="html(g.block.head)" />
      <div
        v-for="p in g.paras"
        :key="p.id"
        class="my-1 border-l-2 pl-2"
        :class="[bar(p), markBar(p)]"
        :data-cut-state="p.state"
        data-cut-paragraph
      >
        <p class="flex flex-wrap gap-2 font-mono text-[10px] text-text-muted">
          <span v-for="(m, i) in meta(p)" :key="i">{{ m }}</span>
          <span v-if="p.over" class="text-danger">{{ t('cut.over', { n: view.max_chars }) }}</span>
          <span
            v-for="(m, i) in marksOf(p)"
            :key="`m${i}`"
            class="rounded px-1 font-sans"
            :class="MARK_CHIP[m.color]"
            :title="markTitle(m)"
            :aria-label="`${m.label || m.set} — ${markTitle(m)}`"
            tabindex="0"
            data-mark
          >{{ m.label || m.set }}</span>
        </p>
        <div :class="proseClass" v-html="html(p.html)" />
      </div>
    </section>
  </div>
</template>

<style scoped>
/* The sanitizer's second fence, as in MarkdownPreview (BUG-117): what a
   note's HTML positions stays inside the block it is in. */
.gosidian-cut :deep(.gosidian-preview) {
  contain: layout;
}
.gosidian-cut :deep(.gosidian-cut-thumb) {
  display: inline-block;
  max-height: 3rem;
  max-width: 8rem;
  margin: 0 0.35rem 0 0;
  vertical-align: middle;
  border: 1px solid rgb(var(--color-border));
  border-radius: 0.25rem;
}
.gosidian-cut :deep(.gosidian-cut-missing) {
  color: rgb(var(--color-danger));
  font-weight: 600;
}
</style>

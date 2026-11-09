<script setup lang="ts">
/**
 * FieldValue — a field's value as text, with its [[wikilinks]] as links (a
 * relation, say), the way the server's HTML shows them. A resolved link
 * opens its note as a window; an unresolved one stays plain. Given its
 * column, a select's values are named in the reader's language and a date
 * is written as they read it, the code or the ISO form in the tooltip
 * (IMP-220, F-23).
 */
import { computed, inject } from 'vue'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey, noteHref, base } from '@/composables/planciaKey'
import type { ViewColumn, ViewLink } from '@/api/preview'
import { formatFieldDate } from '@/api/format'
import { useFieldNames } from './fieldNames'

const props = defineProps<{ value?: string | string[]; links?: ViewLink[]; column?: ViewColumn }>()
const { valueName } = useFieldNames()

// The plancia store is reached only on a click, when no window opener is
// provided: a value shown outside a plancia needs neither.
const openWindow = inject<((spec: OpenSpec) => string) | null>('openWindow', null)

// A text part with a code carries it to the tooltip; a date's code is its
// ISO form.
type Part = { text: string; code?: string; date?: boolean } | { link: ViewLink }

/** A whole value without links, as its column names it. */
function named(v: string): Part {
  if (props.column?.type === 'date') {
    const text = formatFieldDate(v)
    return text === v ? { text } : { text, code: v, date: true }
  }
  const text = props.column ? valueName(props.column, v) : v
  return text === v ? { text } : { text, code: v }
}

const parts = computed<Part[]>(() => {
  const values = Array.isArray(props.value) ? props.value : props.value ? [props.value] : []
  const out: Part[] = []
  let next = 0
  values.forEach((v, i) => {
    if (i > 0) out.push({ text: ', ' })
    if (!v.includes('[[')) {
      out.push(named(v))
      return
    }
    let last = 0
    for (const m of v.matchAll(/\[\[([^\]]+)\]\]/g)) {
      if (m.index > last) out.push({ text: v.slice(last, m.index) })
      const inner = (m[1] ?? '').replace(/\\\|/g, '|')
      out.push({ link: props.links?.[next] ?? { text: inner.split('|').pop()?.trim() ?? inner } })
      next++
      last = m.index + m[0].length
    }
    if (last < v.length) out.push({ text: v.slice(last) })
  })
  return out
})

function open(e: MouseEvent, path: string) {
  if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
  e.preventDefault()
  const spec: OpenSpec = {
    type: 'note',
    key: planciaKey('note', path),
    title: base(path),
    props: { path },
  }
  if (openWindow) openWindow(spec)
  else useWindowsStore().open(spec)
}
</script>

<template>
  <template v-for="(p, i) in parts" :key="i">
    <a
      v-if="'link' in p && p.link.path"
      class="wikilink"
      :href="noteHref(p.link.path)"
      :data-preview-path="p.link.path"
      @click="open($event, p.link.path)"
      >{{ p.link.text }}</a
    ><span v-else-if="'link' in p" class="wikilink unresolved">{{ p.link.text }}</span
    ><time v-else-if="p.date" :datetime="p.code" :title="p.code">{{ p.text }}</time
    ><span v-else-if="p.code" :title="p.code">{{ p.text }}</span
    ><template v-else>{{ p.text }}</template>
  </template>
</template>

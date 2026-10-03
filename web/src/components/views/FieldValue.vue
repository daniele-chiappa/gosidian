<script setup lang="ts">
/**
 * FieldValue — a field's value as text, with its [[wikilinks]] as links (a
 * relation, say), the way the server's HTML shows them. A resolved link
 * opens its note as a window; an unresolved one stays plain.
 */
import { computed, inject } from 'vue'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import type { ViewLink } from '@/api/preview'

const props = defineProps<{ value?: string | string[]; links?: ViewLink[] }>()

// The plancia store is reached only on a click, when no window opener is
// provided: a value shown outside a plancia needs neither.
const openWindow = inject<((spec: OpenSpec) => string) | null>('openWindow', null)

type Part = { text: string } | { link: ViewLink }

const parts = computed<Part[]>(() => {
  const values = Array.isArray(props.value) ? props.value : props.value ? [props.value] : []
  const out: Part[] = []
  let next = 0
  values.forEach((v, i) => {
    if (i > 0) out.push({ text: ', ' })
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

function noteHref(path: string): string {
  return '/notes/' + path.split('/').map(encodeURIComponent).join('/')
}

function open(e: MouseEvent, path: string) {
  if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
  e.preventDefault()
  const spec: OpenSpec = {
    type: 'note',
    key: planciaKey('note', path),
    title: (path.split('/').pop() ?? path).replace(/\.md$/, ''),
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
    ><template v-else>{{ p.text }}</template>
  </template>
</template>

<script setup lang="ts">
/**
 * TagField — a note's frontmatter tags as chips (IMP-223, F-13): each with
 * a button that takes it away, and a field that adds them, with the
 * project's tags as suggestions, the most used first, and its declared
 * vocabulary after (a combobox, as the palette's). Enter, a comma or a
 * space adds what is typed; a pasted list is split. The tags were written
 * in the YAML only, where a comma out of place saved a wrong tag unseen.
 *
 * Every change is saved at once, one after the other, against the tags
 * the note had (expect): a change made meanwhile is not overwritten, and
 * the field shows the tags of now with the reason.
 */
import { computed, nextTick, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { X } from 'lucide-vue-next'
import { listTags, type TagCount } from '@/api/tags'
import { saveField } from './fieldSave'

const props = withDefaults(
  defineProps<{
    path: string
    tags: string[]
    writable: boolean
    /** The project's declared vocabulary; its `ns:*` entries are no tags. */
    vocabulary?: string[]
  }>(),
  { vocabulary: () => [] },
)
const emit = defineEmits<{ saved: [string[]] }>()
const { t } = useI18n()

const COLUMN = { name: 'tags' }
const MAX_SUGGESTIONS = 8

const shown = ref<string[]>([...props.tags])
// What the note has, as far as the last answer says: the next save expects it.
let confirmed = [...props.tags]
let pending = 0
watch(
  () => props.tags,
  (tags) => {
    if (pending) return
    shown.value = [...tags]
    confirmed = [...tags]
  },
)

const status = ref('')
const message = ref('')
let queue: Promise<void> = Promise.resolve()

function change(next: string[], said: string) {
  shown.value = next
  message.value = ''
  pending++
  queue = queue.then(async () => {
    const seen = confirmed
    const res = await saveField(
      props.path,
      COLUMN,
      seen,
      next.length ? { set: next } : { unset: true },
      t,
    )
    pending--
    if (res.ok) {
      confirmed = next
      status.value = said
      emit('saved', next)
      return
    }
    if (res.conflict)
      confirmed = Array.isArray(res.current) ? res.current : res.current ? [res.current] : []
    shown.value = [...confirmed]
    message.value = res.message
  })
}

const has = (tag: string) => shown.value.some((x) => x.toLowerCase() === tag.toLowerCase())

/** The tags in what was typed or pasted: split at commas and spaces, without #. */
function parse(text: string): string[] {
  const out: string[] = []
  for (const part of text.split(/[\s,]+/)) {
    const tag = part.replace(/^#+/, '')
    if (tag && !has(tag) && !out.some((x) => x.toLowerCase() === tag.toLowerCase())) out.push(tag)
  }
  return out
}

function add(tags: string[]) {
  query.value = ''
  closeList()
  if (!tags.length) return
  change([...shown.value, ...tags], t('tags_field.added', { tag: tags.join(', ') }, tags.length))
}

const inputEl = ref<HTMLInputElement | null>(null)
async function remove(tag: string) {
  change(
    shown.value.filter((x) => x !== tag),
    t('tags_field.removed', { tag }),
  )
  await nextTick()
  inputEl.value?.focus()
}

// The suggestions: the project's tags, loaded once at the first focus.
const project = computed(() => props.path.split('/')[0] ?? '')
const used = ref<TagCount[] | null>(null)
async function loadTags() {
  if (used.value) return
  used.value = []
  try {
    used.value = (await listTags(project.value)).slice().sort((a, b) => b.count - a.count)
  } catch {
    /* no suggestions: a tag can still be typed */
  }
}

const query = ref('')
const open = ref(false)
const active = ref(-1)
const suggestions = computed(() => {
  const q = query.value.trim().replace(/^#+/, '').toLowerCase()
  if (!open.value || !q) return []
  const known = [
    ...(used.value ?? []).map((x) => x.tag),
    ...props.vocabulary.filter((v) => !v.endsWith(':*')),
  ]
  const out: string[] = []
  for (const tag of known) {
    if (tag.toLowerCase().includes(q) && !has(tag) && !out.includes(tag)) out.push(tag)
    if (out.length === MAX_SUGGESTIONS) break
  }
  return out
})
watch(query, () => {
  open.value = true
  active.value = -1
})
function closeList() {
  open.value = false
  active.value = -1
}

const baseId = `tf-${useId()}`
const listId = `${baseId}-list`
const optionId = (i: number) => `${listId}-${i}`

// The list and the suggestion the arrows are on in sight: the field can sit
// low in a window that scrolls.
const listEl = ref<HTMLElement | null>(null)
const inSight = (el: Element | null | undefined) => el?.scrollIntoView?.({ block: 'nearest' })
watch(
  () => suggestions.value.length > 0,
  async (shownNow) => {
    if (!shownNow) return
    await nextTick()
    inSight(listEl.value)
  },
)
watch(active, async (i) => {
  if (i < 0) return
  await nextTick()
  inSight(document.getElementById(optionId(i)))
})

function onKeydown(e: KeyboardEvent) {
  const list = suggestions.value
  switch (e.key) {
    case 'ArrowDown':
    case 'ArrowUp':
      if (!list.length) return
      e.preventDefault()
      open.value = true
      active.value =
        e.key === 'ArrowDown'
          ? (active.value + 1) % list.length
          : (active.value - 1 + list.length) % list.length
      return
    case 'Enter':
      e.preventDefault()
      add(active.value >= 0 && list[active.value] ? [list[active.value]!] : parse(query.value))
      return
    case ',':
    case ' ':
      e.preventDefault()
      add(parse(query.value))
      return
    case 'Escape':
      if (open.value && list.length) {
        e.preventDefault()
        e.stopPropagation()
        closeList()
      }
      return
    case 'Tab':
      closeList()
  }
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-1" data-tag-field>
    <ul
      v-if="shown.length"
      class="m-0 flex list-none flex-wrap gap-1 p-0"
      :aria-label="t('tags_field.label')"
    >
      <li
        v-for="tag in shown"
        :key="tag"
        class="inline-flex items-center gap-0.5 rounded bg-surface-hover px-1.5 text-xs leading-5"
        :data-tag="tag"
      >
        <span>{{ tag }}</span>
        <button
          v-if="writable"
          type="button"
          class="inline-flex h-4 w-4 items-center justify-center rounded text-text-muted hover:text-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
          :aria-label="t('tags_field.remove', { tag })"
          :title="t('tags_field.remove', { tag })"
          @click="remove(tag)"
        >
          <X class="h-3 w-3" aria-hidden="true" />
        </button>
      </li>
    </ul>
    <div v-if="writable" class="relative">
      <input
        ref="inputEl"
        v-model="query"
        type="text"
        autocomplete="off"
        role="combobox"
        aria-autocomplete="list"
        :aria-label="t('tags_field.add')"
        :aria-expanded="suggestions.length > 0"
        :aria-controls="listId"
        :aria-activedescendant="active >= 0 && suggestions.length ? optionId(active) : undefined"
        :placeholder="t('tags_field.add')"
        class="h-control-sm w-36 rounded border border-border-input bg-bg-elevated px-1.5 text-xs"
        @focus="loadTags"
        @blur="closeList"
        @keydown="onKeydown"
      />
      <ul
        v-if="suggestions.length"
        :id="listId"
        ref="listEl"
        role="listbox"
        :aria-label="t('tags_field.suggestions')"
        class="absolute left-0 top-full z-20 mt-1 max-h-60 min-w-full overflow-auto rounded border border-border bg-bg-elevated py-1 text-xs shadow"
      >
        <li
          v-for="(tag, i) in suggestions"
          :id="optionId(i)"
          :key="tag"
          role="option"
          :aria-selected="i === active"
          :class="[
            'cursor-pointer whitespace-nowrap px-2 py-1',
            i === active ? 'bg-surface-hover' : 'hover:bg-surface-hover',
          ]"
          @mousedown.prevent="add([tag])"
        >
          {{ tag }}
        </li>
      </ul>
    </div>
    <p v-if="message" class="m-0 w-full text-xs text-danger" role="alert" data-tag-message>
      {{ message }}
    </p>
    <p class="sr-only" role="status" aria-live="polite">{{ status }}</p>
  </div>
</template>

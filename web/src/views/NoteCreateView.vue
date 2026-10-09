<script setup lang="ts">
/**
 * NoteCreateView — the "new note" creation form, opened as a plancia window
 * from the `+` on a tree folder row (the target folder is passed as `path`).
 * Unified for both note kinds via a type toggle:
 *   - Markdown: a normal .md note (frontmatter title), opened in edit mode.
 *   - Image:    a media note (ADR-013) — upload an image, then create a .md
 *               with `type: image` + `media:` + the caption as body.
 *
 * Restores manual note creation, which the v2.0 HTMX→Vue migration never
 * ported (IMP-058); the backend (POST /notes, POST /attach) was already there.
 */
import { ref, computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { createNote } from '@/api/notes'
import { attachFile } from '@/api/attach'
import { useTreeStore } from '@/stores/tree'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'
import { noteFrontmatter } from './noteFrontmatter'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

// name: the note's name to start from (a missing note's "create it here").
const props = defineProps<{ path?: string; name?: string }>()
const emit = defineEmits<{ close: [] }>()

const { t } = useI18n()
const treeStore = useTreeStore()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

type Kind = 'markdown' | 'image'
const kind = ref<Kind>('markdown')

// Target folder (no trailing slash). Empty means the vault root.
const folder = computed(() => (props.path ?? '').replace(/\/+$/, ''))
const project = computed(() => folder.value.split('/')[0] || '')

const name = ref(props.name ?? '')
const title = ref('')
const caption = ref('')
const file = ref<File | null>(null)
const creating = ref(false)
const error = ref<string | null>(null)

const slug = computed(() =>
  name.value
    .trim()
    .replace(/\.md$/i, '')
    .replace(/^\/+|\/+$/g, ''),
)
const notePath = computed(() => (folder.value ? `${folder.value}/${slug.value}.md` : `${slug.value}.md`))

function onFile(e: Event) {
  const input = e.target as HTMLInputElement
  file.value = input.files?.[0] ?? null
}

function frontmatter(extra: [string, string | string[]][] = []): string {
  return noteFrontmatter([['title', title.value.trim() || slug.value], ...extra])
}

async function submit() {
  if (!slug.value || creating.value) return
  if (kind.value === 'image' && !file.value) {
    error.value = t('note_create.err_choose_image')
    return
  }
  creating.value = true
  error.value = null
  try {
    let content: string
    let mode: 'edit' | undefined
    if (kind.value === 'image') {
      const res = await attachFile(file.value as File, project.value || undefined)
      content =
        frontmatter([
          ['type', 'image'],
          ['media', res.path],
          ['tags', [project.value, 'type:image']],
        ]) + `${caption.value.trim()}\n`
    } else {
      content = frontmatter()
      mode = 'edit'
    }
    const created = await createNote(notePath.value, content)
    treeStore.refresh()
    openWindow({
      type: 'note',
      key: planciaKey('note', created.path),
      title: created.title || created.path,
      props: { path: created.path, ...(mode ? { mode } : {}) },
    })
    emit('close')
  } catch (e) {
    error.value = errorText(e, t, t('note_create.err_failed'))
  } finally {
    creating.value = false
  }
}
</script>

<template>
  <div class="p-6 max-w-xl mx-auto">
    <h1 class="text-lg font-semibold mb-1">{{ t('note_create.title') }}</h1>
    <p class="text-sm text-text-muted mb-5">
      {{ t('note_create.location_prefix') }} <span class="font-mono">{{ folder || t('note_create.root') }}/</span>
    </p>

    <!-- Kind toggle -->
    <div class="inline-flex rounded border border-border overflow-hidden text-sm mb-4">
      <button
        type="button"
        class="px-3 py-1"
        :class="kind === 'markdown' ? 'bg-accent text-accent-fg' : 'hover:bg-surface-hover'"
        @click="kind = 'markdown'"
      >{{ t('note_create.kind_markdown') }}</button>
      <button
        type="button"
        class="px-3 py-1"
        :class="kind === 'image' ? 'bg-accent text-accent-fg' : 'hover:bg-surface-hover'"
        @click="kind = 'image'"
      >{{ t('note_create.kind_image') }}</button>
    </div>

    <form
      class="space-y-4"
      @submit.prevent="submit"
    >
      <label class="block">
        <span class="text-sm text-text-muted">{{ t('note_create.name_label') }}</span>
        <input
          v-model="name"
          type="text"
          :placeholder="t('note_create.name_placeholder')"
          class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
        >
        <span
          v-if="slug"
          class="mt-1 block text-xs text-text-muted font-mono"
        >{{ notePath }}</span>
      </label>

      <label class="block">
        <span class="text-sm text-text-muted">{{ t('note_create.title_label') }} <span class="opacity-60">{{ t('note_create.optional') }}</span></span>
        <input
          v-model="title"
          type="text"
          :placeholder="slug || t('note_create.title_placeholder')"
          class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
        >
      </label>

      <template v-if="kind === 'image'">
        <label class="block">
          <span class="text-sm text-text-muted">{{ t('note_create.image_label') }}</span>
          <input
            type="file"
            accept="image/*"
            class="mt-1 w-full text-sm file:mr-3 file:rounded file:border-0 file:bg-accent file:text-accent-fg file:px-3 file:py-1.5 file:cursor-pointer"
            @change="onFile"
          >
        </label>
        <label class="block">
          <span class="text-sm text-text-muted">{{ t('note_create.caption_label') }} <span class="opacity-60">{{ t('note_create.caption_hint') }}</span></span>
          <textarea
            v-model="caption"
            rows="3"
            :placeholder="t('note_create.caption_placeholder')"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
          />
        </label>
      </template>

      <ErrorMessage v-if="error" :text="error" class="text-sm" />

      <div class="flex gap-2 pt-1">
        <button
          type="submit"
          class="px-3 py-2 rounded bg-accent text-accent-fg hover:bg-accent-hover disabled:opacity-50"
          :disabled="!slug || creating"
        >{{ creating ? t('note_create.submitting') : t('note_create.submit') }}</button>
        <button
          type="button"
          class="px-3 py-2 rounded hover:bg-surface-hover"
          @click="emit('close')"
        >{{ t('note_create.cancel') }}</button>
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
/**
 * TablePreview — the read view of a CSV table note (ADR-016): the CSV payload
 * fetched from /vault-files/ and rendered as a paginated table, followed by
 * the note's caption (its markdown body, already rendered to HTML upstream)
 * and a small metadata line. A broken `media:` pointer degrades to a
 * placeholder rather than an opaque error, so the note still reads.
 *
 * Cells are rendered as plain text nodes (Vue interpolation, never v-html):
 * CSV is untrusted vault content but text-only rendering makes it inert.
 * Parsing happens client-side with a minimal RFC 4180 reader — no dependency —
 * with the same comma/semicolon/tab delimiter sniffing as the backend.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MediaRef } from '@/api/notes'
import MarkdownPreview from './MarkdownPreview.vue'

const { t } = useI18n()

const props = defineProps<{
  media: MediaRef
  /** Rendered HTML of the note body (the caption), or '' when empty. */
  captionHtml: string
  notePath: string
}>()

const PAGE_SIZE = 500

const loading = ref(false)
const loadError = ref(false)
const header = ref<string[]>([])
const rows = ref<string[][]>([])
const page = ref(0)

// Minimal RFC 4180 parser: quoted fields, escaped quotes ("" inside quotes),
// CR/LF and CRLF row endings. Ragged rows are kept as-is.
function parseCSV(text: string, delim: string): string[][] {
  const out: string[][] = []
  let row: string[] = []
  let field = ''
  let inQuotes = false
  let i = 0
  const pushField = () => {
    row.push(field)
    field = ''
  }
  const pushRow = () => {
    pushField()
    // Skip a trailing empty line ([""]) produced by a final newline.
    if (row.length > 1 || row[0] !== '') out.push(row)
    row = []
  }
  while (i < text.length) {
    const c = text[i]
    if (inQuotes) {
      if (c === '"') {
        if (text[i + 1] === '"') {
          field += '"'
          i += 2
          continue
        }
        inQuotes = false
        i++
        continue
      }
      field += c
      i++
      continue
    }
    if (c === '"' && field === '') {
      inQuotes = true
      i++
    } else if (c === delim) {
      pushField()
      i++
    } else if (c === '\n') {
      pushRow()
      i++
    } else if (c === '\r') {
      pushRow()
      if (text[i + 1] === '\n') i++
      i++
    } else {
      field += c
      i++
    }
  }
  if (field !== '' || row.length > 0) pushRow()
  return out
}

// Same delimiter sniffing as the backend's csvSummary: the winner among
// comma / semicolon / tab on the first line.
function sniffDelimiter(text: string): string {
  const firstLine = text.slice(0, text.indexOf('\n') === -1 ? undefined : text.indexOf('\n'))
  const count = (ch: string) => firstLine.split(ch).length - 1
  let delim = ','
  let best = count(',')
  if (count(';') > best) {
    best = count(';')
    delim = ';'
  }
  if (count('\t') > best) delim = '\t'
  return delim
}

async function load() {
  if (props.media.broken) return
  loading.value = true
  loadError.value = false
  try {
    const res = await fetch(props.media.url)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const text = await res.text()
    const parsed = parseCSV(text, sniffDelimiter(text))
    header.value = parsed[0] ?? []
    rows.value = parsed.slice(1)
    page.value = 0
  } catch {
    loadError.value = true
  } finally {
    loading.value = false
  }
}
watch(() => props.media.url, load, { immediate: true })

const totalPages = computed(() => Math.max(1, Math.ceil(rows.value.length / PAGE_SIZE)))
const pageRows = computed(() => rows.value.slice(page.value * PAGE_SIZE, (page.value + 1) * PAGE_SIZE))

const sizeLabel = computed(() => {
  const n = props.media.size
  if (!n) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
})

// Metadata line under the table: path · mime · size · row count.
const metaLabel = computed(() => {
  const parts = [props.media.path]
  if (props.media.mime) parts.push(props.media.mime)
  if (sizeLabel.value) parts.push(sizeLabel.value)
  if (!loading.value && !loadError.value) parts.push(`${rows.value.length} ${t('table.rows')}`)
  return parts.join(' · ')
})
</script>

<template>
  <div class="p-6 max-w-5xl mx-auto">
    <div
      v-if="media.broken"
      class="flex items-center justify-center rounded border border-dashed border-warning/50 bg-warning/5 px-4 py-10 text-sm text-warning"
    >
      {{ t('table.not_found') }}: <span class="ml-1 font-mono">{{ media.path }}</span>
    </div>
    <div
      v-else-if="loadError"
      class="flex items-center justify-center rounded border border-dashed border-danger/50 bg-danger/5 px-4 py-10 text-sm text-danger"
    >
      {{ t('table.load_error') }}: <span class="ml-1 font-mono">{{ media.path }}</span>
    </div>
    <template v-else>
      <p v-if="loading" class="text-text-muted text-sm">Loading…</p>
      <template v-else>
        <div class="overflow-x-auto rounded border border-border">
          <table class="w-full text-sm border-collapse">
            <thead>
              <tr class="bg-bg-elevated">
                <th
                  v-for="(col, ci) in header"
                  :key="ci"
                  class="text-left font-semibold px-3 py-2 border-b border-border whitespace-nowrap"
                >
                  {{ col }}
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(r, ri) in pageRows" :key="ri" class="odd:bg-bg even:bg-bg-elevated/40">
                <td
                  v-for="(cell, ci) in r"
                  :key="ci"
                  class="px-3 py-1.5 border-b border-border/50 align-top"
                >
                  {{ cell }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="mt-2 flex items-center gap-3 text-xs text-text-muted">
          <template v-if="totalPages > 1">
            <button
              class="px-2 py-1 rounded border border-border disabled:opacity-40"
              :disabled="page === 0"
              @click="page--"
            >
              {{ t('table.prev') }}
            </button>
            <span>{{ t('table.page_of', { page: page + 1, total: totalPages }) }}</span>
            <button
              class="px-2 py-1 rounded border border-border disabled:opacity-40"
              :disabled="page >= totalPages - 1"
              @click="page++"
            >
              {{ t('table.next') }}
            </button>
          </template>
          <a :href="media.url" :download="media.path.split('/').pop()" class="underline">
            {{ t('table.download') }}
          </a>
        </div>
      </template>
    </template>

    <p class="mt-2 text-xs text-text-muted font-mono">
      {{ metaLabel }}
    </p>

    <div
      v-if="captionHtml"
      class="mt-6"
    >
      <MarkdownPreview :html="captionHtml" :note-path="notePath" />
    </div>
  </div>
</template>

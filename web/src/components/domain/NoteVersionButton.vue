<script setup lang="ts">
/**
 * NoteVersionButton — which version of a note readers see, in the bar of the
 * note and of its design (IMP-228): the state at a glance and, in a panel,
 * the way to pin the note as it is, a tag or another commit, or to go back
 * to the latest. The note goes on changing as a draft either way (IMP-201).
 * A pin is a click away from going back, so it asks no confirmation.
 *
 * The panel reads the versions when it opens: a window that only shows the
 * note does not pay for them. Whatever is unsaved goes first: what is pinned
 * is what shows, and a pin writes the note, which would conflict with the
 * text in the editor. The panel also says what the note gives who calls over
 * MCP: that goes by the same rights, and needs no word in the bar.
 */
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onClickOutside } from '@vueuse/core'
import { Pin } from 'lucide-vue-next'
import {
  clearMCPVersion,
  clearValidVersion,
  getHistory,
  getVersions,
  setMCPVersion,
  setValidVersion,
  tagVersion,
  type HistoryEntry,
  type NoteTag,
  type ValidVersion,
  type Versions,
} from '@/api/history'
import { errorText } from '@/api/errors'
import { announce } from '@/composables/useAnnounce'
import DateTime from '@/components/primitives/DateTime.vue'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

const props = defineProps<{
  path: string
  valid?: ValidVersion | null
  /** There is something to save, and save saves it: false if it could not. */
  dirty?: boolean
  save?: () => Promise<boolean>
  /** A narrow bar: the name of the version alone. */
  compact?: boolean
}>()
const emit = defineEmits<{ changed: []; open: [tag: NoteTag]; history: [] }>()
const { t } = useI18n()

/** The commits offered: older ones are in the history's window. */
const COMMITS = 20

const open = ref(false)
const root = ref<HTMLElement | null>(null)
const button = ref<HTMLButtonElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const versions = ref<Versions | null>(null)
const commits = ref<HistoryEntry[] | null>(null)
const showCommits = ref(false)
// The commit chosen for the name below: none for the note as it is.
const commit = ref('')
const label = ref('')
const busy = ref(false)
const error = ref('')

const valid = computed(() => versions.value?.valid ?? props.valid ?? null)
const canAdmin = computed(() => !!versions.value?.can_admin)
const locked = computed(() => busy.value || !canAdmin.value)
const hasDraft = computed(() => !!props.valid && !props.valid.same && !props.valid.missing)
const stateLabel = computed(() => {
  if (!props.valid) return t('versions.latest')
  return props.compact ? props.valid.label : t('versions.pinned', { label: props.valid.label })
})
const stateTitle = computed(() =>
  props.valid
    ? [t('versions.pinned', { label: props.valid.label }), ...(hasDraft.value ? [t('versions.draft_newer')] : [])].join(' · ')
    : t('versions.title'),
)
// The latest, what readers see, a named version; and the one chosen, when
// the note no longer has it.
const FOLLOW_VALID = 'valid'
const mcpChoices = computed(() => {
  const items = versions.value?.items ?? []
  const mcp = versions.value?.mcp
  return [
    { value: '', label: t('versions.latest_choice') },
    { value: FOLLOW_VALID, label: t('versions.mcp_same') },
    ...items.map((v) => ({ value: v.tag, label: v.label })),
    ...(mcp && mcp.tag !== FOLLOW_VALID && !items.some((v) => v.tag === mcp.tag)
      ? [{ value: mcp.tag, label: mcp.label || mcp.tag }]
      : []),
  ]
})
const shortCommit = computed(() => commits.value?.find((c) => c.sha === commit.value)?.short_sha ?? '')
const pinLabel = computed(() => {
  if (shortCommit.value) return t('versions.pin_commit', { sha: shortCommit.value })
  return props.dirty ? t('versions.save_and_pin') : t('versions.pin_current')
})

/** The next V<n> after the highest: V1 among names of another kind. */
function nextLabel(items: NoteTag[]): string {
  const numbers = items.map((i) => /^V(\d+)$/i.exec(i.label)?.[1]).filter((n) => n !== undefined)
  return `V${Math.max(0, ...numbers.map(Number)) + 1}`
}

async function load() {
  versions.value = await getVersions(props.path)
  label.value = nextLabel(versions.value.items)
}

async function show() {
  // Measured where it falls by itself, not where it was moved last time.
  fitted.value = {}
  open.value = true
  error.value = ''
  commit.value = ''
  showCommits.value = false
  commits.value = null
  try {
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('versions.load_failed'))
  }
  await nextTick()
  fit()
  panel.value?.querySelector<HTMLElement>('input:not(:disabled), button:not(:disabled)')?.focus()
}

// The window clips what leaves it, and the button may stand anywhere in its
// bar: the panel stays inside, moved right of the button if it must.
const MARGIN = 8
const fitted = ref<Record<string, string>>({})
function fit() {
  const el = panel.value
  let clip = el?.parentElement ?? null
  while (clip && !/hidden|clip|auto|scroll/.test(getComputedStyle(clip).overflowX)) clip = clip.parentElement
  if (!el || !clip) return
  const room = clip.getBoundingClientRect()
  const box = el.getBoundingClientRect()
  const width = Math.min(box.width, room.width - 2 * MARGIN)
  fitted.value = {
    maxWidth: `${room.width - 2 * MARGIN}px`,
    maxHeight: `${room.bottom - box.top - MARGIN}px`,
    transform: `translateX(${Math.max(0, room.left + MARGIN - (box.right - width))}px)`,
  }
}

function hide(refocus = false) {
  if (!open.value) return
  open.value = false
  if (refocus) button.value?.focus()
}

const toggle = () => (open.value ? hide(true) : void show())

onClickOutside(root, () => hide(), { detectIframe: true })

// Tabbing away closes it. A blur to nowhere does not: a clicked button may
// take no focus (OverflowMenu), and the click outside handles the rest.
function onFocusOut(e: FocusEvent) {
  if (e.relatedTarget instanceof Node && !root.value?.contains(e.relatedTarget)) hide()
}

/** One change at a time, after a save of what is unsaved: its error stays in the panel. */
async function change(run: () => Promise<void>) {
  busy.value = true
  error.value = ''
  try {
    if (props.dirty && props.save && !(await props.save())) {
      error.value = t('versions.save_failed')
      return
    }
    await run()
    emit('changed')
    await load()
    commit.value = ''
  } catch (e) {
    error.value = errorText(e, t, t('versions.failed'))
  } finally {
    busy.value = false
  }
}

const choose = (tag: NoteTag | null) =>
  change(async () => {
    if (tag) await setValidVersion(props.path, tag.tag)
    else await clearValidVersion(props.path)
    announce(tag ? t('versions.pinned', { label: tag.label }) : t('versions.back_to_latest'))
  })

const chooseMCP = (tag: string) =>
  change(async () => {
    if (tag) await setMCPVersion(props.path, tag)
    else await clearMCPVersion(props.path)
    announce(t('versions.mcp_set', { label: mcpChoices.value.find((c) => c.value === tag)?.label ?? tag }))
  })

/** A name for the note as it is, or for the commit chosen, and the pin. */
function nameVersion(pin: boolean) {
  const name = label.value.trim()
  if (!name) return
  return change(async () => {
    const tag = await tagVersion(props.path, name, commit.value)
    if (pin) await setValidVersion(props.path, tag.tag)
    announce(pin ? t('versions.pinned', { label: name }) : t('history.tag_done', { label: name }))
  })
}

async function toggleCommits() {
  showCommits.value = !showCommits.value
  if (!showCommits.value) {
    commit.value = ''
    return
  }
  if (commits.value) return
  try {
    commits.value = await getHistory(props.path, COMMITS)
  } catch (e) {
    error.value = errorText(e, t, t('versions.load_failed'))
  }
}

watch(
  () => props.path,
  () => hide(),
)
</script>

<template>
  <div
    ref="root"
    class="relative shrink-0"
    data-version
    @focusout="onFocusOut"
  >
    <button
      ref="button"
      type="button"
      class="inline-flex h-control-sm max-w-40 items-center gap-1 rounded border border-border px-2 text-xs hover:bg-surface-hover"
      :class="{ 'text-warning': valid?.missing }"
      :title="stateTitle"
      aria-haspopup="dialog"
      :aria-expanded="open ? 'true' : 'false'"
      data-version-button
      @click="toggle"
    >
      <Pin
        class="h-3.5 w-3.5 shrink-0"
        :class="{ 'text-accent': props.valid }"
        aria-hidden="true"
      />
      <span class="truncate">{{ stateLabel }}</span>
      <!-- The dot is a colour only: its words are read with the button -->
      <span
        v-if="hasDraft"
        class="h-1.5 w-1.5 shrink-0 rounded-full bg-warning"
        aria-hidden="true"
        data-version-draft
      />
      <span
        v-if="hasDraft"
        class="sr-only"
      >{{ t('versions.draft_newer') }}</span>
    </button>
    <div
      v-if="open"
      ref="panel"
      class="absolute right-0 top-full z-20 mt-1 w-80 space-y-3 overflow-auto rounded border border-border bg-bg-elevated p-3 text-sm shadow-lg"
      :style="fitted"
      role="dialog"
      :aria-label="t('versions.title')"
      data-version-panel
      @keydown.esc.prevent.stop="hide(true)"
    >
      <div
        class="space-y-1"
        role="radiogroup"
        :aria-label="t('versions.readers_see')"
      >
        <p class="mb-1 text-xs font-semibold text-text-muted">
          {{ t('versions.readers_see') }}
        </p>
        <label class="flex items-center gap-2">
          <input
            type="radio"
            name="readers"
            :checked="!valid"
            :disabled="locked"
            data-version-latest
            @change="choose(null)"
          >
          <span>{{ t('versions.latest_choice') }}</span>
        </label>
        <div
          v-for="v in versions?.items ?? []"
          :key="v.tag"
          class="flex items-center gap-2"
          data-version-choice
        >
          <label class="flex min-w-0 flex-1 items-center gap-2">
            <input
              type="radio"
              name="readers"
              :value="v.tag"
              :checked="valid?.tag === v.tag"
              :disabled="locked"
              @change="choose(v)"
            >
            <span
              class="truncate"
              :title="v.message || v.tag"
            >{{ v.label }}</span>
            <DateTime
              class="shrink-0 text-xs text-text-muted"
              :value="v.date"
            />
          </label>
          <button
            type="button"
            class="shrink-0 rounded border border-border px-1.5 py-0.5 text-xs hover:bg-surface-hover"
            :aria-label="t('versions.open', { label: v.label })"
            data-version-open
            @click="emit('open', v)"
          >
            {{ t('versions.open_short') }}
          </button>
        </div>
        <p
          v-if="versions && !versions.items.length"
          class="text-xs text-text-muted"
        >
          {{ t('versions.none') }}
        </p>
      </div>
      <label
        v-if="versions"
        class="block space-y-1"
      >
        <span class="block text-xs font-semibold text-text-muted">{{ t('versions.mcp_see') }}</span>
        <select
          class="w-full rounded border border-border-input bg-bg px-1 py-1 text-sm"
          :value="versions.mcp?.tag ?? ''"
          :disabled="locked"
          data-version-mcp
          @change="chooseMCP(($event.target as HTMLSelectElement).value)"
        >
          <option
            v-for="c in mcpChoices"
            :key="c.value"
            :value="c.value"
          >
            {{ c.label }}
          </option>
        </select>
      </label>
      <p
        v-if="versions?.mcp?.missing"
        class="text-xs text-warning"
        data-version-mcp-missing
      >
        {{ t('versions.mcp_missing', { label: versions.mcp.label || versions.mcp.tag }) }}
      </p>
      <form
        v-if="canAdmin"
        class="space-y-2 border-t border-border pt-2"
        data-version-form
        @submit.prevent="nameVersion(true)"
      >
        <label class="flex items-center gap-2 text-xs text-text-muted">
          {{ t('versions.name') }}
          <input
            v-model="label"
            class="min-w-0 flex-1 rounded border border-border-input bg-bg px-2 py-1 text-sm text-text"
            maxlength="64"
            data-version-name
          >
        </label>
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="submit"
            class="rounded bg-accent px-2 py-1 text-xs text-accent-fg hover:bg-accent-hover disabled:opacity-60"
            :disabled="busy || !label.trim()"
            :aria-busy="busy ? 'true' : undefined"
            data-version-pin
          >
            {{ pinLabel }}
          </button>
          <button
            type="button"
            class="rounded border border-border px-2 py-1 text-xs hover:bg-surface-hover disabled:opacity-60"
            :disabled="busy || !label.trim()"
            data-version-tag
            @click="nameVersion(false)"
          >
            {{ t('versions.tag_only') }}
          </button>
        </div>
        <button
          type="button"
          class="text-xs text-text-muted underline hover:text-text"
          :aria-expanded="showCommits ? 'true' : 'false'"
          data-version-commits
          @click="toggleCommits"
        >
          {{ t('versions.other_commit') }}
        </button>
        <ul
          v-if="showCommits && commits"
          class="max-h-40 space-y-1 overflow-auto"
        >
          <li
            v-for="c in commits"
            :key="c.sha"
          >
            <button
              type="button"
              class="w-full truncate rounded px-1 text-left text-xs hover:bg-surface-hover"
              :class="{ 'bg-surface-hover': commit === c.sha }"
              :aria-pressed="commit === c.sha"
              :title="c.subject"
              :data-version-commit="c.sha"
              @click="commit = commit === c.sha ? '' : c.sha"
            >
              <code class="font-mono text-accent">{{ c.short_sha }}</code> {{ c.subject }}
            </button>
          </li>
        </ul>
      </form>
      <p
        v-else-if="versions"
        class="text-xs text-text-muted"
        data-version-admin-only
      >
        {{ t('versions.admin_only') }}
      </p>
      <ErrorMessage
        v-if="error"
        :text="error"
        class="text-xs"
        data-version-error
      />
      <button
        type="button"
        class="block text-xs text-text-muted underline hover:text-text"
        data-version-history
        @click="emit('history')"
      >
        {{ t('versions.full_history') }}
      </button>
    </div>
  </div>
</template>

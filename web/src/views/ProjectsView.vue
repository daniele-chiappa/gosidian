<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { nextTick, onMounted, ref, inject, watch } from 'vue'
import {
  listProjects,
  createProject,
  updateProject,
  deleteProject,
  exportProject,
  type Project,
} from '@/api/projects'
import { visibilityHelp, visibilityLabel, type Visibility } from '@/api/access'
import { getSettings } from '@/api/settings'
import { useTreeStore } from '@/stores/tree'
import { useAuthStore } from '@/stores/auth'
import { useAccessStore } from '@/stores/access'
import { useUIStore } from '@/stores/ui'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { Lock, Globe, Users } from 'lucide-vue-next'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import { askText, confirmAction } from '@/composables/useConfirm'

const { t, te } = useI18n()

// A project chosen in the palette, with when it was chosen: the palette
// opened the whole list, from its top (BUG-160).
const props = defineProps<{ project?: string; projectAt?: number }>()

const projects = ref<Project[]>([])
const loading = ref(false)
// Only the first load puts "Loading…" in place of the list: a reload after
// an action took the whole list down and back, focus and scroll with it.
const loaded = ref(false)
const error = ref<string | null>(null)
// What the last rename did to the references to the project's files.
const notice = ref<string | null>(null)
const newName = ref('')
const treeStore = useTreeStore()
const auth = useAuthStore()
const access = useAccessStore()
const ui = useUIStore()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

// Master switches: use_anchors/use_globals only take effect when the server
// master switch is on. We surface them so the toggles can flag "no effect yet".
const anchorsMaster = ref(false)
const globalsMaster = ref(false)

const VISIBILITIES: Visibility[] = ['private', 'internal', 'public']

/** Explore a project as a graph window (its link neighbourhood). */
function openProjectGraph(name: string) {
  openWindow({
    type: 'graph',
    key: 'graph:project:' + name,
    title: `${t('nav.graph')} · ${name}`,
    props: { project: name },
  })
}

/** Who holds a grant on a project; editable by the owner and project admins. */
function openProjectAccess(name: string) {
  openWindow({
    type: 'project-members',
    key: 'project-members:' + name,
    title: t('members.title', { project: name }),
    props: { project: name },
  })
}

async function load() {
  loading.value = true
  error.value = null
  try {
    projects.value = await listProjects()
    loaded.value = true
  } catch (e) {
    error.value = errorText(e, t, t('projects.load_failed'))
  } finally {
    loading.value = false
  }
}

/** Reload the list and the per-project levels the sidebar and tabs rely on. */
async function refresh() {
  await load()
  void access.load()
}

async function handleCreate() {
  if (!newName.value.trim()) return
  try {
    await createProject(newName.value.trim())
    newName.value = ''
    await refresh()
    treeStore.refresh()
  } catch (e) {
    error.value = errorText(e, t, t('projects.create_failed'))
  }
}

async function apply(p: Project, patch: Parameters<typeof updateProject>[1], label: string) {
  error.value = null
  try {
    await updateProject(p.name, patch)
    await refresh()
    // The IN folder's node of the tree comes and goes with these.
    if ('use_in' in patch || 'use_pipelines' in patch) treeStore.refresh()
  } catch (e) {
    error.value = errorText(e, t, t('projects.update_failed', { what: label }))
  }
}

function changeVisibility(p: Project, value: string) {
  if (!VISIBILITIES.includes(value as Visibility) || value === p.visibility) return
  void apply(p, { visibility: value as Visibility }, t('projects.visibility'))
}

function globalsTitle(p: Project): string {
  if (!globalsMaster.value)
    return t('projects.flag.globals_master_off')
  return p.use_globals ? t('projects.flag.globals_on') : t('projects.flag.globals_off')
}

function anchorsTitle(p: Project): string {
  if (!anchorsMaster.value)
    return t('projects.flag.anchors_master_off')
  return p.use_anchors ? t('projects.flag.anchors_on') : t('projects.flag.anchors_off')
}

function pipelinesTitle(p: Project): string {
  if (!ui.pipelinesEnabled) return t('projects.flag.pipelines_master_off')
  return p.use_pipelines ? t('projects.flag.pipelines_on') : t('projects.flag.pipelines_off')
}

function tagsTitle(p: Project): string {
  return p.pipeline_tags ? t('projects.flag.tags_on') : t('projects.flag.tags_off')
}

function inTitle(p: Project): string {
  return p.use_in ? t('projects.flag.in_on') : t('projects.flag.in_off')
}

function inGitTitle(p: Project): string {
  return p.track_in_git ? t('projects.flag.in_git_on') : t('projects.flag.in_git_off')
}

/** Putting the IN folder in git can mean gigabytes in the history: asked first. */
async function toggleINGit(p: Project) {
  if (!p.track_in_git && !(await confirmAction(t('projects.flag.in_git_confirm', { name: p.name }), { confirmLabel: t('projects.flag.in_git_confirm_ok') })))
    return
  await apply(p, { track_in_git: !p.track_in_git }, flagWord('IN git'))
}

function accessTitle(p: Project): string {
  const who = t('projects.grants', { accounts: p.members_count, teams: p.teams_count })
  return p.access === 'admin' ? t('projects.grants_manage', { who }) : t('projects.grants_see', { who })
}

/** Master switches decide whether use_anchors/use_globals have any effect.
 *  Non-fatal: on failure the toggles still work, they just lose the hint. */
async function loadMasters() {
  try {
    const s = await getSettings()
    anchorsMaster.value = s.anchors_enabled
    globalsMaster.value = s.globals_enabled
  } catch {
    /* member+ only; guests never see these toggles anyway */
  }
}

async function rename(p: Project) {
  const newSlug = (await askText(t('projects.rename_prompt', { name: p.name }), p.name, { confirmLabel: t('projects.rename') }))?.trim()
  if (!newSlug || newSlug === p.name) return
  notice.value = null
  try {
    notice.value = renameNotice(await updateProject(p.name, { new_name: newSlug }))
    await refresh()
    treeStore.refresh()
  } catch (e) {
    error.value = errorText(e, t, t('projects.rename_failed'))
  }
}

/** What a rename did to the references to the project's files (BUG-153):
 *  the notes it rewrote, and those that keep the old name, in projects the
 *  user may not write. Nothing when no note named the project. */
function renameNotice(p: Project): string | null {
  const l = p.links
  const parts: string[] = []
  if (l && l.rewritten > 0) parts.push(t('projects.renamed_links', { count: l.rewritten, name: p.name }))
  if (l && l.not_rewritten > 0) parts.push(t('projects.renamed_links_left', { count: l.not_rewritten }))
  return parts.length ? parts.join(' ') : null
}

/** Name of the project whose zip is being prepared (the button waits). */
const exporting = ref<string | null>(null)

async function exportZip(p: Project) {
  error.value = null
  exporting.value = p.name
  try {
    await exportProject(p.name)
  } catch (e) {
    error.value = errorText(e, t, t('projects.export_failed'))
  } finally {
    exporting.value = null
  }
}

async function destroy(p: Project) {
  if (!(await confirmAction(t('projects.confirm_delete', { name: p.name, count: p.note_count }), { confirmLabel: t('common.delete') })))
    return
  try {
    await deleteProject(p.name)
    await refresh()
    treeStore.refresh()
  } catch (e) {
    error.value = errorText(e, t, t('projects.delete_failed'))
  }
}

/** The flag chips, in the order of the row, for the legend. */
const FLAG_NAMES = ['skip-git', 'hidden', 'globals', 'anchors', 'tag-vocab', 'lean-read', 'mirror', 'pipelines', 'git tags', 'IN', 'IN git'] as const
type FlagName = (typeof FLAG_NAMES)[number]

// A flag is said in words; its code, which the docs and the API use, stays
// in the tooltip and the legend (IMP-220, F-25).
const flagKey = (f: FlagName) => f.replace(/[- ]/g, '_')
const flagWord = (f: FlagName) => t(`projects.flag_name.${flagKey(f)}`)
const flagTitle = (f: FlagName, text: string) => `${f} — ${text}`
// The reader's level in words, the code in its tooltip (IMP-220, F-20).
const levelName = (l: string) => (te(`members.level_name.${l}`) ? t(`members.level_name.${l}`) : l)

onMounted(() => {
  load()
  loadMasters()
})

// The project chosen takes the focus on its name, and so comes into view,
// once the list is there and at each choice after (BUG-160).
const root = ref<HTMLElement | null>(null)
watch(
  () => [props.project, props.projectAt, loaded.value],
  async () => {
    if (!props.project || !loaded.value) return
    await nextTick()
    const names = Array.from(root.value?.querySelectorAll<HTMLElement>('[data-project-name]') ?? [])
    names.find((b) => b.dataset.projectName === props.project)?.focus()
  },
)
</script>

<template>
  <div
    ref="root"
    class="p-8 max-w-4xl mx-auto"
  >
    <!-- The window's title bar names the view, the heading is for screen
         readers; the flags' meaning is in the legend and in each chip's
         tooltip. -->
    <h1
      class="sr-only"
      tabindex="-1"
      data-window-heading
    >
      {{ t('projects.title') }}
    </h1>
    <div class="mb-6 space-y-2">
      <p class="text-sm text-text-muted">
        {{ t('projects.intro') }}
      </p>
      <details v-if="projects.some((p) => p.access === 'admin')" class="text-sm" data-flags-legend>
        <summary class="cursor-pointer text-text-muted hover:text-text">{{ t('projects.legend.title') }}</summary>
        <dl class="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-text-muted">
          <template v-for="f in FLAG_NAMES" :key="f">
            <dt class="text-text">
              {{ flagWord(f) }}
              <code class="text-xs text-text-muted">{{ f }}</code>
            </dt>
            <dd>{{ t(`projects.legend.${flagKey(f)}`) }}</dd>
          </template>
        </dl>
      </details>
    </div>

    <form
      v-if="auth.canWrite && (auth.isOwner || access.canCreateProjects)"
      class="flex gap-2 mb-6"
      @submit.prevent="handleCreate"
    >
      <input
        v-model.trim="newName"
        type="text"
        :placeholder="t('projects.new_placeholder')"
        :aria-label="t('projects.new_label')"
        class="h-control py-0 flex-1 rounded bg-bg-elevated border border-border-input px-3 focus:outline-none focus:ring-2 focus:ring-focus"
      />
      <button
        type="submit"
        class="h-control px-3 rounded bg-accent text-accent-fg hover:bg-accent-hover"
      >{{ t('common.create') }}</button>
    </form>

    <p v-if="loading && !loaded" class="text-text-muted">{{ t('common.loading') }}</p>
    <ErrorMessage v-if="error" :text="error" class="mb-3" />
    <p
      v-if="notice"
      role="status"
      class="text-sm text-text-muted mb-3"
    >
      {{ notice }}
    </p>

    <ul v-if="loaded" class="space-y-2">
      <li
        v-for="p in projects"
        :key="p.name"
        class="rounded border border-border bg-surface px-4 py-3 flex items-center gap-2 flex-wrap"
      >
        <!-- Visibility cue + name -->
        <span
          class="inline-flex items-center justify-center w-5 shrink-0"
          :title="visibilityHelp(p.visibility)"
        >
          <Lock v-if="p.visibility === 'private'" class="w-3.5 h-3.5 text-text-muted" :aria-label="visibilityLabel('private')" />
          <Globe v-else-if="p.visibility === 'public'" class="w-3.5 h-3.5 text-success" :aria-label="visibilityLabel('public')" />
          <Users v-else class="w-3.5 h-3.5 text-info" :aria-label="visibilityLabel('internal')" />
        </span>
        <button
          type="button"
          class="font-medium hover:text-accent flex-1 text-left min-w-[8rem]"
          :title="t('projects.open_graph')"
          :data-project-name="p.name"
          @click="openProjectGraph(p.name)"
        >{{ p.name }}</button>
        <span class="text-xs text-text-muted">{{ t('projects.notes', { n: p.note_count }, p.note_count) }}</span>

        <!-- Your own level -->
        <span
          class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded"
          :class="p.access === 'admin' ? 'bg-accent/10 text-accent' : p.access === 'write' ? 'bg-success/10 text-success' : 'border border-border text-text-muted'"
          :title="`${p.access} — ${t('projects.your_level', { level: levelName(p.access) })}`"
          data-level
        >{{ levelName(p.access) }}</span>

        <!-- Visibility: a selector for project admins, a badge for everyone else -->
        <select
          v-if="p.access === 'admin'"
          class="h-control-sm py-0 text-xs rounded bg-bg-elevated border border-border-input px-2"
          :value="p.visibility"
          :title="visibilityHelp(p.visibility)"
          :aria-label="t('projects.visibility_of', { name: p.name })"
          @change="changeVisibility(p, ($event.target as HTMLSelectElement).value)"
        >
          <option
            v-for="v in VISIBILITIES"
            :key="v"
            :value="v"
            :disabled="v === 'public' && !auth.isOwner"
          >{{ visibilityLabel(v) }}{{ v === 'public' && !auth.isOwner ? ` (${t('projects.owner_only')})` : '' }}</option>
        </select>
        <span
          v-else
          class="text-xs px-2 py-1 rounded border border-border text-text-muted"
          :title="visibilityHelp(p.visibility)"
        >{{ visibilityLabel(p.visibility).toLowerCase() }}</span>

        <!-- Grants: accounts + teams, said in words (IMP-220, F-25); opens
             the Access window -->
        <button
          type="button"
          class="h-control-sm text-xs px-2 rounded border border-border inline-flex items-center gap-1 hover:bg-surface-hover"
          :title="accessTitle(p)"
          data-grants
          @click="openProjectAccess(p.name)"
        >
          <Users
            class="w-3 h-3"
            aria-hidden="true"
          />
          <span>{{ t('projects.grants_short', { accounts: p.members_count, teams: p.teams_count }) }}</span>
        </button>

        <button
          v-if="!auth.isAnonymous"
          type="button"
          class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover disabled:opacity-50"
          :title="t('projects.export_hint')"
          :disabled="exporting !== null"
          @click="exportZip(p)"
        >{{ exporting === p.name ? t('projects.exporting') : t('projects.export') }}</button>

        <template v-if="p.access === 'admin'">
          <button
            type="button"
            class="h-control-sm text-xs px-2 rounded"
            :class="p.skip_git_sync ? 'bg-warning/10 text-warning' : 'border border-border'"
            :title="flagTitle('skip-git', p.skip_git_sync ? t('projects.flag.git_on') : t('projects.flag.git_off'))"
            :aria-pressed="!!p.skip_git_sync"
            @click="apply(p, { skip_git_sync: !p.skip_git_sync }, flagWord('skip-git'))"
          >{{ flagWord('skip-git') }}</button>
          <button
            type="button"
            class="h-control-sm text-xs px-2 rounded"
            :class="p.hidden_from_mcp ? 'bg-warning/10 text-warning' : 'border border-border'"
            :title="flagTitle('hidden', p.hidden_from_mcp ? t('projects.flag.hidden_on') : t('projects.flag.hidden_off'))"
            :aria-pressed="!!p.hidden_from_mcp"
            @click="apply(p, { hidden_from_mcp: !p.hidden_from_mcp }, flagWord('hidden'))"
          >{{ flagWord('hidden') }}</button>
          <!-- A flag whose switch in Settings is off has no effect: a dashed
               outline says so, at full contrast, where a faded label fell
               under 4.5:1 (IMP-221). -->
          <button
            type="button"
            class="h-control-sm text-xs px-2 rounded"
            :class="[
              p.use_globals ? 'bg-accent/10 text-accent' : '',
              globalsMaster ? (p.use_globals ? '' : 'border border-border') : 'border border-dashed border-current',
            ]"
            :title="flagTitle('globals', globalsTitle(p))"
            :aria-pressed="!!p.use_globals"
            @click="apply(p, { use_globals: !p.use_globals }, flagWord('globals'))"
          >{{ flagWord('globals') }}</button>
          <button
            type="button"
            class="h-control-sm text-xs px-2 rounded"
            :class="[
              p.use_anchors ? 'bg-accent/10 text-accent' : '',
              anchorsMaster ? (p.use_anchors ? '' : 'border border-border') : 'border border-dashed border-current',
            ]"
            :title="flagTitle('anchors', anchorsTitle(p))"
            :aria-pressed="!!p.use_anchors"
            @click="apply(p, { use_anchors: !p.use_anchors }, flagWord('anchors'))"
          >{{ flagWord('anchors') }}</button>
          <button
            type="button"
            class="h-control-sm text-xs px-2 rounded"
            :class="p.use_tag_vocabulary ? 'bg-accent/10 text-accent' : 'border border-border'"
            :title="flagTitle('tag-vocab', p.use_tag_vocabulary ? t('projects.flag.vocab_on') : t('projects.flag.vocab_off'))"
            :aria-pressed="!!p.use_tag_vocabulary"
            @click="apply(p, { use_tag_vocabulary: !p.use_tag_vocabulary }, flagWord('tag-vocab'))"
          >{{ flagWord('tag-vocab') }}</button>
          <button
            type="button"
            class="h-control-sm text-xs px-2 rounded"
            :class="p.lean_read_bootstrap ? 'bg-accent/10 text-accent' : 'border border-border'"
            :title="flagTitle('lean-read', p.lean_read_bootstrap ? t('projects.flag.lean_on') : t('projects.flag.lean_off'))"
            :aria-pressed="!!p.lean_read_bootstrap"
            @click="apply(p, { lean_read_bootstrap: !p.lean_read_bootstrap }, flagWord('lean-read'))"
          >{{ flagWord('lean-read') }}</button>
          <button
            type="button"
            class="h-control-sm text-xs px-2 rounded"
            :class="p.allow_local_mirror ? 'bg-accent/10 text-accent' : 'border border-border'"
            :title="flagTitle('mirror', p.allow_local_mirror ? t('projects.flag.mirror_on') : t('projects.flag.mirror_off'))"
            :aria-pressed="!!p.allow_local_mirror"
            @click="apply(p, { allow_local_mirror: !p.allow_local_mirror }, flagWord('mirror'))"
          >{{ flagWord('mirror') }}</button>
          <button
            type="button"
            class="h-control-sm text-xs px-2 rounded"
            :class="[
              p.use_pipelines ? 'bg-accent/10 text-accent' : '',
              ui.pipelinesEnabled ? (p.use_pipelines ? '' : 'border border-border') : 'border border-dashed border-current',
            ]"
            :title="flagTitle('pipelines', pipelinesTitle(p))"
            :aria-pressed="!!p.use_pipelines"
            @click="apply(p, { use_pipelines: !p.use_pipelines }, flagWord('pipelines'))"
          >{{ flagWord('pipelines') }}</button>
          <button
            v-if="p.use_pipelines"
            type="button"
            class="h-control-sm text-xs px-2 rounded"
            :class="p.pipeline_tags ? 'bg-accent/10 text-accent' : 'border border-border'"
            :title="flagTitle('git tags', tagsTitle(p))"
            :aria-pressed="!!p.pipeline_tags"
            data-flag="pipeline_tags"
            @click="apply(p, { pipeline_tags: !p.pipeline_tags }, flagWord('git tags'))"
          >{{ flagWord('git tags') }}</button>
          <button
            type="button"
            class="h-control-sm text-xs px-2 rounded"
            :class="p.use_in ? 'bg-accent/10 text-accent' : 'border border-border'"
            :title="flagTitle('IN', inTitle(p))"
            :aria-pressed="!!p.use_in"
            data-flag="use_in"
            @click="apply(p, { use_in: !p.use_in }, flagWord('IN'))"
          >{{ flagWord('IN') }}</button>
          <button
            v-if="p.use_in"
            type="button"
            class="h-control-sm text-xs px-2 rounded"
            :class="p.track_in_git ? 'bg-accent/10 text-accent' : 'border border-border'"
            :title="flagTitle('IN git', inGitTitle(p))"
            :aria-pressed="!!p.track_in_git"
            data-flag="track_in_git"
            @click="toggleINGit(p)"
          >{{ flagWord('IN git') }}</button>
          <button
            type="button"
            class="h-control-sm text-xs px-2 rounded hover:bg-surface-hover"
            @click="rename(p)"
          >{{ t('projects.rename') }}</button>
          <button
            type="button"
            class="h-control-sm text-xs px-2 rounded text-danger hover:bg-surface-hover"
            @click="destroy(p)"
          >{{ t('common.delete') }}</button>
        </template>
      </li>
      <li v-if="projects.length === 0" class="text-sm text-text-muted">
        {{ t('projects.none') }}
      </li>
    </ul>
  </div>
</template>

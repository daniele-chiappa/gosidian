<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { onMounted, ref, inject } from 'vue'
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
import { useWindowsStore, type OpenSpec } from 'plancia'
import { Lock, Globe, Users, UsersRound } from 'lucide-vue-next'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

const { t } = useI18n()

const projects = ref<Project[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const newName = ref('')
const treeStore = useTreeStore()
const auth = useAuthStore()
const access = useAccessStore()
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
    title: `Graph · ${name}`,
    props: { project: name },
  })
}

/** Who holds a grant on a project; editable by the owner and project admins. */
function openProjectAccess(name: string) {
  openWindow({
    type: 'project-members',
    key: 'project-members:' + name,
    title: `Access · ${name}`,
    props: { project: name },
  })
}

async function load() {
  loading.value = true
  error.value = null
  try {
    projects.value = await listProjects()
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
  const newSlug = prompt(t('projects.rename_prompt', { name: p.name }), p.name)
  if (!newSlug || newSlug === p.name) return
  try {
    await updateProject(p.name, { new_name: newSlug })
    await refresh()
    treeStore.refresh()
  } catch (e) {
    error.value = errorText(e, t, t('projects.rename_failed'))
  }
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
  if (!confirm(t('projects.confirm_delete', { name: p.name, count: p.note_count }))) return
  try {
    await deleteProject(p.name)
    await refresh()
    treeStore.refresh()
  } catch (e) {
    error.value = errorText(e, t, t('projects.delete_failed'))
  }
}

onMounted(() => {
  load()
  loadMasters()
})
</script>

<template>
  <div class="p-8 max-w-4xl mx-auto">
    <h1 class="text-2xl font-semibold mb-1">{{ t('projects.title') }}</h1>
    <p class="text-sm text-text-muted mb-6">
      {{ t('projects.intro') }}
    </p>

    <form
      v-if="auth.canWrite && (auth.isOwner || access.canCreateProjects)"
      class="flex gap-2 mb-6"
      @submit.prevent="handleCreate"
    >
      <input
        v-model.trim="newName"
        type="text"
        :placeholder="t('projects.new_placeholder')"
        class="flex-1 rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
      />
      <button
        type="submit"
        class="px-3 py-2 rounded bg-accent text-accent-fg hover:bg-accent-hover"
      >{{ t('common.create') }}</button>
    </form>

    <p v-if="loading" class="text-text-muted">{{ t('common.loading') }}</p>
    <ErrorMessage v-if="error" :text="error" class="mb-3" />

    <ul v-if="!loading" class="space-y-2">
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
          @click="openProjectGraph(p.name)"
        >{{ p.name }}</button>
        <span class="text-xs text-text-muted">{{ t('projects.notes', { n: p.note_count }, p.note_count) }}</span>

        <!-- Your own level -->
        <span
          class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded"
          :class="p.access === 'admin' ? 'bg-accent/20 text-accent' : p.access === 'write' ? 'bg-success/20 text-success' : 'border border-border text-text-muted'"
          :title="t('projects.your_level', { level: p.access })"
        >{{ p.access }}</span>

        <!-- Visibility: a selector for project admins, a badge for everyone else -->
        <select
          v-if="p.access === 'admin'"
          class="text-xs rounded bg-bg-elevated border border-border px-2 py-1"
          :value="p.visibility"
          :title="visibilityHelp(p.visibility)"
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

        <!-- Grants: accounts + teams; opens the Access window -->
        <button
          type="button"
          class="text-xs px-2 py-1 rounded border border-border inline-flex items-center gap-1 hover:bg-surface-hover"
          :title="accessTitle(p)"
          @click="openProjectAccess(p.name)"
        >
          <Users class="w-3 h-3" />
          <span>{{ p.members_count }}</span>
          <span class="text-text-muted">·</span>
          <UsersRound class="w-3 h-3" />
          <span>{{ p.teams_count }}</span>
        </button>

        <button
          v-if="!auth.isAnonymous"
          type="button"
          class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover disabled:opacity-50"
          :title="t('projects.export_hint')"
          :disabled="exporting !== null"
          @click="exportZip(p)"
        >{{ exporting === p.name ? t('projects.exporting') : t('projects.export') }}</button>

        <template v-if="p.access === 'admin'">
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.skip_git_sync ? 'bg-warning/20 text-warning' : 'border border-border'"
            :title="p.skip_git_sync ? t('projects.flag.git_on') : t('projects.flag.git_off')"
            @click="apply(p, { skip_git_sync: !p.skip_git_sync }, 'skip-git')"
          >skip-git</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.hidden_from_mcp ? 'bg-warning/20 text-warning' : 'border border-border'"
            :title="p.hidden_from_mcp ? t('projects.flag.hidden_on') : t('projects.flag.hidden_off')"
            @click="apply(p, { hidden_from_mcp: !p.hidden_from_mcp }, 'hidden')"
          >hidden</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="[
              p.use_globals ? 'bg-accent/20 text-accent' : 'border border-border',
              globalsMaster ? '' : 'opacity-50',
            ]"
            :title="globalsTitle(p)"
            @click="apply(p, { use_globals: !p.use_globals }, 'globals')"
          >globals</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="[
              p.use_anchors ? 'bg-accent/20 text-accent' : 'border border-border',
              anchorsMaster ? '' : 'opacity-50',
            ]"
            :title="anchorsTitle(p)"
            @click="apply(p, { use_anchors: !p.use_anchors }, 'anchors')"
          >anchors</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.use_tag_vocabulary ? 'bg-accent/20 text-accent' : 'border border-border'"
            :title="p.use_tag_vocabulary ? t('projects.flag.vocab_on') : t('projects.flag.vocab_off')"
            @click="apply(p, { use_tag_vocabulary: !p.use_tag_vocabulary }, 'tag-vocab')"
          >tag-vocab</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.lean_read_bootstrap ? 'bg-accent/20 text-accent' : 'border border-border'"
            :title="p.lean_read_bootstrap ? t('projects.flag.lean_on') : t('projects.flag.lean_off')"
            @click="apply(p, { lean_read_bootstrap: !p.lean_read_bootstrap }, 'lean-read')"
          >lean-read</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.allow_local_mirror ? 'bg-accent/20 text-accent' : 'border border-border'"
            :title="p.allow_local_mirror ? t('projects.flag.mirror_on') : t('projects.flag.mirror_off')"
            @click="apply(p, { allow_local_mirror: !p.allow_local_mirror }, 'mirror')"
          >mirror</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded hover:bg-surface-hover"
            @click="rename(p)"
          >{{ t('projects.rename') }}</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
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

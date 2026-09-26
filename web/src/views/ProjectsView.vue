<script setup lang="ts">
import { onMounted, ref, inject } from 'vue'
import {
  listProjects,
  createProject,
  updateProject,
  deleteProject,
  type Project,
} from '@/api/projects'
import { VISIBILITY_HELP, VISIBILITY_LABEL, type Visibility } from '@/api/access'
import { getSettings } from '@/api/settings'
import { useTreeStore } from '@/stores/tree'
import { useAuthStore } from '@/stores/auth'
import { useAccessStore } from '@/stores/access'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { Lock, Globe, Users, UsersRound } from 'lucide-vue-next'

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
    error.value = e instanceof Error ? e.message : 'Failed to load'
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
    treeStore.invalidateAll()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Create failed'
  }
}

async function apply(p: Project, patch: Parameters<typeof updateProject>[1], label: string) {
  error.value = null
  try {
    await updateProject(p.name, patch)
    await refresh()
  } catch (e) {
    error.value = e instanceof Error ? `${label}: ${e.message}` : `${label} failed`
  }
}

function changeVisibility(p: Project, value: string) {
  if (!VISIBILITIES.includes(value as Visibility) || value === p.visibility) return
  void apply(p, { visibility: value as Visibility }, 'Visibility')
}

function globalsTitle(p: Project): string {
  if (!globalsMaster.value)
    return 'Global-projects master switch (GOSIDIAN_GLOBAL_ENABLED) is off — this flag has no effect until it is enabled on the server.'
  return p.use_globals
    ? 'Globals on: this project merges the shared global skills/agents at bootstrap. Click to disable.'
    : 'Click to merge the shared global skills/agents into this project at bootstrap.'
}

function anchorsTitle(p: Project): string {
  if (!anchorsMaster.value)
    return 'Agent-anchor master switch (GOSIDIAN_ANCHORS_ENABLED) is off — this flag has no effect until it is enabled on the server.'
  return p.use_anchors
    ? "Anchors on: this project's vault agents are materialised as local .claude/agents anchors at bootstrap. Click to disable."
    : 'Click to materialise this project’s vault agents as local subagent anchors at bootstrap.'
}

function accessTitle(p: Project): string {
  const who = `${p.members_count} account(s) and ${p.teams_count} team(s) hold a grant`
  return p.access === 'admin' ? `${who} — click to manage` : `${who} — click to see who`
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
  const newSlug = prompt(`Rename "${p.name}" to:`, p.name)
  if (!newSlug || newSlug === p.name) return
  try {
    await updateProject(p.name, { new_name: newSlug })
    await refresh()
    treeStore.invalidateAll()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Rename failed'
  }
}

async function destroy(p: Project) {
  if (!confirm(`Delete project "${p.name}" and ${p.note_count} note(s)?`)) return
  try {
    await deleteProject(p.name)
    await refresh()
    treeStore.invalidateAll()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Delete failed'
  }
}

onMounted(() => {
  load()
  loadMasters()
})
</script>

<template>
  <div class="p-8 max-w-4xl mx-auto">
    <h1 class="text-2xl font-semibold mb-1">Projects</h1>
    <p class="text-sm text-text-muted mb-6">
      Top-level vault folders. <em>Visibility</em> says who can read a project —
      <em>private</em> (accounts with a grant), <em>internal</em> (every member) or
      <em>public</em> (every account, guests included); writing and administering
      always come from a grant, to an account or to a team (Access). Your own level
      shows on each row.
      <em>skip-git</em> excludes from auto-commit; <em>hidden</em> keeps the project
      invisible to MCP agents; <em>globals</em> merges the shared global skills/agents
      at bootstrap; <em>anchors</em> materialises vault agents as local subagent files
      at bootstrap (both need their server master switch on — dimmed when off);
      <em>tag-vocab</em> lets the project extend the lint tag vocabulary from
      memory/conventions.md; <em>lean-read</em> gives tokens that cannot write a
      shorter bootstrap (reading directives only: fewer tokens, less context);
      <em>mirror</em> lets readers keep a local read-only copy of the project
      (the notes leave the server; every sync is audited).
    </p>

    <form
      v-if="auth.canWrite && (auth.isOwner || access.canCreateProjects)"
      class="flex gap-2 mb-6"
      @submit.prevent="handleCreate"
    >
      <input
        v-model.trim="newName"
        type="text"
        placeholder="new-project"
        class="flex-1 rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
      />
      <button
        type="submit"
        class="px-3 py-2 rounded bg-accent text-accent-fg hover:bg-accent-hover"
      >Create</button>
    </form>

    <p v-if="loading" class="text-text-muted">Loading…</p>
    <p v-if="error" class="text-danger mb-3">{{ error }}</p>

    <ul v-if="!loading" class="space-y-2">
      <li
        v-for="p in projects"
        :key="p.name"
        class="rounded border border-border bg-surface px-4 py-3 flex items-center gap-2 flex-wrap"
      >
        <!-- Visibility cue + name -->
        <span
          class="inline-flex items-center justify-center w-5 shrink-0"
          :title="VISIBILITY_HELP[p.visibility]"
        >
          <Lock v-if="p.visibility === 'private'" class="w-3.5 h-3.5 text-text-muted" aria-label="Private" />
          <Globe v-else-if="p.visibility === 'public'" class="w-3.5 h-3.5 text-success" aria-label="Public" />
          <Users v-else class="w-3.5 h-3.5 text-info" aria-label="Internal" />
        </span>
        <button
          type="button"
          class="font-medium hover:text-accent flex-1 text-left min-w-[8rem]"
          title="Open project graph"
          @click="openProjectGraph(p.name)"
        >{{ p.name }}</button>
        <span class="text-xs text-text-muted">{{ p.note_count }} notes</span>

        <!-- Your own level -->
        <span
          class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded"
          :class="p.access === 'admin' ? 'bg-accent/20 text-accent' : p.access === 'write' ? 'bg-success/20 text-success' : 'border border-border text-text-muted'"
          :title="`Your level on this project: ${p.access}`"
        >{{ p.access }}</span>

        <!-- Visibility: a selector for project admins, a badge for everyone else -->
        <select
          v-if="p.access === 'admin'"
          class="text-xs rounded bg-bg-elevated border border-border px-2 py-1"
          :value="p.visibility"
          :title="VISIBILITY_HELP[p.visibility]"
          @change="changeVisibility(p, ($event.target as HTMLSelectElement).value)"
        >
          <option
            v-for="v in VISIBILITIES"
            :key="v"
            :value="v"
            :disabled="v === 'public' && !auth.isOwner"
          >{{ VISIBILITY_LABEL[v] }}{{ v === 'public' && !auth.isOwner ? ' (owner only)' : '' }}</option>
        </select>
        <span
          v-else
          class="text-xs px-2 py-1 rounded border border-border text-text-muted"
          :title="VISIBILITY_HELP[p.visibility]"
        >{{ VISIBILITY_LABEL[p.visibility].toLowerCase() }}</span>

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

        <template v-if="p.access === 'admin'">
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.skip_git_sync ? 'bg-warning/20 text-warning' : 'border border-border'"
            :title="p.skip_git_sync ? 'Click to re-enable git sync' : 'Click to skip from git sync'"
            @click="apply(p, { skip_git_sync: !p.skip_git_sync }, 'skip-git')"
          >skip-git</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.hidden_from_mcp ? 'bg-warning/20 text-warning' : 'border border-border'"
            :title="p.hidden_from_mcp ? 'Click to expose to MCP again' : 'Click to hide from MCP'"
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
            :title="p.use_tag_vocabulary
              ? 'Tag vocabulary on: memory_lint accepts the extra tags declared in this project\'s memory/conventions.md frontmatter (tag_vocabulary). Click to disable.'
              : 'Click to let this project extend the lint tag vocabulary via memory/conventions.md frontmatter (tag_vocabulary: exact tags or ns:* wildcards).'"
            @click="apply(p, { use_tag_vocabulary: !p.use_tag_vocabulary }, 'tag-vocab')"
          >tag-vocab</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.lean_read_bootstrap ? 'bg-accent/20 text-accent' : 'border border-border'"
            :title="p.lean_read_bootstrap
              ? 'Lean read bootstrap on: tokens that cannot write here get only the reading directives (fewer tokens, less context about how the memory is organized). Click to disable.'
              : 'Click to give tokens that cannot write here a lean bootstrap: reading directives only. Saves tokens per session, drops the context about writing and note formats.'"
            @click="apply(p, { lean_read_bootstrap: !p.lean_read_bootstrap }, 'lean-read')"
          >lean-read</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.allow_local_mirror ? 'bg-accent/20 text-accent' : 'border border-border'"
            :title="p.allow_local_mirror
              ? 'Local mirror on: tokens that can read this project may keep a read-only copy of its notes on their machine (gosidian mirror sync). Every sync is audited. Click to disable.'
              : 'Click to let tokens that can read this project keep a read-only copy of its notes on their machine (gosidian mirror sync): cheaper reading for agents, but the notes leave the server.'"
            @click="apply(p, { allow_local_mirror: !p.allow_local_mirror }, 'mirror')"
          >mirror</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded hover:bg-surface-hover"
            @click="rename(p)"
          >Rename</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
            @click="destroy(p)"
          >Delete</button>
        </template>
      </li>
      <li v-if="projects.length === 0" class="text-sm text-text-muted">
        No project is visible to you yet.
      </li>
    </ul>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, inject } from 'vue'
import {
  listProjects,
  createProject,
  updateProject,
  deleteProject,
  type Project,
} from '@/api/projects'
import { getSettings } from '@/api/settings'
import { useTreeStore } from '@/stores/tree'
import { useAuthStore } from '@/stores/auth'
import { useWindowsStore, type OpenSpec } from 'plancia'

const projects = ref<Project[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const newName = ref('')
const treeStore = useTreeStore()
const auth = useAuthStore()
const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

// Master switches: use_anchors/use_globals only take effect when the server
// master switch is on. We surface them so the toggles can flag "no effect yet".
const anchorsMaster = ref(false)
const globalsMaster = ref(false)

/** Explore a project as a graph window (its link neighbourhood). */
function openProjectGraph(name: string) {
  openWindow({
    type: 'graph',
    key: 'graph:project:' + name,
    title: `Graph · ${name}`,
    props: { project: name },
  })
}

/** Manage who can access a project (owner-only; effective under member_scope=members). */
function openProjectMembers(name: string) {
  openWindow({
    type: 'project-members',
    key: 'project-members:' + name,
    title: `Members · ${name}`,
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

async function handleCreate() {
  if (!newName.value.trim()) return
  try {
    await createProject(newName.value.trim())
    newName.value = ''
    await load()
    treeStore.invalidateAll()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Create failed'
  }
}

async function toggleHidden(p: Project) {
  await updateProject(p.name, { hidden_from_mcp: !p.hidden_from_mcp })
  await load()
}

async function toggleSkip(p: Project) {
  await updateProject(p.name, { skip_git_sync: !p.skip_git_sync })
  await load()
}

async function togglePublic(p: Project) {
  await updateProject(p.name, { public: !p.public })
  await load()
}

async function toggleGlobals(p: Project) {
  await updateProject(p.name, { use_globals: !p.use_globals })
  await load()
}

async function toggleAnchors(p: Project) {
  await updateProject(p.name, { use_anchors: !p.use_anchors })
  await load()
}

async function toggleTagVocabulary(p: Project) {
  await updateProject(p.name, { use_tag_vocabulary: !p.use_tag_vocabulary })
  await load()
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
    await load()
    treeStore.invalidateAll()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Rename failed'
  }
}

async function destroy(p: Project) {
  if (!confirm(`Delete project "${p.name}" and ${p.note_count} note(s)?`)) return
  try {
    await deleteProject(p.name)
    await load()
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
  <div class="p-8 max-w-3xl mx-auto">
    <h1 class="text-2xl font-semibold mb-1">Projects</h1>
    <p class="text-sm text-text-muted mb-6">
      Top-level vault folders. <em>private</em>/<em>public</em> controls guest visibility
      (public = readable by guest-role users); <em>skip-git</em> excludes from auto-commit;
      <em>hidden</em> keeps the project invisible to MCP agents;
      <em>globals</em> merges the shared global skills/agents at bootstrap;
      <em>anchors</em> materialises vault agents as local subagent files at bootstrap
      (both need their server master switch on — dimmed when off);
      <em>tag-vocab</em> lets the project extend the lint tag vocabulary from
      memory/conventions.md.
    </p>

    <form
      v-if="auth.canWrite"
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
    <p v-else-if="error" class="text-danger">{{ error }}</p>

    <ul v-else class="space-y-2">
      <li
        v-for="p in projects"
        :key="p.name"
        class="rounded border border-border bg-surface px-4 py-3 flex items-center gap-3"
      >
        <button
          type="button"
          class="font-medium hover:text-accent flex-1 text-left"
          title="Open project graph"
          @click="openProjectGraph(p.name)"
        >{{ p.name }}</button>
        <span class="text-xs text-text-muted">{{ p.note_count }} notes</span>

        <template v-if="auth.canWrite">
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.public ? 'bg-success/20 text-success' : 'border border-border'"
            :title="p.public ? 'Public — visible to guest users. Click to make private.' : 'Private. Click to make public (readable by guests).'"
            @click="togglePublic(p)"
          >{{ p.public ? 'public' : 'private' }}</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.skip_git_sync ? 'bg-warning/20 text-warning' : 'border border-border'"
            :title="p.skip_git_sync ? 'Click to re-enable git sync' : 'Click to skip from git sync'"
            @click="toggleSkip(p)"
          >skip-git</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.hidden_from_mcp ? 'bg-warning/20 text-warning' : 'border border-border'"
            :title="p.hidden_from_mcp ? 'Click to expose to MCP again' : 'Click to hide from MCP'"
            @click="toggleHidden(p)"
          >hidden</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="[
              p.use_globals ? 'bg-accent/20 text-accent' : 'border border-border',
              globalsMaster ? '' : 'opacity-50',
            ]"
            :title="globalsTitle(p)"
            @click="toggleGlobals(p)"
          >globals</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="[
              p.use_anchors ? 'bg-accent/20 text-accent' : 'border border-border',
              anchorsMaster ? '' : 'opacity-50',
            ]"
            :title="anchorsTitle(p)"
            @click="toggleAnchors(p)"
          >anchors</button>
          <button
            type="button"
            class="text-xs px-2 py-1 rounded"
            :class="p.use_tag_vocabulary ? 'bg-accent/20 text-accent' : 'border border-border'"
            :title="p.use_tag_vocabulary
              ? 'Tag vocabulary on: memory_lint accepts the extra tags declared in this project\'s memory/conventions.md frontmatter (tag_vocabulary). Click to disable.'
              : 'Click to let this project extend the lint tag vocabulary via memory/conventions.md frontmatter (tag_vocabulary: exact tags or ns:* wildcards).'"
            @click="toggleTagVocabulary(p)"
          >tag-vocab</button>

          <button
            v-if="auth.isOwner"
            type="button"
            class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover"
            title="Manage who can access this project"
            @click="openProjectMembers(p.name)"
          >Members</button>
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
        <span
          v-else
          class="text-xs px-2 py-0.5 rounded"
          :class="p.public ? 'bg-success/20 text-success' : 'border border-border'"
        >{{ p.public ? 'public' : 'private' }}</span>
      </li>
    </ul>
  </div>
</template>

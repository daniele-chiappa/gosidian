<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  getProject,
  listProjectMembers,
  setProjectMember,
  removeProjectMember,
  type GrantLevel,
  type Project,
  type ProjectMember,
} from '@/api/projects'
import { listUsers, type AdminUser } from '@/api/admin'
import { VISIBILITY_LABEL } from '@/api/access'
import { useAccessStore } from '@/stores/access'

const props = defineProps<{ project: string }>()

const access = useAccessStore()
const project = ref<Project | null>(null)
const members = ref<ProjectMember[]>([])
const users = ref<AdminUser[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const addUser = ref('')
const addLevel = ref<GrantLevel>('read')
const busy = ref(false)

const LEVELS: GrantLevel[] = ['read', 'write', 'admin']
const LEVEL_HELP: Record<GrantLevel, string> = {
  read: 'read — can open the project even when its visibility would not allow it',
  write: 'write — can also create, edit and delete notes',
  admin: 'admin — can also change the project settings, rename and delete it',
}

// Users eligible to add: not the owner (who sees everything), not disabled, and
// not already granted.
const candidates = computed(() => {
  const have = new Set(members.value.map((m) => m.user_id))
  return users.value.filter((u) => u.role !== 'owner' && !u.disabled_at && !have.has(u.id))
})

/** What the project's visibility already gives, so the grants read in context. */
const visibilityNote = computed(() => {
  switch (project.value?.visibility) {
    case 'public':
      return 'Every signed-in account, guests included, can already read it. Grants add write and admin.'
    case 'internal':
      return 'Every member account can already read it. Grants add write and admin.'
    case 'private':
      return 'Only the accounts listed here (and the owner) can see it.'
    default:
      return ''
  }
})

async function load() {
  loading.value = true
  error.value = null
  try {
    const [p, m, u] = await Promise.all([
      getProject(props.project),
      listProjectMembers(props.project),
      listUsers(),
    ])
    project.value = p
    members.value = m
    users.value = u
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load'
  } finally {
    loading.value = false
  }
}

/** A grant change alters what the other account sees; refresh our own cues too. */
async function reload() {
  await load()
  void access.load()
}

async function add() {
  if (!addUser.value) return
  busy.value = true
  error.value = null
  try {
    await setProjectMember(props.project, addUser.value, addLevel.value)
    addUser.value = ''
    addLevel.value = 'read'
    await reload()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Add failed'
  } finally {
    busy.value = false
  }
}

async function changeLevel(m: ProjectMember, level: string) {
  if (!LEVELS.includes(level as GrantLevel)) return
  try {
    await setProjectMember(props.project, m.user_id, level as GrantLevel)
    await reload()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Update failed'
  }
}

async function remove(m: ProjectMember) {
  try {
    await removeProjectMember(props.project, m.user_id)
    await reload()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Remove failed'
  }
}

function levelClass(level: GrantLevel): string {
  return level === 'admin'
    ? 'bg-accent/20 text-accent'
    : level === 'write'
      ? 'bg-success/20 text-success'
      : 'border border-border text-text-muted'
}

onMounted(load)
</script>

<template>
  <div class="p-6 max-w-xl mx-auto">
    <h2 class="text-lg font-semibold mb-1">Members · {{ project?.name ?? props.project }}</h2>
    <p class="text-sm text-text-muted mb-1">
      Grants give specific accounts a level on this project: <em>read</em>, <em>write</em>
      or <em>admin</em>. The account's role is the ceiling — a guest stays read-only.
      The owner always has full access.
    </p>
    <p v-if="project" class="text-sm mb-4">
      <span
        class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded border border-border text-text-muted mr-1"
      >{{ VISIBILITY_LABEL[project.visibility] }}</span>
      <span class="text-text-muted">{{ visibilityNote }}</span>
    </p>

    <p v-if="loading" class="text-text-muted">Loading…</p>
    <p v-if="error" class="text-sm text-danger mb-2">{{ error }}</p>

    <ul class="space-y-2 mb-4">
      <li
        v-for="m in members"
        :key="m.user_id"
        class="flex items-center gap-3 rounded border border-border bg-surface px-3 py-2"
      >
        <span class="flex-1 text-sm font-medium">{{ m.username }}</span>
        <span
          class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded"
          :class="levelClass(m.level)"
        >{{ m.level }}</span>
        <select
          class="text-xs rounded bg-bg-elevated border border-border px-2 py-1"
          :value="m.level"
          :title="LEVEL_HELP[m.level]"
          @change="changeLevel(m, ($event.target as HTMLSelectElement).value)"
        >
          <option v-for="l in LEVELS" :key="l" :value="l">{{ l }}</option>
        </select>
        <button
          type="button"
          class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
          @click="remove(m)"
        >Remove</button>
      </li>
      <li v-if="!loading && members.length === 0" class="text-sm text-text-muted">
        No grants yet.
      </li>
    </ul>

    <form class="flex items-end gap-2" @submit.prevent="add">
      <label class="flex-1 text-sm">
        <span class="text-text-muted text-xs">Add account</span>
        <select
          v-model="addUser"
          class="mt-1 w-full rounded bg-bg-elevated border border-border px-2 py-2"
        >
          <option value="">Select an account…</option>
          <option v-for="u in candidates" :key="u.id" :value="u.id">
            {{ u.username }} ({{ u.role }})
          </option>
        </select>
      </label>
      <label class="text-sm">
        <span class="text-text-muted text-xs">Level</span>
        <select
          v-model="addLevel"
          class="mt-1 rounded bg-bg-elevated border border-border px-2 py-2"
          :title="LEVEL_HELP[addLevel]"
        >
          <option v-for="l in LEVELS" :key="l" :value="l">{{ l }}</option>
        </select>
      </label>
      <button
        type="submit"
        :disabled="busy || !addUser"
        class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
      >+ Add</button>
    </form>
  </div>
</template>

<script setup lang="ts">
/**
 * Project access window: who holds a grant on the project, directly or
 * through a team, and — for the owner or a project admin — the controls to
 * change it. Readers see the list without candidates (IMP-101 phase 2).
 */
import { computed, onMounted, ref } from 'vue'
import { setProjectMember, removeProjectMember, type GrantLevel, type ProjectMember } from '@/api/projects'
import {
  getProjectAccess,
  setProjectTeam,
  removeProjectTeam,
  type ProjectAccess,
  type ProjectTeamGrant,
} from '@/api/teams'
import { VISIBILITY_LABEL } from '@/api/access'
import { useAccessStore } from '@/stores/access'

const props = defineProps<{ project: string }>()

const access = useAccessStore()
const view = ref<ProjectAccess | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const busy = ref(false)
const addUser = ref('')
const addUserLevel = ref<GrantLevel>('read')
const addTeam = ref('')
const addTeamLevel = ref<GrantLevel>('read')

const LEVELS: GrantLevel[] = ['read', 'write', 'admin']
const LEVEL_HELP: Record<GrantLevel, string> = {
  read: 'read — can open the project even when its visibility would not allow it',
  write: 'write — can also create, edit and delete notes',
  admin: 'admin — can also change the project settings, rename and delete it, and manage who can use it',
}

const canAdmin = computed(() => view.value?.can_admin ?? false)

/** What the project's visibility already gives, so the grants read in context. */
const visibilityNote = computed(() => {
  switch (view.value?.visibility) {
    case 'public':
      return 'Every signed-in account, guests included, can already read it. Grants add write and admin.'
    case 'internal':
      return 'Every member account can already read it. Grants add write and admin.'
    case 'private':
      return 'Only the accounts and teams listed here (and the owner) can see it.'
    default:
      return ''
  }
})

async function load() {
  loading.value = true
  error.value = null
  try {
    view.value = await getProjectAccess(props.project)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load'
  } finally {
    loading.value = false
  }
}

/** A grant change alters what other accounts see; refresh our own cues too. */
async function reload() {
  await load()
  void access.load()
}

async function run(label: string, fn: () => Promise<unknown>) {
  busy.value = true
  error.value = null
  try {
    await fn()
    await reload()
  } catch (e) {
    error.value = e instanceof Error ? `${label}: ${e.message}` : `${label} failed`
  } finally {
    busy.value = false
  }
}

async function grantUser() {
  if (!addUser.value) return
  await run('Add account', async () => {
    await setProjectMember(props.project, addUser.value, addUserLevel.value)
    addUser.value = ''
    addUserLevel.value = 'read'
  })
}

async function changeUser(m: ProjectMember, level: string) {
  if (!LEVELS.includes(level as GrantLevel)) return
  await run('Change level', () => setProjectMember(props.project, m.user_id, level as GrantLevel))
}

async function dropUser(m: ProjectMember) {
  await run('Remove account', () => removeProjectMember(props.project, m.user_id))
}

async function grantTeam() {
  if (!addTeam.value) return
  await run('Add team', async () => {
    await setProjectTeam(props.project, addTeam.value, addTeamLevel.value)
    addTeam.value = ''
    addTeamLevel.value = 'read'
  })
}

async function changeTeam(g: ProjectTeamGrant, level: string) {
  if (!LEVELS.includes(level as GrantLevel)) return
  await run('Change level', () => setProjectTeam(props.project, g.team_id, level as GrantLevel))
}

async function dropTeam(g: ProjectTeamGrant) {
  await run('Remove team', () => removeProjectTeam(props.project, g.team_id))
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
    <h2 class="text-lg font-semibold mb-1">Access · {{ props.project }}</h2>
    <p class="text-sm text-text-muted mb-1">
      Grants give accounts and teams a level on this project: <em>read</em>, <em>write</em>
      or <em>admin</em>. The account's role is the ceiling — a guest stays read-only.
      The owner always has full access.
    </p>
    <p v-if="view" class="text-sm mb-4">
      <span
        class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded border border-border text-text-muted mr-1"
      >{{ VISIBILITY_LABEL[view.visibility] }}</span>
      <span class="text-text-muted">{{ visibilityNote }}</span>
    </p>
    <p v-if="view && !canAdmin" class="text-xs text-text-muted mb-4">
      You can see who has access; changing it takes the admin level on this project.
    </p>

    <p v-if="loading" class="text-text-muted">Loading…</p>
    <p v-if="error" class="text-sm text-danger mb-2">{{ error }}</p>

    <template v-if="view">
      <!-- Accounts -->
      <h3 class="text-xs uppercase tracking-wide text-text-muted mb-2">Accounts ({{ view.users.length }})</h3>
      <ul class="space-y-2 mb-3">
        <li
          v-for="m in view.users"
          :key="m.user_id"
          class="flex items-center gap-3 rounded border border-border bg-surface px-3 py-2"
        >
          <span class="flex-1 text-sm font-medium">{{ m.username }}</span>
          <span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded" :class="levelClass(m.level)">{{ m.level }}</span>
          <template v-if="canAdmin">
            <select
              class="text-xs rounded bg-bg-elevated border border-border px-2 py-1"
              :value="m.level"
              :title="LEVEL_HELP[m.level]"
              @change="changeUser(m, ($event.target as HTMLSelectElement).value)"
            >
              <option v-for="l in LEVELS" :key="l" :value="l">{{ l }}</option>
            </select>
            <button
              type="button"
              class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
              @click="dropUser(m)"
            >Remove</button>
          </template>
        </li>
        <li v-if="view.users.length === 0" class="text-sm text-text-muted">No account grants.</li>
      </ul>
      <form v-if="canAdmin && view.candidates" class="flex items-end gap-2 mb-6" @submit.prevent="grantUser">
        <label class="flex-1 text-sm">
          <span class="text-text-muted text-xs">Add account</span>
          <select v-model="addUser" class="mt-1 w-full rounded bg-bg-elevated border border-border px-2 py-2">
            <option value="">Select an account…</option>
            <option v-for="u in view.candidates.users" :key="u.id" :value="u.id">{{ u.username }} ({{ u.role }})</option>
          </select>
        </label>
        <label class="text-sm">
          <span class="text-text-muted text-xs">Level</span>
          <select v-model="addUserLevel" class="mt-1 rounded bg-bg-elevated border border-border px-2 py-2" :title="LEVEL_HELP[addUserLevel]">
            <option v-for="l in LEVELS" :key="l" :value="l">{{ l }}</option>
          </select>
        </label>
        <button
          type="submit"
          :disabled="busy || !addUser"
          class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
        >+ Add</button>
      </form>

      <!-- Teams -->
      <h3 class="text-xs uppercase tracking-wide text-text-muted mb-2">Teams ({{ view.teams.length }})</h3>
      <ul class="space-y-2 mb-3">
        <li
          v-for="g in view.teams"
          :key="g.team_id"
          class="flex items-center gap-3 rounded border border-border bg-surface px-3 py-2"
        >
          <span class="flex-1 text-sm font-medium">{{ g.name }}</span>
          <span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded" :class="levelClass(g.level)">{{ g.level }}</span>
          <template v-if="canAdmin">
            <select
              class="text-xs rounded bg-bg-elevated border border-border px-2 py-1"
              :value="g.level"
              :title="LEVEL_HELP[g.level]"
              @change="changeTeam(g, ($event.target as HTMLSelectElement).value)"
            >
              <option v-for="l in LEVELS" :key="l" :value="l">{{ l }}</option>
            </select>
            <button
              type="button"
              class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
              @click="dropTeam(g)"
            >Remove</button>
          </template>
        </li>
        <li v-if="view.teams.length === 0" class="text-sm text-text-muted">No team grants.</li>
      </ul>
      <form v-if="canAdmin && view.candidates" class="flex items-end gap-2" @submit.prevent="grantTeam">
        <label class="flex-1 text-sm">
          <span class="text-text-muted text-xs">Add team</span>
          <select v-model="addTeam" class="mt-1 w-full rounded bg-bg-elevated border border-border px-2 py-2">
            <option value="">Select a team…</option>
            <option v-for="t in view.candidates.teams" :key="t.id" :value="t.id">{{ t.name }}</option>
          </select>
        </label>
        <label class="text-sm">
          <span class="text-text-muted text-xs">Level</span>
          <select v-model="addTeamLevel" class="mt-1 rounded bg-bg-elevated border border-border px-2 py-2" :title="LEVEL_HELP[addTeamLevel]">
            <option v-for="l in LEVELS" :key="l" :value="l">{{ l }}</option>
          </select>
        </label>
        <button
          type="submit"
          :disabled="busy || !addTeam"
          class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
        >+ Add</button>
      </form>
      <p v-if="canAdmin && view.candidates && view.candidates.teams.length === 0 && view.teams.length === 0" class="text-xs text-text-muted mt-2">
        No team exists yet — the owner creates them in Admin → Teams.
      </p>
    </template>
  </div>
</template>

<script setup lang="ts">
/**
 * Admin → Teams (IMP-101 phase 2). A team groups accounts so one grant per
 * project covers all of them; the account's effective level is the highest
 * of its direct grant and its teams' grants, capped by the role. Owner-only.
 */
import { computed, onMounted, reactive, ref } from 'vue'
import {
  listTeams,
  createTeam,
  updateTeam,
  deleteTeam,
  addTeamUser,
  removeTeamUser,
  setTeamGrant,
  removeTeamGrant,
  type Team,
} from '@/api/teams'
import { listUsers, type AdminUser } from '@/api/admin'
import { listProjects, type GrantLevel, type Project } from '@/api/projects'
import { useAccessStore } from '@/stores/access'
import { roleLabel } from '@/api/access'

const access = useAccessStore()
const teams = ref<Team[]>([])
const users = ref<AdminUser[]>([])
const projects = ref<Project[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

const LEVELS: GrantLevel[] = ['read', 'write', 'admin']

// --- Create ---
const newTeam = reactive({ name: '', description: '' })
const creating = ref(false)

// --- Per-team drafts (add user / add grant / rename) ---
const addUserDraft = reactive<Record<string, string>>({})
const addGrantDraft = reactive<Record<string, { project: string; level: GrantLevel }>>({})
const editing = ref<string | null>(null)
const editDraft = reactive({ name: '', description: '' })

function candidatesFor(t: Team): AdminUser[] {
  const have = new Set(t.users.map((u) => u.id))
  return users.value.filter((u) => u.role !== 'owner' && !u.disabled_at && !have.has(u.id))
}

function projectsFor(t: Team): Project[] {
  const have = new Set(t.grants.map((g) => g.project))
  return projects.value.filter((p) => !have.has(p.name))
}

const empty = computed(() => !loading.value && teams.value.length === 0)

async function load() {
  loading.value = true
  error.value = null
  try {
    const [t, u, p] = await Promise.all([listTeams(), listUsers(), listProjects()])
    teams.value = t
    users.value = u
    projects.value = p
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load'
  } finally {
    loading.value = false
  }
}

/** Grants changed: reload the list and our own per-project levels. */
async function refresh() {
  await load()
  void access.load()
}

async function run(label: string, fn: () => Promise<unknown>) {
  error.value = null
  try {
    await fn()
    await refresh()
  } catch (e) {
    error.value = e instanceof Error ? `${label}: ${e.message}` : `${label} failed`
  }
}

async function submitCreate() {
  if (!newTeam.name.trim()) return
  creating.value = true
  await run('Create team', async () => {
    await createTeam(newTeam.name.trim(), newTeam.description.trim())
    newTeam.name = ''
    newTeam.description = ''
  })
  creating.value = false
}

function startEdit(t: Team) {
  editing.value = t.id
  editDraft.name = t.name
  editDraft.description = t.description ?? ''
}

async function saveEdit(t: Team) {
  await run('Rename team', () =>
    updateTeam(t.id, { name: editDraft.name.trim(), description: editDraft.description.trim() }),
  )
  editing.value = null
}

async function destroy(t: Team) {
  if (!confirm(`Delete team "${t.name}"? Its ${t.grants.length} grant(s) go with it; the accounts stay.`)) return
  await run('Delete team', () => deleteTeam(t.id))
}

async function addUser(t: Team) {
  const id = addUserDraft[t.id]
  if (!id) return
  await run('Add account', async () => {
    await addTeamUser(t.id, id)
    addUserDraft[t.id] = ''
  })
}

async function dropUser(t: Team, userId: string) {
  await run('Remove account', () => removeTeamUser(t.id, userId))
}

async function addGrant(t: Team) {
  const d = addGrantDraft[t.id]
  if (!d?.project) return
  await run('Add grant', async () => {
    await setTeamGrant(t.id, d.project, d.level)
    d.project = ''
    d.level = 'read'
  })
}

async function changeGrant(t: Team, project: string, level: string) {
  if (!LEVELS.includes(level as GrantLevel)) return
  await run('Change grant', () => setTeamGrant(t.id, project, level as GrantLevel))
}

async function dropGrant(t: Team, project: string) {
  await run('Remove grant', () => removeTeamGrant(t.id, project))
}

function grantDraft(t: Team): { project: string; level: GrantLevel } {
  let d = addGrantDraft[t.id]
  if (!d) {
    d = { project: '', level: 'read' }
    addGrantDraft[t.id] = d
  }
  return d
}

function levelClass(level: string): string {
  return level === 'admin'
    ? 'bg-accent/20 text-accent'
    : level === 'write'
      ? 'bg-success/20 text-success'
      : 'border border-border text-text-muted'
}

onMounted(load)
</script>

<template>
  <div class="space-y-4">
    <p class="text-sm text-text-muted">
      A team gives every account in it the same level on the projects it is granted.
      An account's effective level is the highest of its direct grant and its teams'
      grants, capped by its role. Project admins can also grant a team from
      Projects → Access.
    </p>

    <!-- Create -->
    <section class="rounded-lg border border-border p-4">
      <form class="flex flex-wrap items-end gap-2" @submit.prevent="submitCreate">
        <label class="text-sm flex-1 min-w-[10rem]">
          <span class="text-text-muted text-xs">New team</span>
          <input
            v-model.trim="newTeam.name"
            type="text"
            placeholder="name"
            required
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
          />
        </label>
        <label class="text-sm flex-[2] min-w-[12rem]">
          <span class="text-text-muted text-xs">Description (optional)</span>
          <input
            v-model.trim="newTeam.description"
            type="text"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
          />
        </label>
        <button
          type="submit"
          :disabled="creating || !newTeam.name"
          class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
        >+ Create team</button>
      </form>
    </section>

    <p v-if="loading" class="text-text-muted">Loading…</p>
    <p v-if="error" class="text-danger text-sm">{{ error }}</p>
    <p v-if="empty" class="text-sm text-text-muted">No teams yet.</p>

    <section
      v-for="t in teams"
      :key="t.id"
      class="rounded-lg border border-border p-4 space-y-3"
    >
      <!-- Header: name / description / actions -->
      <div class="flex items-start gap-3">
        <div class="flex-1 min-w-0">
          <template v-if="editing === t.id">
            <form class="flex flex-wrap gap-2" @submit.prevent="saveEdit(t)">
              <input
                v-model.trim="editDraft.name"
                type="text"
                required
                class="rounded bg-bg-elevated border border-border px-2 py-1 text-sm"
              />
              <input
                v-model.trim="editDraft.description"
                type="text"
                placeholder="description"
                class="flex-1 rounded bg-bg-elevated border border-border px-2 py-1 text-sm"
              />
              <button type="submit" class="text-xs px-2 py-1 rounded bg-accent text-accent-fg">Save</button>
              <button type="button" class="text-xs px-2 py-1 rounded border border-border" @click="editing = null">Cancel</button>
            </form>
          </template>
          <template v-else>
            <h3 class="font-semibold">{{ t.name }}</h3>
            <p v-if="t.description" class="text-xs text-text-muted">{{ t.description }}</p>
          </template>
        </div>
        <button
          v-if="editing !== t.id"
          type="button"
          class="text-xs px-2 py-1 rounded hover:bg-surface-hover"
          @click="startEdit(t)"
        >Rename</button>
        <button
          type="button"
          class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
          @click="destroy(t)"
        >Delete</button>
      </div>

      <div class="grid gap-4 md:grid-cols-2">
        <!-- Accounts -->
        <div>
          <h4 class="text-xs uppercase tracking-wide text-text-muted mb-2">Accounts ({{ t.users.length }})</h4>
          <ul class="space-y-1 mb-2">
            <li
              v-for="u in t.users"
              :key="u.id"
              class="flex items-center gap-2 text-sm rounded border border-border bg-surface px-2 py-1"
            >
              <span class="flex-1 truncate">{{ u.username }}</span>
              <span class="text-[10px] uppercase text-text-muted">{{ roleLabel(u.role) }}</span>
              <button
                type="button"
                class="text-xs px-1.5 rounded text-danger hover:bg-surface-hover"
                title="Remove from team"
                @click="dropUser(t, u.id)"
              >×</button>
            </li>
            <li v-if="t.users.length === 0" class="text-xs text-text-muted">No accounts yet.</li>
          </ul>
          <form class="flex gap-2" @submit.prevent="addUser(t)">
            <select
              v-model="addUserDraft[t.id]"
              class="flex-1 rounded bg-bg-elevated border border-border px-2 py-1 text-sm"
            >
              <option value="">Add account…</option>
              <option v-for="u in candidatesFor(t)" :key="u.id" :value="u.id">{{ u.username }} ({{ roleLabel(u.role) }})</option>
            </select>
            <button
              type="submit"
              :disabled="!addUserDraft[t.id]"
              class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover disabled:opacity-50"
            >Add</button>
          </form>
        </div>

        <!-- Grants -->
        <div>
          <h4 class="text-xs uppercase tracking-wide text-text-muted mb-2">Project grants ({{ t.grants.length }})</h4>
          <ul class="space-y-1 mb-2">
            <li
              v-for="g in t.grants"
              :key="g.project"
              class="flex items-center gap-2 text-sm rounded border border-border bg-surface px-2 py-1"
            >
              <span class="flex-1 truncate font-medium">{{ g.project }}</span>
              <span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded" :class="levelClass(g.level)">{{ g.level }}</span>
              <select
                class="text-xs rounded bg-bg-elevated border border-border px-1 py-0.5"
                :value="g.level"
                @change="changeGrant(t, g.project, ($event.target as HTMLSelectElement).value)"
              >
                <option v-for="l in LEVELS" :key="l" :value="l">{{ l }}</option>
              </select>
              <button
                type="button"
                class="text-xs px-1.5 rounded text-danger hover:bg-surface-hover"
                title="Remove grant"
                @click="dropGrant(t, g.project)"
              >×</button>
            </li>
            <li v-if="t.grants.length === 0" class="text-xs text-text-muted">No grants yet.</li>
          </ul>
          <form class="flex gap-2" @submit.prevent="addGrant(t)">
            <select
              v-model="grantDraft(t).project"
              class="flex-1 rounded bg-bg-elevated border border-border px-2 py-1 text-sm"
            >
              <option value="">Grant a project…</option>
              <option v-for="p in projectsFor(t)" :key="p.name" :value="p.name">{{ p.name }}</option>
            </select>
            <select
              v-model="grantDraft(t).level"
              class="rounded bg-bg-elevated border border-border px-2 py-1 text-sm"
            >
              <option v-for="l in LEVELS" :key="l" :value="l">{{ l }}</option>
            </select>
            <button
              type="submit"
              :disabled="!grantDraft(t).project"
              class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover disabled:opacity-50"
            >Add</button>
          </form>
        </div>
      </div>
    </section>
  </div>
</template>

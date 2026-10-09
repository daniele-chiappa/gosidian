<script setup lang="ts">
/**
 * Admin → Teams (IMP-101 phase 2). A team groups accounts so one grant per
 * project covers all of them; the account's effective level is the highest
 * of its direct grant and its teams' grants, capped by the role. Owner-only.
 */
import { useI18n } from 'vue-i18n'
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
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

const { t } = useI18n()

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

function candidatesFor(team: Team): AdminUser[] {
  const have = new Set(team.users.map((u) => u.id))
  return users.value.filter((u) => u.role !== 'owner' && !u.disabled_at && !have.has(u.id))
}

function projectsFor(team: Team): Project[] {
  const have = new Set(team.grants.map((g) => g.project))
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
    error.value = errorText(e, t, t('admin.teams.load_failed'))
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
    error.value = errorText(e, t, label)
  }
}

async function submitCreate() {
  if (!newTeam.name.trim()) return
  creating.value = true
  await run(t('admin.teams.create_failed'), async () => {
    await createTeam(newTeam.name.trim(), newTeam.description.trim())
    newTeam.name = ''
    newTeam.description = ''
  })
  creating.value = false
}

function startEdit(team: Team) {
  editing.value = team.id
  editDraft.name = team.name
  editDraft.description = team.description ?? ''
}

async function saveEdit(team: Team) {
  await run(t('admin.teams.rename_failed'), () =>
    updateTeam(team.id, { name: editDraft.name.trim(), description: editDraft.description.trim() }),
  )
  editing.value = null
}

async function destroy(team: Team) {
  if (!confirm(t('admin.teams.confirm_delete', { name: team.name, n: team.grants.length }))) return
  await run(t('admin.teams.delete_failed'), () => deleteTeam(team.id))
}

async function addUser(team: Team) {
  const id = addUserDraft[team.id]
  if (!id) return
  await run(t('members.add_account_failed'), async () => {
    await addTeamUser(team.id, id)
    addUserDraft[team.id] = ''
  })
}

async function dropUser(team: Team, userId: string) {
  await run(t('admin.teams.remove_account_failed'), () => removeTeamUser(team.id, userId))
}

async function addGrant(team: Team) {
  const d = addGrantDraft[team.id]
  if (!d?.project) return
  await run(t('admin.teams.add_grant_failed'), async () => {
    await setTeamGrant(team.id, d.project, d.level)
    d.project = ''
    d.level = 'read'
  })
}

async function changeGrant(team: Team, project: string, level: string) {
  if (!LEVELS.includes(level as GrantLevel)) return
  await run(t('members.change_failed'), () => setTeamGrant(team.id, project, level as GrantLevel))
}

async function dropGrant(team: Team, project: string) {
  await run(t('members.remove_failed'), () => removeTeamGrant(team.id, project))
}

function grantDraft(team: Team): { project: string; level: GrantLevel } {
  let d = addGrantDraft[team.id]
  if (!d) {
    d = { project: '', level: 'read' }
    addGrantDraft[team.id] = d
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
      {{ t('admin.teams.intro') }}
    </p>

    <!-- Create -->
    <section class="rounded-lg border border-border p-4">
      <form class="flex flex-wrap items-end gap-2" @submit.prevent="submitCreate">
        <label class="text-sm flex-1 min-w-[10rem]">
          <span class="text-text-muted text-xs">{{ t('admin.teams.new') }}</span>
          <input
            v-model.trim="newTeam.name"
            type="text"
            :placeholder="t('admin.teams.name')"
            required
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
          />
        </label>
        <label class="text-sm flex-[2] min-w-[12rem]">
          <span class="text-text-muted text-xs">{{ t('admin.teams.description_optional') }}</span>
          <input
            v-model.trim="newTeam.description"
            type="text"
            class="mt-1 w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-focus"
          />
        </label>
        <button
          type="submit"
          :disabled="creating || !newTeam.name"
          class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
        >{{ t('admin.teams.create') }}</button>
      </form>
    </section>

    <p v-if="loading" class="text-text-muted">{{ t('common.loading') }}</p>
    <ErrorMessage v-if="error" :text="error" class="text-sm" />
    <p v-if="empty" class="text-sm text-text-muted">{{ t('admin.teams.empty') }}</p>

    <section
      v-for="team in teams"
      :key="team.id"
      class="rounded-lg border border-border p-4 space-y-3"
    >
      <!-- Header: name / description / actions -->
      <div class="flex items-start gap-3">
        <div class="flex-1 min-w-0">
          <template v-if="editing === team.id">
            <form class="flex flex-wrap gap-2" @submit.prevent="saveEdit(team)">
              <input
                v-model.trim="editDraft.name"
                type="text"
                required
                class="rounded bg-bg-elevated border border-border px-2 py-1 text-sm"
              />
              <input
                v-model.trim="editDraft.description"
                type="text"
                :placeholder="t('admin.teams.description')"
                class="flex-1 rounded bg-bg-elevated border border-border px-2 py-1 text-sm"
              />
              <button type="submit" class="text-xs px-2 py-1 rounded bg-accent text-accent-fg">{{ t('common.save') }}</button>
              <button type="button" class="text-xs px-2 py-1 rounded border border-border" @click="editing = null">{{ t('common.cancel') }}</button>
            </form>
          </template>
          <template v-else>
            <h3 class="font-semibold">{{ team.name }}</h3>
            <p v-if="team.description" class="text-xs text-text-muted">{{ team.description }}</p>
          </template>
        </div>
        <button
          v-if="editing !== team.id"
          type="button"
          class="text-xs px-2 py-1 rounded hover:bg-surface-hover"
          @click="startEdit(team)"
        >{{ t('projects.rename') }}</button>
        <button
          type="button"
          class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
          @click="destroy(team)"
        >{{ t('common.delete') }}</button>
      </div>

      <div class="grid gap-4 md:grid-cols-2">
        <!-- Accounts -->
        <div>
          <h4 class="text-xs uppercase tracking-wide text-text-muted mb-2">{{ t('members.accounts', { n: team.users.length }) }}</h4>
          <ul class="space-y-1 mb-2">
            <li
              v-for="u in team.users"
              :key="u.id"
              class="flex items-center gap-2 text-sm rounded border border-border bg-surface px-2 py-1"
            >
              <span class="flex-1 truncate">{{ u.username }}</span>
              <span class="text-[10px] uppercase text-text-muted">{{ roleLabel(u.role) }}</span>
              <button
                type="button"
                class="text-xs px-1.5 rounded text-danger hover:bg-surface-hover"
                :title="t('admin.teams.remove_account')"
                @click="dropUser(team, u.id)"
              >×</button>
            </li>
            <li v-if="team.users.length === 0" class="text-xs text-text-muted">{{ t('admin.teams.no_accounts') }}</li>
          </ul>
          <form class="flex gap-2" @submit.prevent="addUser(team)">
            <select
              v-model="addUserDraft[team.id]"
              class="flex-1 rounded bg-bg-elevated border border-border px-2 py-1 text-sm"
            >
              <option value="">{{ t('admin.teams.add_account') }}</option>
              <option v-for="u in candidatesFor(team)" :key="u.id" :value="u.id">{{ u.username }} ({{ roleLabel(u.role) }})</option>
            </select>
            <button
              type="submit"
              :disabled="!addUserDraft[team.id]"
              class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover disabled:opacity-50"
            >{{ t('admin.teams.add') }}</button>
          </form>
        </div>

        <!-- Grants -->
        <div>
          <h4 class="text-xs uppercase tracking-wide text-text-muted mb-2">{{ t('admin.teams.grants', { n: team.grants.length }) }}</h4>
          <ul class="space-y-1 mb-2">
            <li
              v-for="g in team.grants"
              :key="g.project"
              class="flex items-center gap-2 text-sm rounded border border-border bg-surface px-2 py-1"
            >
              <span class="flex-1 truncate font-medium">{{ g.project }}</span>
              <span class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded" :class="levelClass(g.level)">{{ g.level }}</span>
              <select
                class="text-xs rounded bg-bg-elevated border border-border px-1 py-0.5"
                :value="g.level"
                @change="changeGrant(team, g.project, ($event.target as HTMLSelectElement).value)"
              >
                <option v-for="l in LEVELS" :key="l" :value="l">{{ t(`members.level_name.${l}`) }}</option>
              </select>
              <button
                type="button"
                class="text-xs px-1.5 rounded text-danger hover:bg-surface-hover"
                :title="t('admin.teams.remove_grant')"
                @click="dropGrant(team, g.project)"
              >×</button>
            </li>
            <li v-if="team.grants.length === 0" class="text-xs text-text-muted">{{ t('admin.teams.no_grants') }}</li>
          </ul>
          <form class="flex gap-2" @submit.prevent="addGrant(team)">
            <select
              v-model="grantDraft(team).project"
              class="flex-1 rounded bg-bg-elevated border border-border px-2 py-1 text-sm"
            >
              <option value="">{{ t('admin.teams.grant_project') }}</option>
              <option v-for="p in projectsFor(team)" :key="p.name" :value="p.name">{{ p.name }}</option>
            </select>
            <select
              v-model="grantDraft(team).level"
              class="rounded bg-bg-elevated border border-border px-2 py-1 text-sm"
            >
              <option v-for="l in LEVELS" :key="l" :value="l">{{ t(`members.level_name.${l}`) }}</option>
            </select>
            <button
              type="submit"
              :disabled="!grantDraft(team).project"
              class="text-xs px-2 py-1 rounded border border-border hover:bg-surface-hover disabled:opacity-50"
            >{{ t('admin.teams.add') }}</button>
          </form>
        </div>
      </div>
    </section>
  </div>
</template>

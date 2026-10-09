<script setup lang="ts">
/**
 * Project access window: who holds a grant on the project, directly or
 * through a team, and — for the owner or a project admin — the controls to
 * change it. Readers see the list without candidates (IMP-101 phase 2).
 */
import { useI18n } from 'vue-i18n'
import { computed, onMounted, ref } from 'vue'
import { setProjectMember, removeProjectMember, type GrantLevel, type ProjectMember } from '@/api/projects'
import {
  getProjectAccess,
  setProjectTeam,
  removeProjectTeam,
  type ProjectAccess,
  type ProjectTeamGrant,
} from '@/api/teams'
import { visibilityLabel, roleLabel } from '@/api/access'
import { useAccessStore } from '@/stores/access'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

const { t } = useI18n()

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
const levelHelp = (l: GrantLevel) => t(`members.level_help.${l}`)

const canAdmin = computed(() => view.value?.can_admin ?? false)

/** What the project's visibility already gives, so the grants read in context. */
const visibilityNote = computed(() => {
  switch (view.value?.visibility) {
    case 'public':
    case 'internal':
    case 'private':
      return t(`members.visibility_note.${view.value.visibility}`)
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
    error.value = errorText(e, t, t('members.load_failed'))
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
    error.value = errorText(e, t, label)
  } finally {
    busy.value = false
  }
}

async function grantUser() {
  if (!addUser.value) return
  await run(t('members.add_account_failed'), async () => {
    await setProjectMember(props.project, addUser.value, addUserLevel.value)
    addUser.value = ''
    addUserLevel.value = 'read'
  })
}

async function changeUser(m: ProjectMember, level: string) {
  if (!LEVELS.includes(level as GrantLevel)) return
  await run(t('members.change_failed'), () => setProjectMember(props.project, m.user_id, level as GrantLevel))
}

async function dropUser(m: ProjectMember) {
  await run(t('members.remove_failed'), () => removeProjectMember(props.project, m.user_id))
}

async function grantTeam() {
  if (!addTeam.value) return
  await run(t('members.add_team_failed'), async () => {
    await setProjectTeam(props.project, addTeam.value, addTeamLevel.value)
    addTeam.value = ''
    addTeamLevel.value = 'read'
  })
}

async function changeTeam(g: ProjectTeamGrant, level: string) {
  if (!LEVELS.includes(level as GrantLevel)) return
  await run(t('members.change_failed'), () => setProjectTeam(props.project, g.team_id, level as GrantLevel))
}

async function dropTeam(g: ProjectTeamGrant) {
  await run(t('members.remove_failed'), () => removeProjectTeam(props.project, g.team_id))
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
    <h2 class="text-lg font-semibold mb-1">{{ t('members.title', { project: props.project }) }}</h2>
    <p class="text-sm text-text-muted mb-1">
      {{ t('members.intro') }}
    </p>
    <p v-if="view" class="text-sm mb-4">
      <span
        class="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded border border-border text-text-muted mr-1"
      >{{ visibilityLabel(view.visibility) }}</span>
      <span class="text-text-muted">{{ visibilityNote }}</span>
    </p>
    <p v-if="view && !canAdmin" class="text-xs text-text-muted mb-4">
      {{ t('members.read_only') }}
    </p>

    <p v-if="loading" class="text-text-muted">{{ t('common.loading') }}</p>
    <ErrorMessage v-if="error" :text="error" class="text-sm mb-2" />

    <template v-if="view">
      <!-- Accounts -->
      <h3 class="text-xs uppercase tracking-wide text-text-muted mb-2">{{ t('members.accounts', { n: view.users.length }) }}</h3>
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
              :title="levelHelp(m.level)"
              @change="changeUser(m, ($event.target as HTMLSelectElement).value)"
            >
              <option v-for="l in LEVELS" :key="l" :value="l">{{ t(`members.level_name.${l}`) }}</option>
            </select>
            <button
              type="button"
              class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
              @click="dropUser(m)"
            >{{ t('members.remove') }}</button>
          </template>
        </li>
        <li v-if="view.users.length === 0" class="text-sm text-text-muted">{{ t('members.no_accounts') }}</li>
      </ul>
      <form v-if="canAdmin && view.candidates" class="flex items-end gap-2 mb-6" @submit.prevent="grantUser">
        <label class="flex-1 text-sm">
          <span class="text-text-muted text-xs">{{ t('members.add_account') }}</span>
          <select v-model="addUser" class="mt-1 w-full rounded bg-bg-elevated border border-border px-2 py-2">
            <option value="">{{ t('members.select_account') }}</option>
            <option v-for="u in view.candidates.users" :key="u.id" :value="u.id">{{ u.username }} ({{ roleLabel(u.role) }})</option>
          </select>
        </label>
        <label class="text-sm">
          <span class="text-text-muted text-xs">{{ t('members.level') }}</span>
          <select v-model="addUserLevel" class="mt-1 rounded bg-bg-elevated border border-border px-2 py-2" :title="levelHelp(addUserLevel)">
            <option v-for="l in LEVELS" :key="l" :value="l">{{ t(`members.level_name.${l}`) }}</option>
          </select>
        </label>
        <button
          type="submit"
          :disabled="busy || !addUser"
          class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
        >{{ t('members.add') }}</button>
      </form>

      <!-- Teams -->
      <h3 class="text-xs uppercase tracking-wide text-text-muted mb-2">{{ t('members.teams', { n: view.teams.length }) }}</h3>
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
              :title="levelHelp(g.level)"
              @change="changeTeam(g, ($event.target as HTMLSelectElement).value)"
            >
              <option v-for="l in LEVELS" :key="l" :value="l">{{ t(`members.level_name.${l}`) }}</option>
            </select>
            <button
              type="button"
              class="text-xs px-2 py-1 rounded text-danger hover:bg-surface-hover"
              @click="dropTeam(g)"
            >{{ t('members.remove') }}</button>
          </template>
        </li>
        <li v-if="view.teams.length === 0" class="text-sm text-text-muted">{{ t('members.no_teams') }}</li>
      </ul>
      <form v-if="canAdmin && view.candidates" class="flex items-end gap-2" @submit.prevent="grantTeam">
        <label class="flex-1 text-sm">
          <span class="text-text-muted text-xs">{{ t('members.add_team') }}</span>
          <select v-model="addTeam" class="mt-1 w-full rounded bg-bg-elevated border border-border px-2 py-2">
            <option value="">{{ t('members.select_team') }}</option>
            <option v-for="t in view.candidates.teams" :key="t.id" :value="t.id">{{ t.name }}</option>
          </select>
        </label>
        <label class="text-sm">
          <span class="text-text-muted text-xs">{{ t('members.level') }}</span>
          <select v-model="addTeamLevel" class="mt-1 rounded bg-bg-elevated border border-border px-2 py-2" :title="levelHelp(addTeamLevel)">
            <option v-for="l in LEVELS" :key="l" :value="l">{{ t(`members.level_name.${l}`) }}</option>
          </select>
        </label>
        <button
          type="submit"
          :disabled="busy || !addTeam"
          class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
        >{{ t('members.add') }}</button>
      </form>
      <p v-if="canAdmin && view.candidates && view.candidates.teams.length === 0 && view.teams.length === 0" class="text-xs text-text-muted mt-2">
        {{ t('members.no_team_yet') }}
      </p>
    </template>
  </div>
</template>

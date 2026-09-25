/**
 * Pinia access store: the signed-in account's effective level on every
 * project it may read (GET /me/access), so components gate per-project
 * controls (edit, new note, project settings) and draw the visibility cues
 * without guessing from the role. Reloaded on the `sidebar` and `tree` SSE
 * topics, which fire whenever grants, visibility or the project set change.
 *
 * Not persisted: it is cheap to fetch and must never outlive a session.
 */
import { defineStore } from 'pinia'
import { getMyAccess, LEVEL_RANK, type AccessLevel, type AccessProject, type Visibility } from '@/api/access'
import { useAuthStore } from '@/stores/auth'

interface AccessState {
  projects: Record<string, AccessProject>
  restricted: boolean
  canCreateProjects: boolean
  personalProject: string
  loaded: boolean
  loading: boolean
}

/** Top-level folder of a vault path ("proj/a/b.md" → "proj"). */
function projectOf(path: string): string {
  const i = path.indexOf('/')
  return i >= 0 ? path.slice(0, i) : path
}

export const useAccessStore = defineStore('access', {
  state: (): AccessState => ({
    projects: {},
    restricted: false,
    canCreateProjects: false,
    personalProject: '',
    loaded: false,
    loading: false,
  }),

  getters: {
    list: (s): AccessProject[] =>
      Object.values(s.projects).sort((a, b) => a.name.localeCompare(b.name)),
    readableCount: (s): number => Object.keys(s.projects).length,
    writableCount: (s): number =>
      Object.values(s.projects).filter((p) => LEVEL_RANK[p.level] >= LEVEL_RANK.write).length,
  },

  actions: {
    async load() {
      if (this.loading) return
      this.loading = true
      try {
        const view = await getMyAccess()
        const next: Record<string, AccessProject> = {}
        for (const p of view.projects) next[p.name] = p
        this.projects = next
        this.restricted = view.restricted
        this.canCreateProjects = view.can_create_projects
        this.personalProject = view.personal_project ?? ''
        this.loaded = true
      } catch {
        /* keep the previous snapshot; the server still enforces everything */
      } finally {
        this.loading = false
      }
    },

    reset() {
      this.projects = {}
      this.restricted = false
      this.canCreateProjects = false
      this.personalProject = ''
      this.loaded = false
    },

    /** Effective level on a project or on the project of a path. */
    level(pathOrProject: string): AccessLevel {
      const auth = useAuthStore()
      if (auth.isOwner) return 'admin'
      return this.projects[projectOf(pathOrProject)]?.level ?? 'none'
    },

    visibility(project: string): Visibility | undefined {
      return this.projects[project]?.visibility
    },

    canRead(pathOrProject: string): boolean {
      return LEVEL_RANK[this.level(pathOrProject)] >= LEVEL_RANK.read
    },

    /** May create, edit and delete notes in the project of the path. */
    canWrite(pathOrProject: string): boolean {
      return LEVEL_RANK[this.level(pathOrProject)] >= LEVEL_RANK.write
    },

    /** May change the project's settings (visibility, flags, rename, delete). */
    canAdmin(pathOrProject: string): boolean {
      return LEVEL_RANK[this.level(pathOrProject)] >= LEVEL_RANK.admin
    },
  },
})

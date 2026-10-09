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
  /** A delete goes to the trash; null until loaded, when a confirmation
   *  promises neither the trash nor a delete for good (BUG-116, S6-3). */
  trash: boolean | null
  loaded: boolean
  loading: boolean
}

/** Top-level folder of a vault path ("proj/a/b.md" → "proj"). */
function projectOf(path: string): string {
  const i = path.indexOf('/')
  return i >= 0 ? path.slice(0, i) : path
}

// A reset bumps the generation, so a response that lands after a sign-out
// does not fill the store again; a load asked while one is on its way runs
// once that one ends, so the event that asked for it is not lost
// (BUG-117, S7-13).
let generation = 0
let again = false

export const useAccessStore = defineStore('access', {
  state: (): AccessState => ({
    projects: {},
    restricted: false,
    canCreateProjects: false,
    personalProject: '',
    trash: null,
    loaded: false,
    loading: false,
  }),

  getters: {
    list: (s): AccessProject[] =>
      Object.values(s.projects).sort((a, b) => a.name.localeCompare(b.name)),
    readableCount: (s): number => Object.keys(s.projects).length,
    writableCount: (s): number =>
      Object.values(s.projects).filter((p) => LEVEL_RANK[p.level] >= LEVEL_RANK.write).length,
    /** The i18n key of the question before a note's delete. */
    deleteNoteKey: (s): string =>
      s.trash === true
        ? 'tree.menu.confirm_delete_note'
        : s.trash === false
          ? 'tree.menu.confirm_delete_note_forever'
          : 'tree.menu.confirm_delete_note_plain',
  },

  actions: {
    async load() {
      if (this.loading) {
        again = true
        return
      }
      this.loading = true
      const gen = generation
      try {
        const view = await getMyAccess()
        if (gen !== generation) return
        const next: Record<string, AccessProject> = {}
        for (const p of view.projects) next[p.name] = p
        this.projects = next
        this.restricted = view.restricted
        this.canCreateProjects = view.can_create_projects
        this.personalProject = view.personal_project ?? ''
        this.trash = view.trash === true
        this.loaded = true
      } catch {
        /* keep the previous snapshot; the server still enforces everything */
      } finally {
        if (gen === generation) this.loading = false
      }
      if (again && gen === generation) {
        again = false
        await this.load()
      }
    },

    reset() {
      generation++
      again = false
      this.loading = false
      this.projects = {}
      this.restricted = false
      this.canCreateProjects = false
      this.personalProject = ''
      this.trash = null
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

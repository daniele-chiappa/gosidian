import { ref } from 'vue'

/**
 * A page older than the server. A build names its chunks after their content
 * and a new one takes the old away, so a tab left open across a release asks
 * for files that are gone: what loads at its first use (the note's editor, a
 * window) does not come, and its place stays empty. Vite tells with this
 * event. A reload brings the new build, and a draft is kept through it.
 */
export const appStale = ref(false)

window.addEventListener('vite:preloadError', () => {
  appStale.value = true
})

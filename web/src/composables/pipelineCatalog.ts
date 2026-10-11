import { ref } from 'vue'
import { getPipelineNodes, type PipelineCatalog } from '@/api/pipelines'

// The node types of the pipelines (IMP-169, F4), asked once and shared by
// the drawings and editors of a page; reload() asks again (a plugin
// accepted meanwhile). When the request fails the catalog stays unknown
// (null) and the next load asks again: the drawings work without it, the
// editor offers no types.

const catalog = ref<PipelineCatalog | null>(null)
let pending: Promise<PipelineCatalog | null> | null = null

export function usePipelineCatalog() {
  function load(): Promise<PipelineCatalog | null> {
    pending ??= getPipelineNodes()
      .then((c) => (catalog.value = c))
      .catch(() => {
        pending = null
        return catalog.value
      })
    return pending
  }
  function reload(): Promise<PipelineCatalog | null> {
    pending = null
    return load()
  }
  return { catalog, load, reload }
}

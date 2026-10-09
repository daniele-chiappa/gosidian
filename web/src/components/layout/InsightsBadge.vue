<script setup lang="ts">
/**
 * Owner-only "new insights" badge. Shows the count of un-triaged
 * self-improvement insights (status:pending). Fetches once on mount and
 * live-refreshes on the SSE 'insight' topic, so a freshly recorded insight
 * bumps the count without a reload. Hidden entirely when the loop is
 * disabled or there is nothing pending. Part of the self-improve delivery
 * layer (plan 20260608-self-improve-feedback-loop).
 */
import { ref, onMounted } from 'vue'
import { Lightbulb } from 'lucide-vue-next'
import { fetchPendingInsights } from '@/api/insights'
import { useSSE } from '@/composables/useSSE'
import { useWindowsStore } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'

const windows = useWindowsStore()
const count = ref(0)
const project = ref('insights')
const enabled = ref(false)

async function refresh() {
  try {
    const r = await fetchPendingInsights()
    enabled.value = r.enabled
    count.value = r.count
    project.value = r.project
  } catch {
    // Owner-only endpoint; on error or non-owner just hide the badge.
    enabled.value = false
  }
}

onMounted(refresh)

const sse = useSSE()
sse.on('insight', () => {
  void refresh()
})

function open() {
  const path = `${project.value}/README.md`
  windows.open({ type: 'note', key: planciaKey('note', path), title: 'insights', props: { path } })
}
</script>

<template>
  <button
    v-if="enabled && count > 0"
    type="button"
    class="h-control-sm px-2 rounded text-xs border border-border hover:bg-surface-hover inline-flex items-center gap-1"
    :title="`${count} new self-improvement insight(s) to review`"
    @click="open"
  >
    <Lightbulb class="w-3 h-3 text-accent" />
    <span>{{ count }}</span>
  </button>
</template>

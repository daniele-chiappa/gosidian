<script setup lang="ts">
/**
 * An upload into IN (IMP-172 phase 5): how far it is, its speed and the
 * time left, the choice for the files whose place is taken, pause, resume
 * and cancel, its files with a retry each, and, once done, what it did.
 * Compact in the IN window, whole in the Uploads window.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Pause, Play, RotateCcw, X, Inbox } from 'lucide-vue-next'
import { errorText } from '@/api/errors'
import { formatSize } from '@/api/format'
import type { UploadPolicy, UploadResultKind } from '@/api/inUpload'
import { confirmAction } from '@/composables/useConfirm'
import { remainingSeconds, sentBytes, totalBytes, useUploadsStore, type UploadItem, type UploadJob } from '@/stores/uploads'

const props = defineProps<{ job: UploadJob; compact?: boolean }>()
const emit = defineEmits<{ openIn: [project: string] }>()

const { t } = useI18n()
const uploads = useUploadsStore()

const total = computed(() => totalBytes(props.job))
const sent = computed(() => sentBytes(props.job))
const percent = computed(() => (total.value ? Math.floor((sent.value * 100) / total.value) : 100))
const taken = computed(() => props.job.items.filter((it) => it.exists && it.state !== 'sent').length)
const failed = computed(() => props.job.items.filter((it) => it.state === 'error'))
const missing = computed(() => props.job.items.filter((it) => it.state === 'missing').length)
const where = computed(() => `${props.job.project}/IN${props.job.dir ? '/' + props.job.dir : ''}`)

const SHOWN = 100
const showAll = ref(false)
const shownItems = computed(() => (showAll.value ? props.job.items : props.job.items.slice(0, SHOWN)))

function duration(s: number): string {
  if (!s || !Number.isFinite(s)) return ''
  if (s < 60) return t('uploads.seconds', { n: Math.ceil(s) })
  if (s < 3600) return t('uploads.minutes', { n: Math.ceil(s / 60) })
  return t('uploads.hours', { h: Math.floor(s / 3600), m: Math.round((s % 3600) / 60) })
}
const left = computed(() => (props.job.state === 'sending' ? duration(remainingSeconds(props.job)) : ''))
const speed = computed(() => (props.job.state === 'sending' && props.job.rate ? `${formatSize(props.job.rate)}/s` : ''))

/** What the completion did, summed over the files and the zips' files. */
const results = computed(() => {
  const out: Partial<Record<UploadResultKind, number>> = {}
  const errors: string[] = []
  for (const it of props.job.items) {
    const r = it.result
    if (!r) continue
    if (r.result === 'unzipped') {
      for (const [k, v] of Object.entries(r.counts ?? {})) out[k as UploadResultKind] = (out[k as UploadResultKind] ?? 0) + (v ?? 0)
      for (const why of r.refused ?? []) errors.push(`${it.path}: ${why}`)
    } else {
      out[r.result] = (out[r.result] ?? 0) + 1
      if (r.result === 'error' && r.error) errors.push(`${it.path}: ${r.error}`)
    }
  }
  return { counts: Object.entries(out).filter(([, v]) => v), errors }
})

const stateClass: Record<UploadJob['state'], string> = {
  opening: 'bg-info/15 text-info',
  asking: 'bg-warning/15 text-warning',
  sending: 'bg-info/15 text-info',
  paused: 'bg-surface-hover text-text-muted',
  completing: 'bg-info/15 text-info',
  done: 'bg-success/15 text-success',
  error: 'bg-danger/15 text-danger',
}

function itemPercent(it: UploadItem): number {
  return it.size ? Math.floor((Math.min(it.size, it.received + it.inflight) * 100) / it.size) : 100
}

async function cancel() {
  const msg = props.job.state === 'done' ? '' : t('uploads.confirm_cancel', { where: where.value })
  if (msg && !(await confirmAction(msg, { confirmLabel: t('uploads.cancel'), danger: true }))) return
  await uploads.cancel(props.job.key)
}

function choose(p: UploadPolicy) {
  uploads.choose(props.job.key, p)
}
</script>

<template>
  <div class="rounded border border-border px-3 py-2 text-xs space-y-2" :data-upload-job="job.id || job.key" :data-upload-state="job.state">
    <div class="flex flex-wrap items-center gap-2">
      <span class="font-mono truncate max-w-80" :title="where">{{ where }}</span>
      <span class="rounded px-1.5 py-0.5" :class="stateClass[job.state]">{{ t(`uploads.state.${job.state}`) }}</span>
      <span class="text-text-muted">{{ t('uploads.files', { count: job.items.length || 0 }, job.items.length) }}</span>
      <span class="flex-1" />
      <button
        v-if="!compact"
        type="button"
        class="h-control-sm px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
        @click="emit('openIn', job.project)"
      ><Inbox class="w-3 h-3" /> {{ t('uploads.open_in') }}</button>
      <button
        v-if="job.state === 'sending' || job.state === 'asking'"
        type="button"
        class="h-control-sm px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
        data-upload-pause
        @click="uploads.pause(job.key)"
      ><Pause class="w-3 h-3" /> {{ t('uploads.pause') }}</button>
      <button
        v-if="job.state === 'paused' || job.state === 'error'"
        type="button"
        class="h-control-sm px-2 rounded border border-border hover:bg-surface-hover inline-flex items-center gap-1"
        data-upload-resume
        @click="uploads.resume(job.key)"
      ><Play class="w-3 h-3" /> {{ t('uploads.resume') }}</button>
      <button
        v-if="job.state !== 'completing'"
        type="button"
        class="h-control-sm px-2 rounded hover:bg-surface-hover inline-flex items-center gap-1"
        :class="job.state === 'done' ? 'text-text-muted' : 'text-danger'"
        :title="job.state === 'done' ? t('uploads.remove') : t('uploads.cancel')"
        data-upload-cancel
        @click="cancel"
      ><X class="w-3 h-3" /> {{ job.state === 'done' ? t('uploads.remove') : t('uploads.cancel') }}</button>
    </div>

    <div v-if="job.state !== 'done'" class="space-y-1">
      <div class="h-1.5 rounded bg-surface-hover overflow-hidden" role="progressbar" :aria-valuenow="percent" aria-valuemin="0" aria-valuemax="100">
        <div class="h-full bg-accent transition-[width]" :style="{ width: percent + '%' }" />
      </div>
      <p class="text-text-muted" data-upload-progress>
        {{ t('uploads.progress', { sent: formatSize(sent), total: formatSize(total), percent }) }}
        <template v-if="speed"> · {{ speed }}</template>
        <template v-if="left"> · {{ t('uploads.left', { time: left }) }}</template>
      </p>
    </div>

    <div v-if="job.state === 'asking'" class="rounded bg-warning/10 px-2 py-2 space-y-2" data-upload-ask>
      <p>{{ t('uploads.taken', { count: taken }, taken) }}</p>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="h-control-sm px-2 rounded border border-border hover:bg-surface-hover" data-upload-policy="replace" @click="choose('replace')">{{ t('uploads.policy.replace') }}</button>
        <button type="button" class="h-control-sm px-2 rounded border border-border hover:bg-surface-hover" data-upload-policy="keep" @click="choose('keep')">{{ t('uploads.policy.keep') }}</button>
        <button type="button" class="h-control-sm px-2 rounded border border-border hover:bg-surface-hover" data-upload-policy="skip" @click="choose('skip')">{{ t('uploads.policy.skip') }}</button>
      </div>
    </div>

    <p v-if="job.full" class="text-danger" data-upload-full>{{ t('uploads.full') }}</p>
    <p v-else-if="job.error" class="text-danger whitespace-pre-line" data-upload-error>{{ errorText(job.error, t, t('uploads.failed')) }}</p>
    <p v-if="job.serverError" class="text-danger whitespace-pre-line" data-upload-error>{{ t('uploads.stopped', { why: job.serverError }) }}</p>
    <p v-if="missing" class="text-warning" data-upload-missing>{{ t('uploads.missing', { count: missing }, missing) }}</p>
    <p v-if="failed.length && compact" class="text-danger">{{ t('uploads.failed_files', { count: failed.length }, failed.length) }}</p>

    <div v-if="job.state === 'done'" class="space-y-1" data-upload-results>
      <p class="flex flex-wrap gap-x-3">
        <span v-for="[k, v] in results.counts" :key="k" :data-upload-result="k">{{ t(`uploads.result.${k}`, { count: v }, v ?? 0) }}</span>
      </p>
      <ul v-if="results.errors.length" class="text-danger list-disc pl-4">
        <li v-for="e in results.errors.slice(0, 20)" :key="e" class="break-words">{{ e }}</li>
      </ul>
    </div>

    <details v-if="!compact && job.items.length" class="text-text-muted">
      <summary class="cursor-pointer">{{ t('uploads.show_files') }}</summary>
      <table class="w-full mt-1">
        <tbody>
          <tr v-for="it in shownItems" :key="it.n" class="border-b border-border/50" :data-upload-item="it.path" :data-upload-item-state="it.state">
            <td class="py-0.5 font-mono break-all">{{ it.path }}<span v-if="it.unzip" class="ml-1 text-info">zip</span></td>
            <td class="py-0.5 text-right whitespace-nowrap">{{ formatSize(it.size) }}</td>
            <td class="py-0.5 text-right whitespace-nowrap w-12">{{ itemPercent(it) }}%</td>
            <td class="py-0.5 whitespace-nowrap">{{ t(`uploads.item.${it.state}`) }}</td>
            <td class="py-0.5 text-right whitespace-nowrap">
              <button v-if="it.state === 'error'" type="button" class="rounded p-0.5 hover:text-text" :title="t('uploads.retry')" :aria-label="t('uploads.retry')" data-upload-retry @click="uploads.retry(job.key, it.n)"><RotateCcw class="w-3 h-3" /></button>
              <button v-if="it.state !== 'sent' && it.state !== 'dropped' && job.state !== 'completing' && job.state !== 'done'" type="button" class="rounded p-0.5 hover:text-danger" :title="t('uploads.drop')" :aria-label="t('uploads.drop')" data-upload-drop @click="uploads.drop(job.key, it.n)"><X class="w-3 h-3" /></button>
            </td>
          </tr>
        </tbody>
      </table>
      <button v-if="job.items.length > SHOWN && !showAll" type="button" class="mt-1 hover:underline" @click="showAll = true">{{ t('uploads.show_all', { count: job.items.length }) }}</button>
    </details>
  </div>
</template>

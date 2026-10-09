<script setup lang="ts">
/**
 * The dialog of confirmAction(), askText() and showNotice()
 * (composables/useConfirm), one request at a time. PlanciaModal keeps the
 * focus inside, closes on Esc and gives the focus back where it was; the
 * confirm button (a prompt's text field) takes it first, so Enter confirms
 * as it did in the browser's dialog.
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { PlanciaModal } from 'plancia'
import { answer, confirmQueue } from '@/composables/useConfirm'

const { t } = useI18n()
const current = computed(() => confirmQueue[0])
const draft = ref('')
watch(current, (c) => (draft.value = c?.value ?? ''), { immediate: true })

const confirmText = computed(() => {
  const c = current.value
  if (!c) return ''
  return c.confirmLabel ?? (c.kind === 'notice' ? t('dialog.ok') : t('dialog.confirm'))
})
// A question is not "Are you sure?": a prompt is titled by what it does.
const title = computed(() => {
  const c = current.value
  if (!c) return ''
  if (c.title) return c.title
  if (c.kind === 'notice') return t('dialog.error_title')
  return c.kind === 'prompt' ? confirmText.value : t('dialog.confirm_title')
})

function confirm() {
  const c = current.value
  if (c) answer(c.id, true, draft.value)
}

// Enter while an input method composes a word is part of the typing.
function onEnter(e: KeyboardEvent) {
  if (e.isComposing) return
  e.preventDefault()
  confirm()
}
</script>

<template>
  <PlanciaModal
    v-if="current"
    :key="current.id"
    :open="true"
    :title="title"
    :labels="{ close: t('plancia.close') }"
    :initial-focus="current.kind === 'prompt' ? '[data-dialog-input]' : '[data-dialog-confirm]'"
    @close="answer(current.id, false)"
  >
    <p class="whitespace-pre-line text-sm" data-dialog-message>{{ current.message }}</p>
    <input
      v-if="current.kind === 'prompt'"
      v-model="draft"
      type="text"
      data-dialog-input
      class="mt-3 h-control py-0 w-full rounded border border-border bg-bg-elevated px-2 text-sm focus:outline-none focus:ring-2 focus:ring-focus"
      :aria-label="current.message"
      @keydown.enter="onEnter"
    />
    <template #footer>
      <button
        v-if="current.kind !== 'notice'"
        type="button"
        class="h-control rounded border border-border px-3 text-sm hover:bg-surface-hover"
        @click="answer(current.id, false)"
      >
        {{ t('common.cancel') }}
      </button>
      <button
        type="button"
        data-dialog-confirm
        class="h-control rounded px-3 text-sm font-medium"
        :class="current.danger ? 'bg-danger text-bg hover:bg-danger/90' : 'bg-accent text-accent-fg hover:bg-accent-hover'"
        @click="confirm"
      >
        {{ confirmText }}
      </button>
    </template>
  </PlanciaModal>
</template>

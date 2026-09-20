<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

// One-time display of a freshly minted set of recovery codes (after enrolment
// or a regeneration). The server keeps only hashes, so this is the user's only
// chance to save them: the parent moves on when `done` fires, not before.
const props = defineProps<{ codes: string[] }>()
const emit = defineEmits<{ done: [] }>()
const { t } = useI18n()
const copied = ref(false)

async function copyAll() {
  try {
    await navigator.clipboard.writeText(props.codes.join('\n'))
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    /* clipboard blocked (e.g. insecure context) — the codes are on screen anyway */
  }
}
</script>

<template>
  <div class="space-y-3">
    <p class="text-sm font-medium">{{ t('totp.recovery_title') }}</p>
    <p class="text-sm text-text-muted">{{ t('totp.recovery_intro') }}</p>
    <ul
      class="grid grid-cols-2 gap-x-6 gap-y-1 rounded bg-bg-elevated border border-border p-3 font-mono text-sm select-all"
    >
      <li v-for="c in codes" :key="c">{{ c }}</li>
    </ul>
    <div class="flex items-center gap-2">
      <button
        type="button"
        class="rounded border border-border px-3 py-2 text-sm hover:bg-surface-hover"
        @click="copyAll"
      >
        {{ copied ? t('totp.recovery_copied') : t('totp.recovery_copy') }}
      </button>
      <button
        type="button"
        class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover"
        @click="emit('done')"
      >
        {{ t('totp.recovery_saved_button') }}
      </button>
    </div>
  </div>
</template>

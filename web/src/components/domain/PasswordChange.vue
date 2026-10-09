<script setup lang="ts">
/**
 * PasswordChange — the account sets a new password with its current one
 * (IMP-063). Used in Settings and in the screen that forces the change of a
 * password the owner chose. The server closes the account's other web
 * sessions; MCP tokens are separate credentials and stay.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { changePassword } from '@/api/password'
import { errorText } from '@/api/errors'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'

const MIN_LENGTH = 8

const emit = defineEmits<{ done: [sessionsClosed: number] }>()

const { t } = useI18n()
const current = ref('')
const next = ref('')
const confirm = ref('')
const busy = ref(false)
const error = ref<string | null>(null)

const problem = computed(() => {
  if (next.value && next.value.length < MIN_LENGTH)
    return t('password.too_short', { n: MIN_LENGTH })
  if (confirm.value && confirm.value !== next.value) return t('password.mismatch')
  if (next.value && next.value === current.value) return t('password.same')
  return null
})
const ready = computed(
  () => Boolean(current.value && next.value && confirm.value) && !problem.value && !busy.value,
)

async function submit() {
  if (!ready.value) return
  busy.value = true
  error.value = null
  try {
    const closed = await changePassword(current.value, next.value)
    current.value = next.value = confirm.value = ''
    emit('done', closed)
  } catch (e) {
    error.value = errorText(e, t, t('password.failed'))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="space-y-2" data-password-change @submit.prevent="submit">
    <label class="block text-sm">
      <span class="text-text-muted">{{ t('password.current') }}</span>
      <input
        v-model="current"
        type="password"
        autocomplete="current-password"
        name="current-password"
        class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-3 focus:outline-none focus:ring-2 focus:ring-focus"
      />
    </label>
    <label class="block text-sm">
      <span class="text-text-muted">{{ t('password.new') }}</span>
      <input
        v-model="next"
        type="password"
        autocomplete="new-password"
        name="new-password"
        class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-3 focus:outline-none focus:ring-2 focus:ring-focus"
      />
    </label>
    <label class="block text-sm">
      <span class="text-text-muted">{{ t('password.confirm') }}</span>
      <input
        v-model="confirm"
        type="password"
        autocomplete="new-password"
        name="confirm-password"
        class="h-control py-0 mt-1 w-full rounded bg-bg-elevated border border-border px-3 focus:outline-none focus:ring-2 focus:ring-focus"
      />
    </label>
    <p v-if="problem" class="text-xs text-warning">
      {{ problem }}
    </p>
    <ErrorMessage v-if="error" :text="error" class="text-sm" />
    <button
      type="submit"
      :disabled="!ready"
      class="h-control rounded bg-accent text-accent-fg px-3 text-sm hover:bg-accent-hover disabled:opacity-60"
    >
      {{ busy ? t('password.saving') : t('password.submit') }}
    </button>
  </form>
</template>

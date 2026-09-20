<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { enrollTOTP, confirmTOTP } from '@/api/totp'
import RecoveryCodes from './RecoveryCodes.vue'

const { t } = useI18n()
// `done` fires only after the user acknowledges the recovery codes; it carries
// how many were minted so the parent can seed the remaining-count display.
const emit = defineEmits<{ done: [count: number] }>()

const secret = ref('')
const uri = ref('')
const qrSvg = ref('')
const code = ref('')
const codes = ref<string[]>([])
const error = ref<string | null>(null)
const busy = ref(false)

async function start() {
  busy.value = true
  error.value = null
  try {
    const d = await enrollTOTP()
    secret.value = d.secret
    uri.value = d.otpauth_uri
    qrSvg.value = d.qr_svg ?? ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : t('totp.start_failed')
  } finally {
    busy.value = false
  }
}

async function confirm() {
  if (!code.value.trim()) return
  busy.value = true
  error.value = null
  try {
    codes.value = await confirmTOTP(secret.value, code.value.trim())
  } catch (e) {
    error.value = e instanceof Error ? e.message : t('totp.invalid_code')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="space-y-3">
    <button
      v-if="!secret"
      type="button"
      :disabled="busy"
      class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
      @click="start"
    >
      {{ t('totp.setup_button') }}
    </button>

    <!-- Secret is active at this point; the codes are shown once, then done. -->
    <RecoveryCodes v-else-if="codes.length" :codes="codes" @done="emit('done', codes.length)" />

    <div v-else class="space-y-3">
      <p class="text-sm text-text-muted">{{ t('totp.scan_instructions') }}</p>

      <!-- Server-rendered QR: inline SVG (no data: URI, so no img-src CSP
           relaxation). Falls back to the secret/URI below if rendering failed. -->
      <div
        v-if="qrSvg"
        class="qr mx-auto h-44 w-44 rounded bg-white p-2"
        role="img"
        :aria-label="t('totp.qr_label')"
        v-html="qrSvg"
      />

      <details class="text-xs text-text-muted">
        <summary class="cursor-pointer select-none">{{ t('totp.manual_entry') }}</summary>
        <p class="mt-2">
          {{ t('totp.secret_label') }}: <code class="font-mono text-text break-all">{{ secret }}</code>
        </p>
        <a :href="uri" class="mt-1 block break-all text-accent font-mono">{{ uri }}</a>
      </details>

      <input
        v-model.trim="code"
        inputmode="numeric"
        autocomplete="one-time-code"
        :placeholder="t('totp.code_placeholder')"
        class="w-full rounded bg-bg-elevated border border-border px-3 py-2 focus:outline-none focus:ring-2 focus:ring-accent"
        @keyup.enter="confirm"
      />
      <button
        type="button"
        :disabled="busy"
        class="rounded bg-accent text-accent-fg px-3 py-2 text-sm hover:bg-accent-hover disabled:opacity-60"
        @click="confirm"
      >
        {{ t('totp.confirm_button') }}
      </button>
    </div>

    <p v-if="error" class="text-sm text-danger">{{ error }}</p>
  </div>
</template>

<style scoped>
.qr :deep(svg) {
  width: 100%;
  height: 100%;
  display: block;
}
</style>

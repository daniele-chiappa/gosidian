<script setup lang="ts">
/**
 * ChatBlock — the ```chat block of a bot's note (IMP-191): where the bot
 * stands in its chat plugin, its chat (the plugin's page, in a frame, with
 * a token gosidian signs for the reader), the code that puts the widget
 * in a site's page, and «Configure again». The plugin serves the chat;
 * gosidian hands it the bot when the note is saved.
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { chatToken, configureChat, fetchChatStatus, type ChatStatus } from '@/api/chat'
import { errorText } from '@/api/errors'

const props = defineProps<{ source: string; notePath?: string }>()
const { t } = useI18n()

const status = ref<ChatStatus | null>(null)
const error = ref<string | null>(null)
const busy = ref(false)
const frameSrc = ref<string | null>(null)
const showCode = ref(false)
const showSnippet = ref(false)

const state = computed(() => status.value?.state)
const problems = computed(() => status.value?.problems ?? [])

async function load() {
  if (!props.notePath) return
  error.value = null
  try {
    status.value = await fetchChatStatus(props.notePath)
  } catch (e) {
    error.value = errorText(e, t, t('chat.load_failed'))
  }
}
onMounted(() => void load())

async function openChat() {
  if (!props.notePath) return
  busy.value = true
  error.value = null
  try {
    const tok = await chatToken(props.notePath)
    // In the fragment: the plugin's page reads it, and it reaches no log.
    frameSrc.value = `${tok.embed_url}#token=${encodeURIComponent(tok.token)}`
  } catch (e) {
    error.value = errorText(e, t, t('chat.token_failed'))
  } finally {
    busy.value = false
  }
}

async function reconfigure() {
  if (!props.notePath) return
  busy.value = true
  error.value = null
  try {
    await configureChat(props.notePath)
    await load()
  } catch (e) {
    error.value = errorText(e, t, t('chat.configure_failed'))
  } finally {
    busy.value = false
  }
}

const snippet = computed(() => {
  const st = state.value
  if (!st?.script_url) return ''
  const id = `chat-${st.bot}`
  const key = status.value?.access.keys[0]
  const attrs = [`src="${st.script_url}"`, `data-bot="${st.bot}"`, `data-container="${id}"`]
  if (key) attrs.push(`data-key="${t('chat.snippet_key', { key })}"`)
  // Split, or the SFC's parser would end this script at the tag.
  const close = '</' + 'script>'
  return `<div id="${id}"></div>\n<script ${attrs.join(' ')} async>${close}`
})
</script>

<template>
  <div class="gosidian-diagram not-prose" data-chat-block>
    <div class="gosidian-diagram__tools">
      <strong v-if="status" class="gosidian-chat__title">{{ status.title || status.bot }}</strong>
      <button
        v-if="state?.configured && status?.access.users"
        type="button"
        class="gosidian-diagram__button"
        :disabled="busy"
        data-chat-open
        @click="frameSrc ? (frameSrc = null) : openChat()"
      >
        {{ frameSrc ? t('chat.close') : t('chat.open') }}
      </button>
      <button
        v-if="status"
        type="button"
        class="gosidian-diagram__button"
        :disabled="busy"
        data-chat-configure
        @click="reconfigure"
      >
        {{ t('chat.reconfigure') }}
      </button>
      <button
        v-if="snippet"
        type="button"
        class="gosidian-diagram__button"
        :aria-pressed="showSnippet"
        data-chat-snippet
        @click="showSnippet = !showSnippet"
      >
        {{ t('chat.snippet') }}
      </button>
      <button
        type="button"
        class="gosidian-diagram__button"
        :aria-pressed="showCode"
        data-chat-code
        @click="showCode = !showCode"
      >
        {{ t('chat.code') }}
      </button>
      <span v-if="state" class="gosidian-diagram__status" data-chat-state>
        {{ state.configured ? t('chat.configured', { plugin: state.plugin }) : t('chat.not_configured', { error: state.error || '' }) }}
      </span>
      <span v-else-if="status && problems.length === 0" class="gosidian-diagram__status" data-chat-state>
        {{ t('chat.pending', { plugin: status.plugin }) }}
      </span>
    </div>
    <p v-for="(p, i) in problems" :key="i" class="gosidian-diagram__status" role="alert">
      {{ t('chat.block_problem', { problem: p.line ? `${t('chat.line', { line: p.line })} ${p.message}` : p.message }) }}
    </p>
    <p v-if="error" class="gosidian-diagram__status" role="alert">{{ error }}</p>
    <p v-if="state?.configured && status && !status.access.users" class="gosidian-diagram__status">
      {{ t('chat.users_off') }}
    </p>
    <iframe
      v-if="frameSrc"
      :src="frameSrc"
      :title="status?.title || status?.bot"
      sandbox="allow-scripts allow-same-origin allow-popups allow-popups-to-escape-sandbox"
      referrerpolicy="no-referrer"
      class="w-full rounded border border-border"
      style="height: 32rem"
      data-chat-frame
    />
    <div v-if="showSnippet && snippet">
      <p class="gosidian-diagram__status">{{ t('chat.snippet_hint') }}</p>
      <pre class="gosidian-diagram__code" data-chat-snippet-code>{{ snippet }}</pre>
    </div>
    <pre v-if="showCode" class="gosidian-diagram__code" data-chat-source>{{ source }}</pre>
  </div>
</template>

<style scoped>
.gosidian-diagram {
  position: relative;
  margin: 1.25em 0;
}
.gosidian-diagram__tools {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 0.25rem;
  margin-bottom: 0.25rem;
}
.gosidian-chat__title {
  margin-right: auto;
  font-size: 0.875rem;
}
.gosidian-diagram__button {
  height: 26px;
  padding: 0 0.5rem;
  border: 1px solid rgb(var(--color-border));
  border-radius: 0.25rem;
  background: rgb(var(--color-surface));
  color: rgb(var(--color-text-muted));
  font-size: 0.75rem;
  line-height: 1;
}
.gosidian-diagram__button:hover,
.gosidian-diagram__button:focus-visible {
  color: rgb(var(--color-text));
  border-color: rgb(var(--color-accent));
}
.gosidian-diagram__code {
  margin: 0;
  padding: 0.75rem;
  overflow: auto;
  border: 1px solid rgb(var(--color-border));
  border-radius: 0.375rem;
  background: rgb(var(--color-bg-elevated));
  font-family: ui-monospace, monospace;
  font-size: 0.8125rem;
  white-space: pre;
}
.gosidian-diagram__status {
  margin: 0.25rem 0;
  font-size: 0.75rem;
  color: rgb(var(--color-text-muted));
}
.gosidian-diagram__tools .gosidian-diagram__status {
  flex-basis: 100%;
  text-align: right;
}
</style>

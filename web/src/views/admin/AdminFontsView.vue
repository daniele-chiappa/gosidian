<script setup lang="ts">
/** The installation's fonts, for the page-layout editor (IMP-226). They are
 *  sent to the editor before a document opens, so a new one counts from the
 *  next opening. The licence is for the owner to read: nothing is refused
 *  for it. */
import { useI18n } from 'vue-i18n'
import { onMounted, ref } from 'vue'
import { addFont, listFonts, removeFont, type CatalogFont } from '@/api/fonts'
import { errorText } from '@/api/errors'
import ScrollRegion from '@/components/primitives/ScrollRegion.vue'
import DateTime from '@/components/primitives/DateTime.vue'
import ErrorMessage from '@/components/primitives/ErrorMessage.vue'
import { confirmAction } from '@/composables/useConfirm'

const { t } = useI18n()

const fonts = ref<CatalogFont[]>([])
const loading = ref(false)
const loaded = ref(false)
const adding = ref(false)
const error = ref<string | null>(null)
const input = ref<HTMLInputElement | null>(null)

async function load() {
  loading.value = true
  error.value = null
  try {
    fonts.value = await listFonts()
    loaded.value = true
  } catch (e) {
    error.value = errorText(e, t, t('admin.fonts.load_failed'))
  } finally {
    loading.value = false
  }
}

async function add(e: Event) {
  const files = Array.from((e.target as HTMLInputElement).files ?? [])
  if (!files.length) return
  adding.value = true
  error.value = null
  try {
    for (const file of files) await addFont(file)
  } catch (err) {
    error.value = errorText(err, t, t('admin.fonts.add_failed'))
  } finally {
    adding.value = false
    if (input.value) input.value.value = ''
  }
  await refresh()
}

/** The list after a change, keeping the error that change gave. */
async function refresh() {
  try {
    fonts.value = await listFonts()
  } catch (e) {
    error.value ??= errorText(e, t, t('admin.fonts.load_failed'))
  }
}

async function remove(f: CatalogFont) {
  const name = `${f.family} ${f.style}`
  if (!(await confirmAction(t('admin.fonts.confirm_remove', { name }), { confirmLabel: t('admin.fonts.remove'), danger: true })))
    return
  error.value = null
  try {
    await removeFont(f.id)
  } catch (e) {
    error.value = errorText(e, t, t('admin.fonts.remove_failed'))
  }
  await refresh()
}

const megabytes = (n: number) => (n / 1048576).toFixed(n < 1048576 ? 2 : 1)
const limited = (f: CatalogFont) => f.embedding === 'preview' || f.embedding === 'restricted'

onMounted(load)
</script>

<template>
  <p class="text-text-muted text-sm mb-3">{{ t('admin.fonts.hint') }}</p>
  <div class="mb-3">
    <input
      ref="input"
      data-font-input
      type="file"
      accept=".ttf,.otf,.ttc"
      multiple
      class="sr-only"
      :aria-label="t('admin.fonts.add')"
      @change="add"
    />
    <button
      type="button"
      class="h-control-sm text-xs px-2 rounded border border-border hover:bg-surface-hover"
      :disabled="adding"
      @click="input?.click()"
    >{{ adding ? t('admin.fonts.adding') : t('admin.fonts.add') }}</button>
  </div>
  <ErrorMessage v-if="error && loaded" :text="error" class="mb-3" />
  <p v-if="loading && !loaded" class="text-text-muted">{{ t('common.loading') }}</p>
  <ErrorMessage v-else-if="error && !loaded" :text="error" />

  <p v-else-if="!fonts.length" class="text-text-muted text-sm">{{ t('admin.fonts.empty') }}</p>

  <ScrollRegion v-else :label="t('admin.tab.fonts')">
    <table class="w-full text-sm">
      <thead class="text-text-muted text-xs [&_th]:font-medium">
        <tr>
          <th class="text-left py-2 px-3">{{ t('admin.fonts.family') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.fonts.style') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.fonts.licence') }}</th>
          <th class="text-right py-2 px-3">{{ t('admin.fonts.size') }}</th>
          <th class="text-left py-2 px-3">{{ t('admin.fonts.added') }}</th>
          <th class="py-2 px-3"><span class="sr-only">{{ t('admin.fonts.remove') }}</span></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="f in fonts" :key="f.id" class="border-t border-border" :data-font="f.id">
          <td class="py-2 px-3">{{ f.family }}</td>
          <td class="py-2 px-3">{{ f.style }}</td>
          <td class="py-2 px-3" :class="limited(f) ? 'text-warning' : 'text-text-muted'" :data-font-licence="f.embedding ?? ''">
            {{ t(`admin.fonts.embedding.${f.embedding ?? 'unknown'}`) }}
          </td>
          <td class="py-2 px-3 text-right tabular-nums">{{ megabytes(f.bytes) }}</td>
          <td class="py-2 px-3 text-xs whitespace-nowrap"><DateTime :value="f.added_at" /></td>
          <td class="py-2 px-3 text-right">
            <button
              type="button"
              class="h-control-sm text-xs px-2 rounded text-danger hover:bg-surface-hover"
              @click="remove(f)"
            >{{ t('admin.fonts.remove') }}</button>
          </td>
        </tr>
      </tbody>
    </table>
  </ScrollRegion>
</template>

<script setup lang="ts">
/**
 * DiagramExportMenu — «Export» of a diagram (IMP-169): its drawing as SVG,
 * its Mermaid, and, for a flow, an Obsidian canvas. A small menu under the
 * button; Escape or a click elsewhere closes it.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { canvasKind, exportDiagram, type DiagramExport } from './diagramExport'

// svg: the drawing as an SVG file, from the view; drawable: there is one.
const props = defineProps<{
  source: string
  svg?: () => string | null | Promise<string | null>
  drawable?: boolean
}>()
const { t } = useI18n()

const open = ref(false)
const box = ref<HTMLElement | null>(null)
const items = computed(() => {
  const out: { as: DiagramExport; label: string }[] = []
  if (props.drawable) out.push({ as: 'svg', label: t('diagram.export_svg_file') })
  out.push({ as: 'mermaid', label: t('diagram.export_mermaid') })
  if (canvasKind(props.source)) out.push({ as: 'canvas', label: t('diagram.export_canvas') })
  return out
})

async function run(as: DiagramExport) {
  open.value = false
  await exportDiagram(props.source, as, props.svg)
}

function onFocusOut(e: FocusEvent) {
  if (!box.value?.contains(e.relatedTarget as Node | null)) open.value = false
}
</script>

<template>
  <span ref="box" class="gosidian-export" @focusout="onFocusOut" @keydown.escape="open = false">
    <button
      type="button"
      class="gosidian-diagram__button"
      aria-haspopup="menu"
      :aria-expanded="open"
      data-diagram-export
      @click="open = !open"
    >
      {{ t('diagram.export') }} ▾
    </button>
    <span v-if="open" class="gosidian-export__menu" role="menu">
      <button
        v-for="i in items"
        :key="i.as"
        type="button"
        role="menuitem"
        :data-diagram-export-as="i.as"
        @click="run(i.as)"
      >
        {{ i.label }}
      </button>
    </span>
  </span>
</template>

<style scoped>
.gosidian-export {
  position: relative;
  display: inline-flex;
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
.gosidian-export__menu {
  position: absolute;
  top: calc(100% + 2px);
  right: 0;
  z-index: 5;
  display: flex;
  flex-direction: column;
  min-width: 12rem;
  padding: 0.25rem;
  border: 1px solid rgb(var(--color-border));
  border-radius: 0.375rem;
  background: rgb(var(--color-surface));
  box-shadow: 0 4px 12px rgb(0 0 0 / 0.12);
}
.gosidian-export__menu button {
  padding: 0.35rem 0.5rem;
  border-radius: 0.25rem;
  text-align: left;
  font-size: 0.8125rem;
  color: rgb(var(--color-text));
}
.gosidian-export__menu button:hover,
.gosidian-export__menu button:focus-visible {
  background: rgb(var(--color-surface-hover));
}
</style>

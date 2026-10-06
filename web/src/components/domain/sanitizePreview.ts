import DOMPurify from 'dompurify'

/**
 * The one barrier between a note's HTML and the page (IMP-128). The
 * server renders markdown with goldmark's raw HTML passing through
 * (WithUnsafe), so a note's `<script>`, `on*=` handlers or `javascript:`
 * links reach the client untouched: whatever shows server-rendered HTML
 * goes through here. The additions keep what the renderer marks up — math,
 * resolved wikilinks (`data-preview-path`), views, headings, embeds.
 */
export function sanitizePreviewHtml(html: string): string {
  return DOMPurify.sanitize(html, {
    ADD_TAGS: ['math', 'mfrac', 'mrow', 'msup', 'mn', 'mi'],
    ADD_ATTR: ['class', 'data-preview-path', 'data-view', 'data-heading', 'data-embed'],
  })
}

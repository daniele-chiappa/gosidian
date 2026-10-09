/**
 * The srcdoc of an HTML note's iframe (ADR-011): the note's own document,
 * with a CSP of its own and the link script in front of everything else.
 * See HTMLPreview.vue for the security model.
 */

// Restrictive policy injected INTO the iframe document. Allows inline script and
// style (the note's own), data: images/fonts/media, and nothing over the
// network. Mirrors the "single self-contained file" contract.
export const INJECTED_CSP = [
  "default-src 'none'",
  "script-src 'unsafe-inline'",
  "style-src 'unsafe-inline'",
  'img-src data:',
  'font-src data:',
  'media-src data:',
].join('; ')

const META = `<meta http-equiv="Content-Security-Policy" content="${INJECTED_CSP}">`

// Strip a leading frontmatter block so it never renders as visible text. The
// HTML-comment form (<!-- --- ... --- -->, ADR-011) is already invisible, but
// the bare markdown form (--- ... ---) shows — drop either.
export function stripFrontmatter(html: string): string {
  return html
    .replace(/^\s*<!--\s*\r?\n---[\s\S]*?---\s*\r?\n?\s*-->\s*/, '')
    .replace(/^\s*---\r?\n[\s\S]*?\r?\n---\r?\n?/, '')
}

// An about:srcdoc iframe INHERITS the shell's CSP (script-src 'self'
// 'nonce-X'), which intersects away the injected 'unsafe-inline' above — so
// a note's inline <script> only runs if it carries the nonce: stamp it onto
// every <script> tag (BUG-019). Absent (e.g. `npm run dev`), leave the
// markup untouched and the dev shell's looser CSP applies.
export function stampScriptNonce(html: string, nonce: string): string {
  if (!nonce) return html
  return html.replace(/<script(?=[\s>])/gi, `<script nonce="${nonce}"`)
}

// LINK_SCRIPT runs inside the iframe and takes the clicks on links (see
// LINKS in HTMLPreview.vue). It runs after the note's own handlers
// (bubbling), so a link a note's script already handles is left alone.
export const LINK_SCRIPT = `(function(){document.addEventListener('click',function(e){
if(e.defaultPrevented||e.button!==0)return;
var a=e.target&&e.target.closest?e.target.closest('a[href]'):null;if(!a)return;
var h=a.getAttribute('href')||'';if(/^javascript:/i.test(h))return;
e.preventDefault();
if(h.charAt(0)==='#'){var id=h.slice(1);try{id=decodeURIComponent(id)}catch(_){}
var el=document.getElementById(id)||document.getElementsByName(id)[0];if(el)el.scrollIntoView();return;}
parent.postMessage({gosidian:'html-note-link',href:h},'*');});})();`

// A doctype at the start, after comments if any: what goes before it would
// put the document in quirks mode.
const DOCTYPE = /^\s*(?:<!--[\s\S]*?-->\s*)*<!doctype[^>]*>/i

/**
 * buildSrcdoc puts the CSP meta and the link script at the very start of
 * the document, after its doctype. The HTML parser opens the head for them
 * and merges the note's own <html> and <head> into it. The meta went into
 * the first match of /<head[^>]*>/ — a <header> of the body, where the
 * browser ignores a CSP, or a <head> inside a comment (BUG-117, S7-5).
 */
export function buildSrcdoc(rawHtml: string, nonce: string): string {
  const html = stampScriptNonce(stripFrontmatter(rawHtml), nonce)
  // The closing tag is split so it does not close an enclosing <script>.
  const head = `${META}<script${nonce ? ` nonce="${nonce}"` : ''}>${LINK_SCRIPT}<` + '/script>'
  const doctype = DOCTYPE.exec(html)
  if (doctype) return doctype[0] + head + html.slice(doctype[0].length)
  // A whole document without a doctype stays in the mode it was written
  // for; a fragment gets one, as it did when it was wrapped in a document.
  if (/<html[\s>]/i.test(html)) return head + html
  return `<!DOCTYPE html>${head}${html}`
}

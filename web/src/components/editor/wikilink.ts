import type { EditorView } from '@codemirror/view'
import {
  pickedCompletion,
  type Completion,
  type CompletionContext,
  type CompletionResult,
} from '@codemirror/autocomplete'
import { suggestNoteTitles } from '@/api/noteTitles'

/**
 * What a wikilink completion writes after the note's path. closeBrackets
 * has usually closed the `[[` already, and the completion added `]]` on
 * top: `[[note]]]]` (BUG-117, S7-6). insert is what to add, skip how many
 * closing brackets already there the cursor moves past.
 */
export function wikilinkClosing(after: string): { insert: string; skip: number } {
  if (after.startsWith(']]')) return { insert: '', skip: 2 }
  if (after.startsWith(']')) return { insert: ']', skip: 1 }
  return { insert: ']]', skip: 0 }
}

/**
 * Wikilink completion source. CodeMirror gives us a CompletionContext
 * from which we slice the `[[<prefix>` opener and ship the prefix to
 * /note-titles, which matches it on the title and the file name and
 * orders the hits (IMP-218, F-27): CodeMirror does not filter them again,
 * since on the label alone `[[nota-b` lost "Nota beta". Each option shows
 * the title, with the path beside. The `apply` field controls what the
 * editor inserts — we put the full path between [[]] for wiki-style links.
 */
export async function wikilinkSource(ctx: CompletionContext): Promise<CompletionResult | null> {
  // Read-only: Ctrl+Space and a click on a suggestion would still write it.
  if (ctx.state.readOnly) return null
  // Look back from cursor for the most recent `[[` and ensure no `]]`
  // closes it before the cursor — otherwise we're not inside an open
  // wikilink. matchBefore won't help here because we need a multi-char
  // opener.
  const lineUpto = ctx.state.doc
    .lineAt(ctx.pos)
    .text.slice(0, ctx.pos - ctx.state.doc.lineAt(ctx.pos).from)
  const open = lineUpto.lastIndexOf('[[')
  if (open === -1) return null
  // Reject if a `]]` falls between the opener and the cursor.
  if (lineUpto.slice(open + 2).includes(']]')) return null
  const prefix = lineUpto.slice(open + 2)
  // Allow inside-word triggering only when explicit (Ctrl+Space) or
  // we already have at least 1 character.
  if (!ctx.explicit && prefix.length === 0) return null

  let hits: { title: string; path: string }[]
  try {
    hits = await suggestNoteTitles(prefix, 10)
  } catch {
    return null
  }
  // Position to replace: from start of prefix to cursor. We keep the
  // `[[` outside the replaced range so it survives.
  const fromPos = ctx.pos - prefix.length
  return {
    from: fromPos,
    filter: false,
    options: hits.map((h) => ({
      label: h.title || h.path,
      // An untitled note's label is its path already.
      detail: h.title ? h.path : undefined,
      apply: (view: EditorView, completion: Completion, from: number, to: number) => {
        const { insert, skip } = wikilinkClosing(view.state.sliceDoc(to, to + 2))
        const text = stripMdSuffix(h.path) + insert
        view.dispatch({
          changes: { from, to, insert: text },
          selection: { anchor: from + text.length + skip },
          annotations: pickedCompletion.of(completion),
          userEvent: 'input.complete',
        })
      },
      type: 'class',
    })),
  }
}

function stripMdSuffix(p: string): string {
  return p.endsWith('.md') ? p.slice(0, -3) : p
}

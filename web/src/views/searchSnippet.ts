/**
 * A search hit's excerpt as plain text. The index cuts the excerpt out of
 * the note's markdown, and the hit list showed its syntax as it is:
 * asterisks, heading marks, list dashes, link targets. Only the marks go;
 * the words stay, and an excerpt cut in the middle of a mark loses the
 * half that is left.
 */
export function plainSnippet(s: string): string {
  const lines = s.split('\n').map((line) =>
    line
      // A code fence line says nothing in an excerpt.
      .replace(/^\s*(```|~~~).*$/, '')
      .replace(/^\s*#{1,6}\s+/, '')
      .replace(/^\s*(>\s*)+/, '')
      .replace(/^\s*([-*+]|\d+[.)])\s+(\[[ xX]\]\s+)?/, ''),
  )
  return (
    lines
      .join(' ')
      // [[target|alias]] shows the alias, [[target#heading]] the target.
      .replace(/!?\[\[([^\]|]*)\|([^\]]*)\]\]/g, '$2')
      .replace(/!?\[\[([^\]#]*)(#[^\]]*)?\]\]/g, '$1')
      .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/(\*\*|__|~~)(?=\S)(.+?)(?<=\S)\1/g, '$2')
      .replace(/(^|[^\w*])\*(?=[^\s*])([^*]+?)(?<=\S)\*(?![\w*])/g, '$1$2')
      .replace(/(^|[^\w])_(?=[^\s_])([^_]+?)(?<=\S)_(?!\w)/g, '$1$2')
      .replace(/`([^`]*)`/g, '$1')
      // What a cut left of a mark: a lone ** or a backtick.
      .replace(/\*\*|__|~~|`/g, '')
      .replace(/\s+/g, ' ')
      .trim()
  )
}

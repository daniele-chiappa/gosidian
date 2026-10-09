/**
 * The web UI sends a note's text in a JSON body to preview it and to save
 * it, and the server reads at most 1 MiB of a JSON body (api/v1
 * DecodeJSON). A larger note, which only git or an import writes, opens
 * read-only as text (S6-13): its preview failed and took the note with it.
 */
export const JSON_BODY_LIMIT = 1 << 20

// Room for the other fields of the body (the path, the keys).
const MARGIN = 4096

/** The bytes the text takes in a JSON body, escapes included. */
export function jsonBytes(text: string): number {
  return new TextEncoder().encode(JSON.stringify(text)).length
}

/** Whether the server would refuse a body carrying this text. */
export function tooLargeToSend(text: string): boolean {
  return jsonBytes(text) > JSON_BODY_LIMIT - MARGIN
}

import client from './client'

/** Where a bot stands in its plugin (IMP-191). */
export interface ChatState {
  bot: string
  plugin: string
  note: string
  project: string
  configured: boolean
  error?: string
  updated: string
  public_url?: string
  embed_url?: string
  script_url?: string
  users: boolean
  keys?: string[]
}

export interface ChatProblem {
  line?: number
  message: string
}

/** The bot of a note: its block as the server read it, and its state. */
export interface ChatStatus {
  bot: string
  plugin: string
  pipeline: string
  title?: string
  access: { users: boolean; keys: string[]; signed: boolean; open: boolean }
  problems: ChatProblem[] | null
  state?: ChatState
}

export async function fetchChatStatus(note: string): Promise<ChatStatus> {
  const { data } = await client.get<ChatStatus>('/chat/status', { params: { note } })
  return data
}

/** A token for the caller to chat with the bot, signed by gosidian. */
export async function chatToken(note: string): Promise<{ token: string; bot: string; embed_url: string; expires: string }> {
  const { data } = await client.post('/chat/token', { note })
  return data
}

/** Hands the bot to its plugin now. */
export async function configureChat(note: string): Promise<{ state: ChatState }> {
  const { data } = await client.post('/chat/configure', { note })
  return data
}

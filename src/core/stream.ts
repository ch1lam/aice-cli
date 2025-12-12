export const KNOWN_PROVIDERS = ['openai', 'deepseek'] as const
export type ProviderId = (typeof KNOWN_PROVIDERS)[number]

const knownProviderSet = new Set<string>(KNOWN_PROVIDERS)

export function isProviderId(value: string): value is ProviderId {
  return knownProviderSet.has(value)
}

export function parseProviderId(value: string): ProviderId | undefined {
  return isProviderId(value) ? value : undefined
}

export type StreamStatus = 'completed' | 'failed' | 'queued' | 'running'

export type TokenUsage = {
  inputTokens?: number
  outputTokens?: number
  totalTokens?: number
}

export interface StreamChunkBase {
  timestamp?: number
}

export interface ProviderTextChunk extends StreamChunkBase {
  text: string
  type: 'text'
}

export interface ProviderUsageChunk extends StreamChunkBase {
  type: 'usage'
  usage: TokenUsage
}

export interface ProviderStatusChunk extends StreamChunkBase {
  detail?: string
  status: StreamStatus
  type: 'status'
}

export interface ProviderErrorChunk extends StreamChunkBase {
  error: Error
  type: 'error'
}

export type ProviderStreamChunk =
  | ProviderErrorChunk
  | ProviderStatusChunk
  | ProviderTextChunk
  | ProviderUsageChunk

export interface SessionMetaChunk extends StreamChunkBase {
  model: string
  providerId: ProviderId
  type: 'meta'
}

export interface SessionTextChunk extends ProviderTextChunk {
  index: number
}

export interface SessionDoneChunk extends StreamChunkBase {
  type: 'done'
}

export type SessionStreamChunk =
  | ProviderErrorChunk
  | ProviderStatusChunk
  | ProviderUsageChunk
  | SessionDoneChunk
  | SessionMetaChunk
  | SessionTextChunk

export type ProviderStream = AsyncGenerator<ProviderStreamChunk, void, void>
export type SessionStream = AsyncGenerator<SessionStreamChunk, void, void>

import type { LanguageModelUsage, TextStreamPart, ToolSet } from 'ai'

export type ProviderId = string

export type StreamStatus = 'aborted' | 'completed' | 'failed' | 'running'

export type TokenUsage = LanguageModelUsage

export interface SessionMetaChunk {
  model: string
  providerId: ProviderId
  type: 'meta'
}

export type ProviderStreamChunk = TextStreamPart<ToolSet>

export type SessionStreamChunk = ProviderStreamChunk | SessionMetaChunk

export type ProviderStream = AsyncIterable<ProviderStreamChunk>
export type SessionStream = AsyncIterable<SessionStreamChunk>

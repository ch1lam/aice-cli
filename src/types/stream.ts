import type { LanguageModelUsage, TextStreamPart, ToolSet } from 'ai'

export type ProviderId = string

export type StreamStatus = 'aborted' | 'completed' | 'failed' | 'running'

export type TokenUsage = LanguageModelUsage

export type ProviderStreamChunk = TextStreamPart<ToolSet>

export type ProviderStream = AsyncIterable<ProviderStreamChunk>
export type SessionStream = ProviderStream

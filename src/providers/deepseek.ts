import type { ChatCompletionChunk, ChatCompletionMessageParam } from 'openai/resources/chat/completions'
import type { Stream } from 'openai/streaming'

import { OpenAI } from 'openai'

import type { LLMProvider, SessionRequest } from '../core/session.js'
import type { ProviderStream, TokenUsage } from '../core/stream.js'

import { type ProviderStreamEvent, streamProviderWithLifecycle } from './streaming.js'

type ChatCompletionStream = Stream<ChatCompletionChunk> & {
  controller?: {
    abort: () => void
  }
}

type DeepSeekDelta =
  & ChatCompletionChunk['choices'][number]['delta']
  & { reasoning_content?: null | string }

type ChatCompletionsClient = {
  chat: {
    completions: {
      create(args: {
        messages: ChatCompletionMessageParam[]
        model: string
        signal?: AbortSignal
        stream: true
        temperature?: number
      }): Promise<ChatCompletionStream>
    }
  }
}

type DeltaContent =
  | Array<{
    text?: string
    type: string
  }>
  | null
  | string

type DeepSeekUsage = {
  completion_tokens?: number
  prompt_tokens?: number
  total_tokens?: number
}

const DEFAULT_ERROR_MESSAGE = 'DeepSeek request failed'

export interface DeepSeekProviderConfig {
  apiKey: string
  baseURL?: string
  model?: string
}

export interface DeepSeekSessionRequest extends SessionRequest {
  temperature?: number
}

export class DeepSeekProvider implements LLMProvider<DeepSeekSessionRequest> {
  readonly id = 'deepseek' as const
  #client: ChatCompletionsClient
  #defaultModel?: string

  constructor(config: DeepSeekProviderConfig, client?: ChatCompletionsClient) {
    if (!config.apiKey) {
      throw new Error('Missing DeepSeek API key')
    }

    this.#client = client ?? new OpenAI({ apiKey: config.apiKey, baseURL: config.baseURL })
    this.#defaultModel = config.model
  }

  stream(request: DeepSeekSessionRequest): ProviderStream {
    return this.#streamChatCompletions(request)
  }

  #buildMessages(request: DeepSeekSessionRequest): ChatCompletionMessageParam[] {
    const messages: ChatCompletionMessageParam[] = []

    if (request.systemPrompt) {
      messages.push({ content: request.systemPrompt, role: 'system' })
    }

    messages.push({ content: request.prompt, role: 'user' })

    return messages
  }

  #extractContent(deltaContent?: DeltaContent): string[] {
    if (!deltaContent) return []
    if (typeof deltaContent === 'string') return [deltaContent]

    return deltaContent.flatMap(part => (part.text ? [part.text] : []))
  }

  #mapChunk(chunk: ChatCompletionChunk): ProviderStreamEvent[] {
    const chunks: ProviderStreamEvent[] = []

    for (const choice of chunk.choices) {
      const delta = choice.delta as DeepSeekDelta | undefined
      const deltas = [
        ...this.#extractContent(delta?.content as DeltaContent | undefined),
        ...(delta?.reasoning_content ? [delta.reasoning_content] : []),
      ]

      for (const delta of deltas) {
        chunks.push({ text: delta, type: 'text' })
      }
    }

    if (chunk.usage) {
      chunks.push({ type: 'usage', usage: this.#mapUsage(chunk.usage) })
    }

    return chunks
  }

  #mapUsage(usage?: DeepSeekUsage | null): TokenUsage {
    return {
      inputTokens: usage?.prompt_tokens ?? undefined,
      outputTokens: usage?.completion_tokens ?? undefined,
      totalTokens: usage?.total_tokens ?? undefined,
    }
  }

  #streamChatCompletions(request: DeepSeekSessionRequest): ProviderStream {
    const model = request.model ?? this.#defaultModel

    if (!model) {
      throw new Error('DeepSeek model is required')
    }

    return streamProviderWithLifecycle({
      createStream: () => this.#client.chat.completions.create({
        messages: this.#buildMessages(request),
        model,
        signal: request.signal,
        stream: true,
        temperature: request.temperature,
      }),
      mapEvent: chunk => this.#mapChunk(chunk),
      startFallbackMessage: DEFAULT_ERROR_MESSAGE,
      streamFallbackMessage: DEFAULT_ERROR_MESSAGE,
    })
  }
}

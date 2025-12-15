import type {
  EasyInputMessage,
  ResponseCreateParamsStreaming,
  ResponseStreamEvent,
  ResponseUsage,
} from 'openai/resources/responses/responses'

import { OpenAI } from 'openai'

import type { LLMProvider, SessionRequest } from '../core/session.js'
import type { ProviderStream, TokenUsage } from '../core/stream.js'

import { type ProviderStreamEvent, streamProviderWithLifecycle } from './streaming.js'

export interface OpenAIProviderConfig {
  apiKey: string
  baseURL?: string
  model?: string
}

export interface OpenAISessionRequest extends SessionRequest {
  temperature?: number
}

export class OpenAIProvider implements LLMProvider<OpenAISessionRequest> {
  readonly id = 'openai' as const
  #client: OpenAI
  #defaultModel?: string

  constructor(config: OpenAIProviderConfig, client?: OpenAI) {
    if (!config.apiKey) {
      throw new Error('Missing OpenAI API key')
    }

    this.#client = client ?? new OpenAI({
      apiKey: config.apiKey,
      baseURL: config.baseURL,
    })
    this.#defaultModel = config.model
  }

  stream(request: OpenAISessionRequest): ProviderStream {
    return this.#streamResponses(request)
  }

  #buildInput(request: OpenAISessionRequest): ResponseCreateParamsStreaming['input'] {
    const segments: EasyInputMessage[] = []

    if (request.systemPrompt) {
      segments.push({ content: request.systemPrompt, role: 'system' })
    }

    segments.push({ content: request.prompt, role: 'user' })

    return segments
  }

  #mapEvent(event: ResponseStreamEvent): null | ProviderStreamEvent {
    if (event.type === 'response.output_text.delta') {
      return { text: event.delta, type: 'text' }
    }

    if (event.type === 'response.completed') {
      if (!event.response.usage) return null
      return { type: 'usage', usage: this.#mapUsage(event.response.usage) }
    }

    if (event.type === 'response.failed') {
      return { error: event.response.error, fallbackMessage: 'OpenAI response failed', type: 'error' }
    }

    if (event.type === 'error') {
      return {
        error: { code: event.code ?? undefined, message: event.message },
        fallbackMessage: 'OpenAI stream error',
        type: 'error',
      }
    }

    return null
  }

  #mapUsage(usage?: null | ResponseUsage): TokenUsage {
    return {
      inputTokens: usage?.input_tokens ?? undefined,
      outputTokens: usage?.output_tokens ?? undefined,
      totalTokens: usage?.total_tokens ?? undefined,
    }
  }

  #streamResponses(request: OpenAISessionRequest): ProviderStream {
    const model = request.model ?? this.#defaultModel

    if (!model) {
      throw new Error('OpenAI model is required')
    }

    return streamProviderWithLifecycle({
      createStream: () => this.#client.responses.stream({
        input: this.#buildInput(request),
        model,
        signal: request.signal,
        temperature: request.temperature,
      }),
      mapEvent: event => this.#mapEvent(event),
      startFallbackMessage: 'OpenAI response failed to start',
      streamFallbackMessage: 'OpenAI stream failed',
    })
  }
}

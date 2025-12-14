import type {
  EasyInputMessage,
  ResponseCreateParamsStreaming,
  ResponseStreamEvent,
  ResponseUsage,
} from 'openai/resources/responses/responses'

import { OpenAI } from 'openai'

import type { LLMProvider, SessionRequest } from '../core/session.js'
import type { ProviderStream, TokenUsage } from '../core/stream.js'

import { toError } from '../core/errors.js'

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

  #errorChunk(error: unknown, fallbackMessage: string): {error: Error; timestamp: number; type: 'error'} {
    return {
      error: toError(error, fallbackMessage),
      timestamp: Date.now(),
      type: 'error',
    }
  }

  #mapUsage(usage?: null | ResponseUsage): TokenUsage {
    return {
      inputTokens: usage?.input_tokens ?? undefined,
      outputTokens: usage?.output_tokens ?? undefined,
      totalTokens: usage?.total_tokens ?? undefined,
    }
  }

  #status(status: 'completed' | 'failed' | 'running') {
    return { status, timestamp: Date.now(), type: 'status' } as const
  }

  async *#streamResponses(request: OpenAISessionRequest): ProviderStream {
    const model = request.model ?? this.#defaultModel

    if (!model) {
      throw new Error('OpenAI model is required')
    }

    yield this.#status('running')

    let response: AsyncIterable<ResponseStreamEvent> & {controller?: {abort: () => Promise<void> | void}}

    try {
      response = await this.#client.responses.stream({
        input: this.#buildInput(request),
        model,
        signal: request.signal,
        temperature: request.temperature,
      })
    } catch (error) {
      yield this.#status('failed')
      yield this.#errorChunk(error, 'OpenAI response failed to start')
      return
    }

    let latestUsage: TokenUsage | undefined

    try {
      for await (const event of response) {
        const now = Date.now()

        if (event.type === 'response.output_text.delta') {
          yield { text: event.delta, timestamp: now, type: 'text' }
          continue
        }

        if (event.type === 'response.completed') {
          latestUsage = event.response.usage
            ? this.#mapUsage(event.response.usage)
            : latestUsage
          continue
        }

        if (event.type === 'response.failed') {
          yield this.#status('failed')
          yield this.#errorChunk(event.response.error, 'OpenAI response failed')
          return
        }

        if (event.type === 'error') {
          yield this.#status('failed')
          yield this.#errorChunk({ code: event.code ?? undefined, message: event.message }, 'OpenAI stream error')
          return
        }
      }
    } catch (error) {
      yield this.#status('failed')
      yield this.#errorChunk(error, 'OpenAI stream failed')
      return
    } finally {
      await response.controller?.abort()
    }

    if (latestUsage) {
      yield { timestamp: Date.now(), type: 'usage', usage: latestUsage }
    }

    yield this.#status('completed')
  }
}

import OpenAI from 'openai'

import type {LLMProvider, SessionRequest} from '../core/session.js'
import type {ProviderStream, ProviderStreamChunk, TokenUsage} from '../core/stream.js'

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

  #buildInput(request: OpenAISessionRequest): Array<{content: string; role: 'system' | 'user'}> {
    const segments: Array<{content: string; role: 'system' | 'user'}> = []

    if (request.systemPrompt) {
      segments.push({content: request.systemPrompt, role: 'system'})
    }

    segments.push({content: request.prompt, role: 'user'})

    return segments
  }

  #mapEventToChunk(event: OpenAI.Client.Responses.Stream.Event): null | ProviderStreamChunk {
    switch (event.type) {
      case 'response.completed': {
        return {
          status: 'completed',
          timestamp: Date.now(),
          type: 'status',
        }
      }

      case 'response.error': {
        return {
          error: event.error ?? new Error('Unknown OpenAI error'),
          timestamp: Date.now(),
          type: 'error',
        }
      }

      case 'response.output_text.delta': {
        return {
          text: event.delta,
          timestamp: Date.now(),
          type: 'text',
        }
      }

      case 'response.output_text.done': {
        return null
      }

      case 'response.output_usage': {
        return {
          timestamp: Date.now(),
          type: 'usage',
          usage: this.#mapUsage(event.usage),
        }
      }

      default: {
        return null
      }
    }
  }

  #mapUsage(usage?: null | OpenAI.ResponseUsage): TokenUsage {
    return {
      inputTokens: usage?.input_tokens ?? undefined,
      outputTokens: usage?.output_tokens ?? undefined,
      totalTokens: usage?.total_tokens ?? undefined,
    }
  }

  async *#streamResponses(request: OpenAISessionRequest): ProviderStream {
    const model = request.model ?? this.#defaultModel

    if (!model) {
      throw new Error('OpenAI model is required')
    }

    const response = await this.#client.responses.stream({
      input: this.#buildInput(request),
      model,
      signal: request.signal,
      temperature: request.temperature,
    })

    try {
      for await (const event of response) {
        const chunk = this.#mapEventToChunk(event)
        if (chunk) {
          yield chunk
        }
      }
    } finally {
      await response.controller?.abort()
    }
  }
}

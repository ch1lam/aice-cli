import type {
  EasyInputMessage,
  ResponseCreateParamsStreaming,
  ResponseStreamEvent,
  ResponseUsage,
} from 'openai/resources/responses/responses'

import {OpenAI} from 'openai'

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

  #buildInput(request: OpenAISessionRequest): ResponseCreateParamsStreaming['input'] {
    const segments: EasyInputMessage[] = []

    if (request.systemPrompt) {
      segments.push({content: request.systemPrompt, role: 'system'})
    }

    segments.push({content: request.prompt, role: 'user'})

    return segments
  }

  #mapEventToChunks(event: ResponseStreamEvent): ProviderStreamChunk[] {
    const now = Date.now()

    switch (event.type) {
      case 'error': {
        return [{error: new Error(event.message), timestamp: now, type: 'error'}]
      }

      case 'response.completed': {
        const chunks: ProviderStreamChunk[] = []

        if (event.response.usage) {
          chunks.push({
            timestamp: now,
            type: 'usage',
            usage: this.#mapUsage(event.response.usage),
          })
        }

        chunks.push({status: 'completed', timestamp: now, type: 'status'})

        return chunks
      }

      case 'response.failed': {
        return [{error: new Error('OpenAI response failed'), timestamp: now, type: 'error'}]
      }

      case 'response.in_progress': {
        return [{status: 'running', timestamp: now, type: 'status'}]
      }

      case 'response.output_text.delta': {
        return [{text: event.delta, timestamp: now, type: 'text'}]
      }

      case 'response.output_text.done': {
        return []
      }

      default: {
        return []
      }
    }
  }

  #mapUsage(usage?: null | ResponseUsage): TokenUsage {
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
        const chunks = this.#mapEventToChunks(event)
        for (const chunk of chunks) {
          yield chunk
        }
      }
    } finally {
      await response.controller?.abort()
    }
  }
}

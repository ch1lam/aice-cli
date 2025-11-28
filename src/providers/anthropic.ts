import Anthropic from '@anthropic-ai/sdk'

import type {LLMProvider, SessionRequest} from '../core/session.js'
import type {ProviderStream, TokenUsage} from '../core/stream.js'

type AnthropicUsage = {
  input_tokens?: number
  inputTokens?: number
  output_tokens?: number
  outputTokens?: number
  total_tokens?: number
  totalTokens?: number
}

type AnthropicStreamEvent = {
  delta?: {text?: string; type?: string; usage?: AnthropicUsage}
  error?: {message?: string}
  type: string
  usage?: AnthropicUsage
}

type AnthropicMessageStream = AsyncIterable<AnthropicStreamEvent> & {
  finalMessage?: () => Promise<{usage?: AnthropicUsage}>
}

type AnthropicClient = {
  messages: {
    create(args: {
      max_tokens: number
      messages: Array<{content: string; role: 'user'}>
      model: string
      stream: true
      system?: string
      temperature?: number
    }): Promise<AnthropicMessageStream>
  }
}

export interface AnthropicProviderConfig {
  apiKey: string
  baseURL?: string
  maxTokens?: number
  model?: string
}

export interface AnthropicSessionRequest extends SessionRequest {
  maxTokens?: number
  temperature?: number
}

export class AnthropicProvider implements LLMProvider<AnthropicSessionRequest> {
  readonly id = 'anthropic' as const
  #client: AnthropicClient
  #defaultMaxTokens: number
  #defaultModel?: string

  constructor(config: AnthropicProviderConfig, client?: AnthropicClient) {
    if (!config.apiKey) {
      throw new Error('Missing Anthropic API key')
    }

    if (client) {
      this.#client = client
    } else {
      const AnthropicCtor = Anthropic as unknown as new (opts: {
        apiKey: string
        baseURL?: string
      }) => AnthropicClient
      this.#client = new AnthropicCtor({apiKey: config.apiKey, baseURL: config.baseURL})
    }

    this.#defaultModel = config.model
    this.#defaultMaxTokens = config.maxTokens ?? 1024
  }

  stream(request: AnthropicSessionRequest): ProviderStream {
    return this.#streamMessages(request)
  }

  #mapUsage(usage: AnthropicUsage): TokenUsage {
    return {
      inputTokens: usage?.inputTokens ?? usage?.input_tokens,
      outputTokens: usage?.outputTokens ?? usage?.output_tokens,
      totalTokens: usage?.totalTokens ?? usage?.total_tokens,
    }
  }

  // eslint-disable-next-line complexity -- event mapping handles multiple stream cases
  async *#streamMessages(request: AnthropicSessionRequest): ProviderStream {
    const model = request.model ?? this.#defaultModel

    if (!model) {
      throw new Error('Anthropic model is required')
    }

    const stream = await this.#client.messages.create({
      /* eslint-disable camelcase */
      max_tokens: request.maxTokens ?? this.#defaultMaxTokens,
      /* eslint-enable camelcase */
      messages: [{content: request.prompt, role: 'user'}],
      model,
      stream: true,
      system: request.systemPrompt,
      temperature: request.temperature,
    })

    let sawStop = false

    for await (const event of stream as AsyncIterable<AnthropicStreamEvent>) {
      const now = Date.now()

      if (event.type === 'error') {
        yield {
          error: new Error(event.error?.message ?? 'Anthropic stream error'),
          timestamp: now,
          type: 'error',
        }
        return
      }

      if (event.type === 'message_start') {
        yield {status: 'running', timestamp: now, type: 'status'}
        continue
      }

      if (event.type === 'content_block_delta' && event.delta?.text) {
        yield {text: event.delta.text, timestamp: now, type: 'text'}
        continue
      }

      if (event.type === 'message_delta' && event.delta?.usage) {
        yield {timestamp: now, type: 'usage', usage: this.#mapUsage(event.delta.usage)}
        continue
      }

      if (event.type === 'message_stop') {
        sawStop = true
      }
    }

    let usage: AnthropicUsage | undefined

    try {
      const finalMessage = await (stream as AnthropicMessageStream).finalMessage?.()
      usage = finalMessage?.usage
    } catch {
      usage = undefined
    }

    if (usage) {
      yield {timestamp: Date.now(), type: 'usage', usage: this.#mapUsage(usage)}
    }

    if (sawStop) {
      yield {status: 'completed', timestamp: Date.now(), type: 'status'}
    }
  }
}

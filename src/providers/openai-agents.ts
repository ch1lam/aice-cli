import {
  Agent,
  type ModelSettings,
  OpenAIProvider,
  type ResponseStreamEvent,
  type RunItemStreamEvent,
  Runner,
  type RunRawModelStreamEvent,
  type RunStreamEvent,
} from '@openai/agents'

import type {LLMProvider, SessionRequest} from '../core/session.js'
import type {ProviderStream, ProviderStreamChunk, TokenUsage} from '../core/stream.js'

export interface OpenAIAgentsProviderConfig {
  apiKey: string
  baseURL?: string
  instructions?: string
  model?: string
}

export interface OpenAIAgentsSessionRequest extends SessionRequest {
  temperature?: number
}

export class OpenAIAgentsProvider implements LLMProvider<OpenAIAgentsSessionRequest> {
  readonly id = 'openai-agents' as const
  #defaultInstructions: string
  #defaultModel?: string
  #runner: Runner

  constructor(config: OpenAIAgentsProviderConfig, runner?: Runner) {
    if (!config.apiKey) {
      throw new Error('Missing OpenAI API key')
    }

    this.#defaultInstructions = config.instructions ?? 'You are a helpful assistant.'
    this.#defaultModel = config.model

    this.#runner =
      runner ??
      new Runner({
        model: config.model,
        modelProvider: new OpenAIProvider({
          apiKey: config.apiKey,
          baseURL: config.baseURL,
          useResponses: true,
        }),
      })
  }

  stream(request: OpenAIAgentsSessionRequest): ProviderStream {
    return this.#streamAgent(request)
  }

  #buildAgent(request: OpenAIAgentsSessionRequest): Agent {
    const instructions = request.systemPrompt ?? this.#defaultInstructions
    const model = request.model ?? this.#defaultModel
    const modelSettings: ModelSettings | undefined =
      request.temperature === undefined ? undefined : {temperature: request.temperature}

    return new Agent({
      instructions,
      model,
      modelSettings,
      name: 'AICE Agent',
    })
  }

  #mapResponseEvent(event: RunRawModelStreamEvent): ProviderStreamChunk[] {
    const payload = event.data as ResponseStreamEvent
    const now = Date.now()

    switch (payload.type) {
      case 'output_text_delta': {
        return [{text: payload.delta, timestamp: now, type: 'text'}]
      }

      case 'response_done': {
        const chunks: ProviderStreamChunk[] = []

        if (payload.response?.usage) {
          chunks.push({
            timestamp: now,
            type: 'usage',
            usage: this.#mapUsage(payload.response.usage),
          })
        }

        chunks.push({status: 'completed', timestamp: now, type: 'status'})
        return chunks
      }

      case 'response_started': {
        return [{status: 'running', timestamp: now, type: 'status'}]
      }

      default: {
        return []
      }
    }
  }

  #mapRunEvent(event: RunStreamEvent): ProviderStreamChunk[] {
    switch (event.type) {
      case 'agent_updated_stream_event': {
        const detail = event.agent?.name ? `agent=${event.agent.name}` : undefined
        return [
          {
            detail,
            status: 'running',
            timestamp: Date.now(),
            type: 'status',
          },
        ]
      }

      case 'raw_model_stream_event': {
        return this.#mapResponseEvent(event)
      }

      case 'run_item_stream_event': {
        return this.#mapRunItem(event)
      }

      default: {
        return []
      }
    }
  }

  #mapRunItem(event: RunItemStreamEvent): ProviderStreamChunk[] {
    const now = Date.now()

    if (event.item?.type === 'message_output_item') {
      // Message output items summarize the turn; the raw model stream already carries deltas.
      return []
    }

    const detail = event.name ?? event.item?.type
    if (detail === undefined) return []

    return [
      {
        detail,
        status: 'running',
        timestamp: now,
        type: 'status',
      },
    ]
  }

  #mapUsage(usage: {inputTokens?: number; outputTokens?: number; totalTokens?: number}): TokenUsage {
    return {
      inputTokens: usage?.inputTokens,
      outputTokens: usage?.outputTokens,
      totalTokens: usage?.totalTokens,
    }
  }

  async *#streamAgent(request: OpenAIAgentsSessionRequest): ProviderStream {
    const agent = this.#buildAgent(request)
    let runError: unknown

    try {
      const runResult = await this.#runner.run(agent, request.prompt, {
        signal: request.signal,
        stream: true,
      })

      try {
        for await (const event of runResult as AsyncIterable<RunStreamEvent>) {
          const chunks = this.#mapRunEvent(event)
          for (const chunk of chunks) {
            yield chunk
          }
        }
      } finally {
        try {
          await runResult.completed
        } catch (error) {
          runError = runError ?? error
        }

        runError = runError ?? runResult.error
      }
    } catch (error) {
      runError = runError ?? error
    }

    if (runError) {
      yield {error: this.#toError(runError), timestamp: Date.now(), type: 'error'}
    }
  }

  #toError(error: unknown): Error {
    if (error instanceof Error) return error
    return new Error(typeof error === 'string' ? error : 'Agent run failed')
  }
}

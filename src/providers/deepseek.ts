import { createDeepSeek } from '@ai-sdk/deepseek'
import {
  type LanguageModel,
  type ModelMessage,
  streamText,
  type TextStreamPart,
  type ToolSet,
} from 'ai'

import type { LLMProvider, SessionRequest } from '../types/session.js'
import type { ProviderStream } from '../types/stream.js'

type StreamTextPart = TextStreamPart<ToolSet>

type StreamTextResult = {
  fullStream: AsyncIterable<StreamTextPart>
}

type StreamTextFn = (options: {
  abortSignal?: AbortSignal
  messages: ModelMessage[]
  model: LanguageModel
  temperature?: number
}) => StreamTextResult

type ModelFactory = (modelId: string) => LanguageModel

export interface DeepSeekProviderConfig {
  apiKey: string
  baseURL?: string
  model?: string
}

export interface DeepSeekSessionRequest extends SessionRequest {
  temperature?: number
}

type DeepSeekProviderDependencies = {
  modelFactory?: ModelFactory
  streamText?: StreamTextFn
}

export class DeepSeekProvider implements LLMProvider<DeepSeekSessionRequest> {
  readonly id = 'deepseek' as const
  #defaultModel?: string
  #modelFactory: ModelFactory
  #streamText: StreamTextFn

  constructor(config: DeepSeekProviderConfig, dependencies: DeepSeekProviderDependencies = {}) {
    if (!config.apiKey) {
      throw new Error('Missing DeepSeek API key')
    }

    this.#defaultModel = config.model
    this.#modelFactory =
      dependencies.modelFactory
      ?? createDeepSeek({ apiKey: config.apiKey, baseURL: config.baseURL })
    this.#streamText = dependencies.streamText ?? streamText
  }

  stream(request: DeepSeekSessionRequest): ProviderStream {
    const modelId = request.model ?? this.#defaultModel

    if (!modelId) {
      throw new Error('DeepSeek model is required')
    }

    const model = this.#modelFactory(modelId)

    return this.#streamText({
      abortSignal: request.signal,
      messages: request.messages,
      model,
      temperature: request.temperature,
    }).fullStream
  }
}

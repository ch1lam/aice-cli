import type { ProviderEnv } from '../types/env.js'
import type { ProviderRequestInput } from '../types/provider.js'
import type { LLMProvider, SessionRequest } from '../types/session.js'
import type { SessionStream } from '../types/stream.js'

import { resolveDefaultBaseURL, resolveDefaultModel } from '../config/provider-defaults.js'
import { DeepSeekProvider } from '../providers/deepseek.js'

export type ChatPrompt = ProviderRequestInput

export interface ChatServiceOptions {
  createProvider?: ProviderFactory
}

type ProviderFactory = (env: ProviderEnv) => LLMProvider<SessionRequest>

export class ChatService {
  #createProvider: ProviderFactory

  constructor(options: ChatServiceOptions = {}) {
    this.#createProvider = options.createProvider ?? createDefaultProvider
  }

  createStream(env: ProviderEnv, input: ChatPrompt): SessionStream {
    const provider = this.#createProvider(env)

    if (provider.id !== env.providerId) {
      throw new Error(`Provider mismatch: expected ${env.providerId}, got ${provider.id}`)
    }

    const request: SessionRequest = {
      messages: input.messages,
      model: resolveDefaultModel(env.providerId, input.model ?? env.model),
      providerId: env.providerId,
      temperature: input.temperature,
    }

    return provider.stream(request)
  }
}

function createDefaultProvider(env: ProviderEnv): LLMProvider<SessionRequest> {
  if (env.providerId !== 'deepseek') {
    throw new Error(`Unsupported provider: ${env.providerId}`)
  }

  return new DeepSeekProvider({
    apiKey: env.apiKey,
    baseURL: resolveDefaultBaseURL(env.providerId, env.baseURL),
    model: env.model,
  })
}

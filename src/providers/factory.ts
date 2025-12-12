import type { ProviderEnv } from '../config/env.js'
import type { LLMProvider, SessionRequest } from '../core/session.js'
import type { ProviderId } from '../core/stream.js'

import { resolveDefaultBaseURL, resolveDefaultModel } from '../config/provider-defaults.js'
import { DeepSeekProvider, type DeepSeekSessionRequest } from './deepseek.js'
import { OpenAIProvider } from './openai.js'

export interface ProviderRequestInput {
  model?: string
  prompt: string
  systemPrompt?: string
  temperature?: number
}

export interface ProviderBinding<Request extends SessionRequest = SessionRequest> {
  createRequest(input: ProviderRequestInput): Request
  provider: LLMProvider<Request>
}

export interface ProviderBindingFactoryOptions {
  env: ProviderEnv
  providerId: ProviderId
}

export type ProviderBindingFactory = (options: ProviderBindingFactoryOptions) => ProviderBinding

export function createProviderBinding(
  options: ProviderBindingFactoryOptions,
): ProviderBinding {
  switch (options.providerId) {
    case 'deepseek': {
      const baseURL = resolveDefaultBaseURL('deepseek', options.env.baseURL)
      const provider = new DeepSeekProvider({
        apiKey: options.env.apiKey,
        baseURL,
        model: options.env.model,
      })

      return {
        createRequest(input) {
          return {
            model: resolveDefaultModel('deepseek', input.model ?? options.env.model),
            prompt: input.prompt,
            providerId: provider.id,
            systemPrompt: input.systemPrompt,
            temperature: input.temperature,
          } satisfies DeepSeekSessionRequest
        },
        provider,
      }
    }

    case 'openai': {
      const provider = new OpenAIProvider({
        apiKey: options.env.apiKey,
        baseURL: options.env.baseURL,
        model: options.env.model,
      })

      return {
        createRequest(input) {
          return {
            model: resolveDefaultModel('openai', input.model ?? options.env.model),
            prompt: input.prompt,
            providerId: provider.id,
            systemPrompt: input.systemPrompt,
            temperature: input.temperature,
          }
        },
        provider,
      }
    }

    default: {
      throw new Error(`Unsupported provider: ${options.providerId}`)
    }
  }
}

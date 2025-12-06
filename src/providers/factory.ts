import type {ProviderEnv} from '../config/env.js'
import type {LLMProvider, SessionRequest} from '../core/session.js'
import type {ProviderId} from '../core/stream.js'

import {AnthropicProvider, type AnthropicSessionRequest} from './anthropic.js'
import {DeepSeekProvider, type DeepSeekSessionRequest} from './deepseek.js'
import {OpenAIAgentsProvider, type OpenAIAgentsSessionRequest} from './openai-agents.js'
import {OpenAIProvider} from './openai.js'

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
    case 'anthropic': {
      const provider = new AnthropicProvider({
        apiKey: options.env.apiKey,
        baseURL: options.env.baseURL,
        model: options.env.model,
      })

      return {
        createRequest(input) {
          return {
            model: input.model ?? options.env.model ?? 'claude-3-5-sonnet-latest',
            prompt: input.prompt,
            providerId: provider.id,
            systemPrompt: input.systemPrompt,
            temperature: input.temperature,
          } satisfies AnthropicSessionRequest
        },
        provider,
      }
    }

    case 'deepseek': {
      const baseURL = options.env.baseURL ?? 'https://api.deepseek.com'
      const provider = new DeepSeekProvider({
        apiKey: options.env.apiKey,
        baseURL,
        model: options.env.model,
      })

      return {
        createRequest(input) {
          return {
            model: input.model ?? options.env.model ?? 'deepseek-chat',
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
            model: input.model ?? options.env.model ?? 'gpt-4o-mini',
            prompt: input.prompt,
            providerId: provider.id,
            systemPrompt: input.systemPrompt,
            temperature: input.temperature,
          }
        },
        provider,
      }
    }

    case 'openai-agents': {
      const provider = new OpenAIAgentsProvider({
        apiKey: options.env.apiKey,
        baseURL: options.env.baseURL,
        instructions: options.env.instructions,
        model: options.env.model,
      })

      return {
        createRequest(input) {
          return {
            model: input.model ?? options.env.model ?? 'gpt-4.1',
            prompt: input.prompt,
            providerId: provider.id,
            systemPrompt: input.systemPrompt,
            temperature: input.temperature,
          } satisfies OpenAIAgentsSessionRequest
        },
        provider,
      }
    }

    default: {
      throw new Error(`Unsupported provider: ${options.providerId}`)
    }
  }
}

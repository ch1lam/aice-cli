import type { LLMProvider } from '../core/session.js'
import type { ProviderDefaults, ProviderEnv } from '../types/env.js'
import type {
  ProviderBinding,
  ProviderBindingFactoryOptions,
  ProviderRequestInput,
} from '../types/provider.js'
import type { ProviderId } from '../types/stream.js'

import { PROVIDER_DEFAULTS, resolveDefaultBaseURL, resolveDefaultModel } from '../config/provider-defaults.js'
import { DeepSeekProvider } from './deepseek.js'

type ProviderConfig = {
  apiKey: string
  baseURL?: string
  model?: string
}

type ProviderClass = new (config: ProviderConfig) => LLMProvider

export interface ProviderRegistryEntry {
  createBinding(options: ProviderBindingFactoryOptions): ProviderBinding
  defaults: ProviderDefaults
  getPingModel(env: ProviderEnv): string
  Provider: ProviderClass
}

function createProviderRegistryEntry(
  providerId: ProviderId,
  Provider: ProviderClass,
): ProviderRegistryEntry {
  const defaults = PROVIDER_DEFAULTS[providerId]

  return {
    createBinding(options) {
      const { env } = options
      const provider = new Provider({
        apiKey: env.apiKey,
        baseURL: resolveDefaultBaseURL(providerId, env.baseURL),
        model: env.model,
      })

      return {
        createRequest(input: ProviderRequestInput) {
          return {
            model: resolveDefaultModel(providerId, input.model ?? env.model),
            prompt: input.prompt,
            providerId: provider.id,
            systemPrompt: input.systemPrompt,
            temperature: input.temperature,
          }
        },
        provider,
      }
    },
    defaults,
    getPingModel(env) {
      return env.model ?? defaults.defaultModel
    },
    Provider,
  }
}

export const providerRegistry: Record<ProviderId, ProviderRegistryEntry> = {
  deepseek: createProviderRegistryEntry('deepseek', DeepSeekProvider),
}

export const providerIds = Object.keys(providerRegistry) as ProviderId[]

const providerIdSet = new Set<ProviderId>(providerIds)

export function isProviderId(value: string): value is ProviderId {
  return providerIdSet.has(value)
}

export function parseProviderId(value: string): ProviderId | undefined {
  return isProviderId(value) ? value : undefined
}

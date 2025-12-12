import type { ProviderId } from '../core/stream.js'

export interface ProviderDefaults {
  defaultBaseURL?: string
  defaultModel: string
  description: string
  label: string
}

export const DEFAULT_PROVIDER_ID: ProviderId = 'openai'

export const PROVIDER_DEFAULTS: Record<ProviderId, ProviderDefaults> = {
  deepseek: {
    defaultBaseURL: 'https://api.deepseek.com',
    defaultModel: 'deepseek-chat',
    description: 'DeepSeek chat + reasoning',
    label: 'DeepSeek',
  },
  openai: {
    defaultModel: 'gpt-4o-mini',
    description: 'Responses API (default)',
    label: 'OpenAI',
  },
}

export function getProviderDefaults(providerId: ProviderId): ProviderDefaults {
  return PROVIDER_DEFAULTS[providerId]
}

export function resolveDefaultModel(providerId: ProviderId, model?: string): string {
  return model ?? PROVIDER_DEFAULTS[providerId].defaultModel
}

export function resolveDefaultBaseURL(
  providerId: ProviderId,
  baseURL?: string,
): string | undefined {
  return baseURL ?? PROVIDER_DEFAULTS[providerId].defaultBaseURL
}


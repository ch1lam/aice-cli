import type { ProviderId } from '../types/stream.ts'

import { ProviderDefaults } from '../types/env.js'

export const DEFAULT_PROVIDER_ID: ProviderId = 'deepseek'

export const PROVIDER_DEFAULTS: Record<ProviderId, ProviderDefaults> = {
  deepseek: {
    defaultBaseURL: 'https://api.deepseek.com',
    defaultModel: 'deepseek-chat',
    description: 'DeepSeek chat + reasoning',
    label: 'DeepSeek',
  },
}

export const providerIds = Object.keys(PROVIDER_DEFAULTS) as ProviderId[]

const providerIdSet = new Set<ProviderId>(providerIds)

export function isProviderId(value: string): value is ProviderId {
  return providerIdSet.has(value)
}

export function parseProviderId(value: string): ProviderId | undefined {
  return isProviderId(value) ? value : undefined
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

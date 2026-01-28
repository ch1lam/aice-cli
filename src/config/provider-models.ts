import type { ProviderModelOption } from '../types/provider-models.js'
import type { ProviderId } from '../types/stream.js'

export const PROVIDER_MODELS: Record<ProviderId, ProviderModelOption[]> = {
  deepseek: [
    {
      description: 'General chat + coding.',
      id: 'deepseek-chat',
      label: 'DeepSeek Chat',
    },
    {
      description: 'Reasoning-heavy responses.',
      id: 'deepseek-reasoner',
      label: 'DeepSeek Reasoner',
    },
  ],
}

export function getProviderModelOptions(providerId: ProviderId): ProviderModelOption[] {
  return PROVIDER_MODELS[providerId] ?? []
}

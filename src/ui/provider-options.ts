import { DEFAULT_PROVIDER_ID, PROVIDER_DEFAULTS } from '../config/provider-defaults.js'
import { KNOWN_PROVIDERS, type ProviderId } from '../core/stream.js'
import { type SelectInputItem } from '../types/select-input.js'

export type ProviderOption = SelectInputItem<ProviderId>

export const providerOptions: ProviderOption[] = KNOWN_PROVIDERS.map(providerId => {
  const defaults = PROVIDER_DEFAULTS[providerId]
  return {
    description: defaults.description,
    label: defaults.label,
    value: providerId,
  }
})

export function providerOptionIndex(providerId: ProviderId): number {
  const index = providerOptions.findIndex(option => option.value === providerId)
  return index === -1 ? 0 : index
}

export function providerIdFromIndex(index: number): ProviderId {
  return providerOptions[index]?.value ?? DEFAULT_PROVIDER_ID
}

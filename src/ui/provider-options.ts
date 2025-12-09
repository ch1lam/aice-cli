import type {ProviderId} from '../core/stream.js'

import {type SelectInputItem} from './select-input.js'

export type ProviderOption = SelectInputItem<ProviderId>

export const providerOptions: ProviderOption[] = [
  {description: 'Responses API (default)', label: 'OpenAI', value: 'openai'},
  {description: 'DeepSeek chat + reasoning', label: 'DeepSeek', value: 'deepseek'},
]

export function providerOptionIndex(providerId: ProviderId): number {
  const index = providerOptions.findIndex(option => option.value === providerId)
  return index === -1 ? 0 : index
}

export function providerIdFromIndex(index: number): ProviderId {
  return providerOptions[index]?.value ?? 'openai'
}

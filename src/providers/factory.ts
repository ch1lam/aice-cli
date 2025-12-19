import type { ProviderBinding, ProviderBindingFactoryOptions } from '../types/provider.js'

import { providerRegistry } from './registry.js'

export function createProviderBinding(
  options: ProviderBindingFactoryOptions,
): ProviderBinding {
  return providerRegistry[options.providerId].createBinding(options)
}

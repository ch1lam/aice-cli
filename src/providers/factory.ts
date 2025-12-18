import type { LLMProvider, SessionRequest } from '../core/session.js'
import type { ProviderId } from '../core/stream.js'
import type { ProviderEnv } from '../types/env.js'

import { providerRegistry } from './registry.js'

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
  return providerRegistry[options.providerId].createBinding(options)
}

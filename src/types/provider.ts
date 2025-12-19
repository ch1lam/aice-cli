import type { LLMProvider, SessionRequest } from '../core/session.js'
import type { ProviderId } from '../core/stream.js'
import type { ProviderEnv } from './env.js'

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

export type ProviderBindingFactory = (
  options: ProviderBindingFactoryOptions,
) => ProviderBinding

import type { ProviderId } from './stream.js'

export interface ProviderEnv {
  apiKey: string
  baseURL?: string
  model?: string
  providerId: ProviderId
}

export interface TryLoadProviderEnvResult {
  env?: ProviderEnv
  error?: Error
}

export interface ProviderDefaults {
  defaultBaseURL?: string
  defaultModel: string
  description: string
  label: string
}

import type { ProviderId } from './stream.js'

export type AppMode = 'chat' | 'setup'

export type SetupStep = 'apiKey' | 'baseURL' | 'model'

export interface SetupState {
  apiKey?: string
  baseURL?: string
  model?: string
  providerId: ProviderId
  step: SetupStep
}

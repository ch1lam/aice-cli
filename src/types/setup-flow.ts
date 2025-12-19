import type { ProviderId } from '../core/stream.js'

export type AppMode = 'chat' | 'setup'

export type SetupStep = 'apiKey' | 'baseURL' | 'model' | 'provider'

export interface SetupState {
  apiKey?: string
  baseURL?: string
  model?: string
  providerId: ProviderId
  step: SetupStep
}

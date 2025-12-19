import type { persistProviderEnv, tryLoadProviderEnv } from '../config/env.js'
import type { pingProvider } from '../providers/ping.js'

export interface SetupServiceOptions {
  persistEnv?: typeof persistProviderEnv
  ping?: typeof pingProvider
  tryLoadEnv?: typeof tryLoadProviderEnv
}

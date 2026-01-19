import type { ProviderEnv, TryLoadProviderEnvResult } from './env.js'
import type { ProviderId } from './stream.js'

type EnvValues = Record<string, string | undefined>

interface EnvIO {
  exists(envPath: string): boolean
  readFile(envPath: string): string
  writeFile(envPath: string, content: string): void
}

interface LoadProviderEnvOptions {
  env?: EnvValues
  envPath?: string
  io?: EnvIO
  providerId?: ProviderId
}

interface PersistProviderEnvOptions extends ProviderEnv {
  env?: EnvValues
  envPath?: string
  io?: EnvIO
}

type PersistProviderEnv = (options: PersistProviderEnvOptions) => EnvValues
type TryLoadProviderEnv = (options?: LoadProviderEnvOptions) => TryLoadProviderEnvResult
type PingProvider = (env: ProviderEnv) => Promise<void>

export interface SetupServiceOptions {
  persistEnv?: PersistProviderEnv
  ping?: PingProvider
  tryLoadEnv?: TryLoadProviderEnv
}

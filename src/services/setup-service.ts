import type { PersistEnvOptions } from '../config/env.js'
import type { ProviderEnv } from '../types/env.js'
import type { SetupServiceOptions } from '../types/setup-service.js'
import type { ProviderId } from '../types/stream.js'

import { persistProviderEnv, tryLoadProviderEnv } from '../config/env.js'
import { toError } from '../core/errors.js'
import { pingProvider } from '../providers/ping.js'

export interface PersistAndLoadOptions extends Omit<PersistEnvOptions, 'env'> {
  env?: PersistEnvOptions['env']
}

export class ProviderEnvPersistError extends Error {
  name = 'ProviderEnvPersistError'
}

export class ProviderEnvLoadError extends Error {
  name = 'ProviderEnvLoadError'
}

export class ProviderNotConfiguredError extends Error {
  name = 'ProviderNotConfiguredError'
  providerId: ProviderId

  constructor(providerId: ProviderId) {
    super(`Provider ${providerId} is not configured`)
    this.providerId = providerId
  }
}

export class SetupService {
  #persistEnv: typeof persistProviderEnv
  #ping: typeof pingProvider
  #tryLoadEnv: typeof tryLoadProviderEnv

  constructor(options: SetupServiceOptions = {}) {
    this.#persistEnv = options.persistEnv ?? persistProviderEnv
    this.#tryLoadEnv = options.tryLoadEnv ?? tryLoadProviderEnv
    this.#ping = options.ping ?? pingProvider
  }

  persistAndLoad(options: PersistAndLoadOptions): ProviderEnv {
    try {
      this.#persistEnv(options)
    } catch (error) {
      throw new ProviderEnvPersistError(toError(error, 'Failed to write .env').message)
    }

    const { env, error } = this.#tryLoadEnv({ providerId: options.providerId })
    if (!env || error) {
      throw new ProviderEnvLoadError(toError(error, 'Unknown error.').message)
    }

    return env
  }

  setModel(env: ProviderEnv, model: string): ProviderEnv {
    try {
      this.#persistEnv({
        apiKey: env.apiKey,
        baseURL: env.baseURL,
        model,
        providerId: env.providerId,
      })
    } catch (error) {
      throw new ProviderEnvPersistError(toError(error, 'Failed to write .env').message)
    }

    return { ...env, model }
  }

  switchProvider(providerId: ProviderId): ProviderEnv {
    const { env, error } = this.#tryLoadEnv({ providerId })
    if (!env || error) {
      throw new ProviderNotConfiguredError(providerId)
    }

    try {
      this.#persistEnv({
        apiKey: env.apiKey,
        baseURL: env.baseURL,
        model: env.model,
        providerId,
      })
    } catch (error) {
      throw new ProviderEnvPersistError(toError(error, 'Failed to write .env').message)
    }

    return env
  }

  async verifyConnectivity(env: ProviderEnv): Promise<void> {
    await this.#ping(env)
  }
}

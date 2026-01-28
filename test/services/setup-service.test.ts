import { expect } from 'chai'

import type { ProviderEnv } from '../../src/types/env.ts'
import type { ProviderId } from '../../src/types/stream.ts'

import {
  ProviderEnvLoadError,
  ProviderEnvPersistError,
  ProviderNotConfiguredError,
  SetupService,
} from '../../src/services/setup-service.ts'

describe('SetupService', () => {
  it('persists and loads provider env', () => {
    const calls: string[] = []
    const loadedEnv: ProviderEnv = { apiKey: 'key', model: 'm', providerId: 'deepseek' }

    const service = new SetupService({
      persistEnv(options) {
        calls.push(`persist:${options.providerId}`)
        return {}
      },
      tryLoadEnv(options) {
        calls.push(`load:${options?.providerId ?? 'default'}`)
        return { env: loadedEnv }
      },
    })

    const env = service.persistAndLoad({
      apiKey: 'key',
      model: 'm',
      providerId: 'deepseek',
    })

    expect(env).to.deep.equal(loadedEnv)
    expect(calls).to.deep.equal(['persist:deepseek', 'load:deepseek'])
  })

  it('wraps persist errors as ProviderEnvPersistError', () => {
    const service = new SetupService({
      persistEnv() {
        throw new Error('nope')
      },
      tryLoadEnv() {
        return { env: { apiKey: 'key', providerId: 'deepseek' } }
      },
    })

    expect(() =>
      service.persistAndLoad({ apiKey: 'key', providerId: 'deepseek' }),
    ).to.throw(ProviderEnvPersistError, 'nope')
  })

  it('wraps load failures as ProviderEnvLoadError', () => {
    const service = new SetupService({
      persistEnv() {
        return {}
      },
      tryLoadEnv() {
        return { error: new Error('boom') }
      },
    })

    expect(() =>
      service.persistAndLoad({ apiKey: 'key', providerId: 'deepseek' }),
    ).to.throw(ProviderEnvLoadError, 'boom')
  })

  it('persists model updates and returns the new env', () => {
    const calls: ProviderEnv[] = []
    const env: ProviderEnv = {
      apiKey: 'key',
      baseURL: 'https://example.com',
      model: 'old-model',
      providerId: 'deepseek',
    }

    const service = new SetupService({
      persistEnv(options) {
        calls.push({
          apiKey: options.apiKey,
          baseURL: options.baseURL,
          model: options.model,
          providerId: options.providerId,
        })
        return {}
      },
    })

    const updated = service.setModel(env, 'new-model')

    expect(updated).to.deep.equal({ ...env, model: 'new-model' })
    expect(calls).to.deep.equal([
      {
        apiKey: 'key',
        baseURL: 'https://example.com',
        model: 'new-model',
        providerId: 'deepseek',
      },
    ])
  })

  it('wraps persist errors when updating the model', () => {
    const env: ProviderEnv = {
      apiKey: 'key',
      providerId: 'deepseek',
    }

    const service = new SetupService({
      persistEnv() {
        throw new Error('write failed')
      },
    })

    expect(() => service.setModel(env, 'new-model')).to.throw(
      ProviderEnvPersistError,
      'write failed',
    )
  })

  it('clears the model override when setModel is called without a model', () => {
    const calls: ProviderEnv[] = []
    const env: ProviderEnv = {
      apiKey: 'key',
      baseURL: 'https://example.com',
      model: 'old-model',
      providerId: 'deepseek',
    }

    const service = new SetupService({
      persistEnv(options) {
        calls.push({
          apiKey: options.apiKey,
          baseURL: options.baseURL,
          model: options.model,
          providerId: options.providerId,
        })
        return {}
      },
    })

    const updated = service.setModel(env)

    expect(updated).to.deep.equal({ ...env, model: undefined })
    expect(calls).to.deep.equal([
      {
        apiKey: 'key',
        baseURL: 'https://example.com',
        model: undefined,
        providerId: 'deepseek',
      },
    ])
  })

  it('verifies connectivity via the ping dependency', async () => {
    const calls: ProviderId[] = []
    const env: ProviderEnv = { apiKey: 'key', providerId: 'deepseek' }

    const service = new SetupService({
      ping(pingEnv) {
        calls.push(pingEnv.providerId)
        return Promise.resolve()
      },
    })

    await service.verifyConnectivity(env)
    expect(calls).to.deep.equal(['deepseek'])
  })

  it('switches providers by loading then persisting selection', () => {
    const calls: string[] = []
    const env: ProviderEnv = {
      apiKey: 'key',
      baseURL: 'https://example.com',
      model: 'm',
      providerId: 'deepseek',
    }

    const service = new SetupService({
      persistEnv(options) {
        calls.push(`persist:${options.providerId}:${options.model ?? '-'}`)
        return {}
      },
      tryLoadEnv(options) {
        calls.push(`load:${options?.providerId ?? 'default'}`)
        return { env }
      },
    })

    const result = service.switchProvider('deepseek')
    expect(result).to.deep.equal(env)
    expect(calls).to.deep.equal(['load:deepseek', 'persist:deepseek:m'])
  })

  it('surfaces missing provider config as ProviderNotConfiguredError', () => {
    const service = new SetupService({
      tryLoadEnv() {
        return { error: new Error('Missing API key') }
      },
    })

    expect(() => service.switchProvider('deepseek')).to.throw(
      ProviderNotConfiguredError,
      'Provider deepseek is not configured',
    )
  })
})

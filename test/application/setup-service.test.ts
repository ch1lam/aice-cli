import { expect } from 'chai'

import type { ProviderEnv } from '../../src/config/env.ts'
import type { ProviderId } from '../../src/core/stream.ts'

import {
  ProviderEnvLoadError,
  ProviderEnvPersistError,
  ProviderNotConfiguredError,
  SetupService,
} from '../../src/application/setup-service.ts'

describe('SetupService', () => {
  it('persists and loads provider env', () => {
    const calls: string[] = []
    const loadedEnv: ProviderEnv = { apiKey: 'key', model: 'm', providerId: 'openai' }

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
      providerId: 'openai',
    })

    expect(env).to.deep.equal(loadedEnv)
    expect(calls).to.deep.equal(['persist:openai', 'load:openai'])
  })

  it('wraps persist errors as ProviderEnvPersistError', () => {
    const service = new SetupService({
      persistEnv() {
        throw new Error('nope')
      },
      tryLoadEnv() {
        return { env: { apiKey: 'key', providerId: 'openai' } }
      },
    })

    expect(() =>
      service.persistAndLoad({ apiKey: 'key', providerId: 'openai' }),
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
      service.persistAndLoad({ apiKey: 'key', providerId: 'openai' }),
    ).to.throw(ProviderEnvLoadError, 'boom')
  })

  it('verifies connectivity via the ping dependency', async () => {
    const calls: ProviderId[] = []
    const env: ProviderEnv = { apiKey: 'key', providerId: 'openai' }

    const service = new SetupService({
      ping(pingEnv) {
        calls.push(pingEnv.providerId)
        return Promise.resolve()
      },
    })

    await service.verifyConnectivity(env)
    expect(calls).to.deep.equal(['openai'])
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

    expect(() => service.switchProvider('openai')).to.throw(
      ProviderNotConfiguredError,
      'Provider openai is not configured',
    )
  })
})


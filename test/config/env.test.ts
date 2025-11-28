import {expect} from 'chai'

import {loadProviderEnv} from '../../src/config/env.ts'

function restoreEnv(snapshot: NodeJS.ProcessEnv): void {
  for (const key of Object.keys(process.env)) {
    if (!(key in snapshot)) {
      delete process.env[key]
    }
  }

  Object.assign(process.env, snapshot)
}

describe('loadProviderEnv', () => {
  const snapshot = {...process.env}

  afterEach(() => {
    restoreEnv(snapshot)
  })

  it('uses AICE_MODEL as an OpenAI-only fallback when provider model is unset', () => {
    process.env.AICE_PROVIDER = 'openai'
    process.env.AICE_OPENAI_API_KEY = 'key'
    process.env.AICE_MODEL = 'gpt-from-env'
    delete process.env.AICE_OPENAI_MODEL

    const env = loadProviderEnv()

    expect(env.providerId).to.equal('openai')
    expect(env.model).to.equal('gpt-from-env')
  })

  it('prefers provider-specific model for Anthropic even when AICE_MODEL is set', () => {
    process.env.AICE_PROVIDER = 'anthropic'
    process.env.AICE_ANTHROPIC_API_KEY = 'anth-key'
    process.env.AICE_MODEL = 'gpt-openai'
    process.env.AICE_ANTHROPIC_MODEL = 'claude-from-env'

    const env = loadProviderEnv()

    expect(env.providerId).to.equal('anthropic')
    expect(env.model).to.equal('claude-from-env')
  })

  it('ignores AICE_MODEL for DeepSeek and leaves model undefined when provider-specific is missing', () => {
    process.env.AICE_PROVIDER = 'deepseek'
    process.env.AICE_DEEPSEEK_API_KEY = 'deep-key'
    process.env.AICE_MODEL = 'gpt-openai'
    delete process.env.AICE_DEEPSEEK_MODEL

    const env = loadProviderEnv()

    expect(env.providerId).to.equal('deepseek')
    expect(env.model).to.equal(undefined)
  })
})

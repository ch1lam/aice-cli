import {expect} from 'chai'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import {persistProviderEnv, tryLoadProviderEnv} from '../../src/config/env.js'

describe('env helpers', () => {
  const keys = [
    'AICE_PROVIDER',
    'AICE_OPENAI_API_KEY',
    'AICE_OPENAI_BASE_URL',
    'AICE_OPENAI_AGENT_MODEL',
    'AICE_OPENAI_MODEL',
  ]

  const originalEnv: Record<string, string | undefined> = {}

  beforeEach(() => {
    for (const key of keys) {
      originalEnv[key] = process.env[key]
    }
  })

  afterEach(() => {
    for (const key of keys) {
      const value = originalEnv[key]
      if (value === undefined) {
        delete process.env[key]
      } else {
        process.env[key] = value
      }
    }
  })

  it('persists provider env and updates process.env', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aice-env-'))
    const envPath = path.join(dir, '.env')

    try {
      persistProviderEnv({
        apiKey: 'test-key',
        envPath,
        model: 'gpt-4o-mini',
        providerId: 'openai',
      })

      const content = fs.readFileSync(envPath, 'utf8')
      expect(content).to.include('AICE_PROVIDER=openai')
      expect(content).to.include('AICE_OPENAI_API_KEY=test-key')
      expect(content).to.include('AICE_OPENAI_MODEL=gpt-4o-mini')

      const {env, error} = tryLoadProviderEnv({providerId: 'openai'})

      expect(error).to.equal(undefined)
      expect(env?.providerId).to.equal('openai')
      expect(env?.apiKey).to.equal('test-key')
      expect(env?.model).to.equal('gpt-4o-mini')
    } finally {
      fs.rmSync(dir, {force: true, recursive: true})
    }
  })

  it('persists OpenAI Agents provider env and updates process.env', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aice-agents-'))
    const envPath = path.join(dir, '.env')

    try {
      persistProviderEnv({
        apiKey: 'agent-key',
        envPath,
        model: 'gpt-4.1',
        providerId: 'openai-agents',
      })

      const content = fs.readFileSync(envPath, 'utf8')
      expect(content).to.include('AICE_PROVIDER=openai-agents')
      expect(content).to.include('AICE_OPENAI_API_KEY=agent-key')
      expect(content).to.include('AICE_OPENAI_AGENT_MODEL=gpt-4.1')

      const {env, error} = tryLoadProviderEnv({providerId: 'openai-agents'})

      expect(error).to.equal(undefined)
      expect(env?.providerId).to.equal('openai-agents')
      expect(env?.apiKey).to.equal('agent-key')
      expect(env?.model).to.equal('gpt-4.1')
    } finally {
      fs.rmSync(dir, {force: true, recursive: true})
    }
  })
})

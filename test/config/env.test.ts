import { expect } from 'chai'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { persistProviderEnv, tryLoadProviderEnv } from '../../src/config/env.js'

describe('env helpers', () => {
  const keys = [
    'AICE_PROVIDER',
    'AICE_OPENAI_API_KEY',
    'AICE_OPENAI_BASE_URL',
    'AICE_OPENAI_MODEL',
    'AICE_MODEL',
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

  it('persists provider env without mutating process.env', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aice-env-'))
    const envPath = path.join(dir, '.env')
    process.env.AICE_PROVIDER = 'keep-existing'

    try {
      persistProviderEnv({
        apiKey: 'test-key',
        baseURL: 'https://example.com',
        envPath,
        model: 'gpt-4o-mini',
        providerId: 'openai',
      })

      const content = fs.readFileSync(envPath, 'utf8')
      expect(content).to.include('AICE_PROVIDER=openai')
      expect(content).to.include('AICE_OPENAI_API_KEY=test-key')
      expect(content).to.include('AICE_OPENAI_BASE_URL=https://example.com')
      expect(content).to.include('AICE_OPENAI_MODEL=gpt-4o-mini')

      const { env, error } = tryLoadProviderEnv({ envPath, providerId: 'openai' })

      expect(error).to.equal(undefined)
      expect(env?.providerId).to.equal('openai')
      expect(env?.apiKey).to.equal('test-key')
      expect(env?.baseURL).to.equal('https://example.com')
      expect(env?.model).to.equal('gpt-4o-mini')
      expect(process.env.AICE_PROVIDER).to.equal('keep-existing')
    } finally {
      fs.rmSync(dir, { force: true, recursive: true })
    }
  })

  it('throws when env write fails', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aice-write-'))
    const envPath = path.join(dir, '.env')
    const failingIO = {
      exists() {
        return false
      },
      readFile() {
        return ''
      },
      writeFile() {
        throw new Error('write failure')
      },
    }

    try {
      expect(() =>
        persistProviderEnv({
          apiKey: 'test-key',
          envPath,
          io: failingIO,
          providerId: 'openai',
        }),
      ).to.throw('write failure')
    } finally {
      fs.rmSync(dir, { force: true, recursive: true })
    }
  })

  it('surfaces env read errors when loading', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aice-read-'))
    const envPath = path.join(dir, '.env')
    const failingIO = {
      exists() {
        return true
      },
      readFile() {
        throw new Error('read failure')
      },
      writeFile() {
        throw new Error('write should not be called')
      },
    }

    try {
      const { env, error } = tryLoadProviderEnv({ envPath, io: failingIO })
      expect(env).to.equal(undefined)
      expect(error?.message).to.equal('read failure')
    } finally {
      fs.rmSync(dir, { force: true, recursive: true })
    }
  })

  it('rejects unknown provider ids from env', () => {
    const { env, error } = tryLoadProviderEnv({
      env: { AICE_PROVIDER: 'unknown' },
    })

    expect(env).to.equal(undefined)
    expect(error?.message).to.include('Unsupported provider: unknown')
  })
})

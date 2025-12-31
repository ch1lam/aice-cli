import { expect } from 'chai'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { persistProviderEnv, tryLoadProviderEnv } from '../../src/config/env.js'

describe('env helpers', () => {
  const keys = [
    'DEEPSEEK_API_KEY',
    'DEEPSEEK_BASE_URL',
    'DEEPSEEK_MODEL',
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
    process.env.DEEPSEEK_API_KEY = 'keep-existing'

    try {
      fs.writeFileSync(
        envPath,
        ['AICE_PROVIDER=openai', 'AICE_OPENAI_API_KEY=legacy', 'OTHER_VAR=keep'].join('\n'),
      )

      persistProviderEnv({
        apiKey: 'test-key',
        baseURL: 'https://example.com',
        envPath,
        model: 'deepseek-chat',
        providerId: 'deepseek',
      })

      const content = fs.readFileSync(envPath, 'utf8')
      expect(content).to.include('DEEPSEEK_API_KEY=test-key')
      expect(content).to.include('DEEPSEEK_BASE_URL=https://example.com')
      expect(content).to.include('DEEPSEEK_MODEL=deepseek-chat')
      expect(content).to.include('OTHER_VAR=keep')
      expect(content).to.not.include('AICE_PROVIDER=')
      expect(content).to.not.include('AICE_OPENAI_API_KEY=')

      const { env, error } = tryLoadProviderEnv({ envPath, providerId: 'deepseek' })

      expect(error).to.equal(undefined)
      expect(env?.providerId).to.equal('deepseek')
      expect(env?.apiKey).to.equal('test-key')
      expect(env?.baseURL).to.equal('https://example.com')
      expect(env?.model).to.equal('deepseek-chat')
      expect(process.env.DEEPSEEK_API_KEY).to.equal('keep-existing')
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
          providerId: 'deepseek',
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

  it('errors when required values are missing', () => {
    const { env, error } = tryLoadProviderEnv({ env: {}, providerId: 'deepseek' })

    expect(env).to.equal(undefined)
    expect(error?.message).to.equal('Missing DEEPSEEK_API_KEY')
  })

  it('removes optional keys when clearing values', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aice-clear-'))
    const envPath = path.join(dir, '.env')

    try {
      fs.writeFileSync(
        envPath,
        [
          'DEEPSEEK_API_KEY=old-key',
          'DEEPSEEK_BASE_URL=https://example.com',
          'DEEPSEEK_MODEL=old-model',
          'OTHER_VAR=keep',
        ].join('\n'),
      )

      persistProviderEnv({
        apiKey: 'new-key',
        envPath,
        providerId: 'deepseek',
      })

      const content = fs.readFileSync(envPath, 'utf8')
      expect(content).to.include('DEEPSEEK_API_KEY=new-key')
      expect(content).to.include('OTHER_VAR=keep')
      expect(content).to.not.include('DEEPSEEK_BASE_URL=')
      expect(content).to.not.include('DEEPSEEK_MODEL=')
    } finally {
      fs.rmSync(dir, { force: true, recursive: true })
    }
  })

  it('rejects unknown provider ids', () => {
    const { env, error } = tryLoadProviderEnv({
      providerId: 'unknown',
    })

    expect(env).to.equal(undefined)
    expect(error?.message).to.include('Unsupported provider: unknown')
  })
})

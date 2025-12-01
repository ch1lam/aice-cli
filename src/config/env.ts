import dotenv from 'dotenv'
import fs from 'node:fs'
import path from 'node:path'

import type {ProviderId} from '../core/stream.js'

dotenv.config()

export interface ProviderEnv {
  apiKey: string
  baseURL?: string
  model?: string
  providerId: ProviderId
}

export interface LoadProviderEnvOptions {
  providerId?: ProviderId
}

export function loadProviderEnv(options?: LoadProviderEnvOptions): ProviderEnv {
  const providerId = (options?.providerId ?? process.env.AICE_PROVIDER ?? 'openai') as ProviderId

  switch (providerId) {
    case 'anthropic': {
      const apiKey = process.env.AICE_ANTHROPIC_API_KEY

      if (!apiKey) {
        throw new Error('Missing AICE_ANTHROPIC_API_KEY')
      }

      return {
        apiKey,
        baseURL: process.env.AICE_ANTHROPIC_BASE_URL,
        model: process.env.AICE_ANTHROPIC_MODEL,
        providerId,
      }
    }

    case 'deepseek': {
      const apiKey = process.env.AICE_DEEPSEEK_API_KEY

      if (!apiKey) {
        throw new Error('Missing AICE_DEEPSEEK_API_KEY')
      }

      return {
        apiKey,
        baseURL: process.env.AICE_DEEPSEEK_BASE_URL ?? process.env.AICE_OPENAI_BASE_URL,
        model: process.env.AICE_DEEPSEEK_MODEL,
        providerId,
      }
    }

    case 'openai': {
      const apiKey = process.env.AICE_OPENAI_API_KEY

      if (!apiKey) {
        throw new Error('Missing AICE_OPENAI_API_KEY')
      }

      return {
        apiKey,
        baseURL: process.env.AICE_OPENAI_BASE_URL,
        model: process.env.AICE_OPENAI_MODEL ?? process.env.AICE_MODEL,
        providerId,
      }
    }

    default: {
      throw new Error(`Unsupported provider: ${providerId}`)
    }
  }
}

export interface ProviderCredentials {
  apiKey: string
  baseURL?: string
  model?: string
  providerId: ProviderId
}

export interface PersistEnvOptions extends ProviderCredentials {
  envPath?: string
}

export interface TryLoadProviderEnvResult {
  env?: ProviderEnv
  error?: Error
}

export function tryLoadProviderEnv(options?: LoadProviderEnvOptions): TryLoadProviderEnvResult {
  try {
    return {env: loadProviderEnv(options)}
  } catch (error) {
    return {error: error instanceof Error ? error : new Error(String(error))}
  }
}

export function persistProviderEnv(options: PersistEnvOptions): void {
  const envPath = options.envPath ?? path.resolve(process.cwd(), '.env')
  const envMap = readEnvFile(envPath)
  const entries = buildEnvEntries(options)

  envMap.set('AICE_PROVIDER', options.providerId)
  process.env.AICE_PROVIDER = options.providerId

  for (const [key, value] of Object.entries(entries)) {
    if (value === undefined) {
      envMap.delete(key)
      delete process.env[key]
      continue
    }

    envMap.set(key, value)
    process.env[key] = value
  }

  const content = [...envMap.entries()].map(([key, value]) => `${key}=${value}`).join('\n')

  fs.writeFileSync(envPath, `${content}\n`, 'utf8')
}

function buildEnvEntries(options: ProviderCredentials): Record<string, string | undefined> {
  switch (options.providerId) {
    case 'anthropic': {
      return {
        AICE_ANTHROPIC_API_KEY: options.apiKey,
        AICE_ANTHROPIC_BASE_URL: options.baseURL,
        AICE_ANTHROPIC_MODEL: options.model,
      }
    }

    case 'deepseek': {
      return {
        AICE_DEEPSEEK_API_KEY: options.apiKey,
        AICE_DEEPSEEK_BASE_URL: options.baseURL,
        AICE_DEEPSEEK_MODEL: options.model,
      }
    }

    case 'openai': {
      return {
        AICE_MODEL: undefined,
        AICE_OPENAI_API_KEY: options.apiKey,
        AICE_OPENAI_BASE_URL: options.baseURL,
        AICE_OPENAI_MODEL: options.model,
      }
    }

    default: {
      throw new Error(`Unsupported provider: ${options.providerId}`)
    }
  }
}

function readEnvFile(envPath: string): Map<string, string> {
  if (!fs.existsSync(envPath)) {
    return new Map()
  }

  const content = fs.readFileSync(envPath, 'utf8')
  const lines = content.split('\n')
  const envMap = new Map<string, string>()

  for (const line of lines) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) continue

    const separatorIndex = trimmed.indexOf('=')
    if (separatorIndex === -1) continue

    const key = trimmed.slice(0, separatorIndex)
    const value = trimmed.slice(separatorIndex + 1)

    envMap.set(key, value)
  }

  return envMap
}

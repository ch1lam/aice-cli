import dotenv from 'dotenv'
import fs from 'node:fs'
import path from 'node:path'

import { KNOWN_PROVIDERS, parseProviderId, type ProviderId } from '../core/stream.js'
import { DEFAULT_PROVIDER_ID } from './provider-defaults.js'

dotenv.config({ quiet: true })

export type EnvValues = Record<string, string | undefined>

export interface ProviderEnv {
  apiKey: string
  baseURL?: string
  model?: string
  providerId: ProviderId
}

export type ProviderCredentials = ProviderEnv

export interface LoadProviderEnvOptions {
  env?: EnvValues
  envPath?: string
  io?: EnvIO
  providerId?: ProviderId
}

export interface PersistEnvOptions extends ProviderCredentials {
  env?: EnvValues
  envPath?: string
  io?: EnvIO
}

export interface TryLoadProviderEnvResult {
  env?: ProviderEnv
  error?: Error
}

export interface EnvIO {
  exists(envPath: string): boolean
  readFile(envPath: string): string
  writeFile(envPath: string, content: string): void
}

const DEFAULT_ENV_PATH = path.resolve(process.cwd(), '.env')

const fileEnvIO: EnvIO = {
  exists: envPath => fs.existsSync(envPath),
  readFile: envPath => fs.readFileSync(envPath, 'utf8'),
  writeFile: (envPath, content) => fs.writeFileSync(envPath, content, 'utf8'),
}

export function loadProviderEnv(options?: LoadProviderEnvOptions): ProviderEnv {
  const envValues = resolveEnvValues(options)
  const providerId = resolveProviderId(envValues, options?.providerId)

  switch (providerId) {
    case 'deepseek': {
      return buildDeepseekEnv(envValues, providerId)
    }

    case 'openai': {
      return buildOpenAIEnv(envValues, providerId)
    }

    default: {
      throw new Error(`Unsupported provider: ${providerId}`)
    }
  }
}

export function tryLoadProviderEnv(options?: LoadProviderEnvOptions): TryLoadProviderEnvResult {
  try {
    return { env: loadProviderEnv(options) }
  } catch (error) {
    return { error: error instanceof Error ? error : new Error(String(error)) }
  }
}

export function persistProviderEnv(options: PersistEnvOptions): EnvValues {
  const envPath = options.envPath ?? DEFAULT_ENV_PATH
  const io = options.io ?? fileEnvIO
  const envMap = mapFromEnvValues(options.env ?? readEnvFile(envPath, io))
  const entries = buildEnvEntries(options)

  envMap.set('AICE_PROVIDER', options.providerId)

  for (const [key, value] of Object.entries(entries)) {
    if (value === undefined) {
      envMap.delete(key)
      continue
    }

    envMap.set(key, value)
  }

  const content = [...envMap.entries()].map(([key, value]) => `${key}=${value}`).join('\n')

  io.writeFile(envPath, `${content}\n`)

  return mapToEnvValues(envMap)
}

function buildEnvEntries(options: ProviderCredentials): Record<string, string | undefined> {
  switch (options.providerId) {
    case 'deepseek': {
      return {
        AICE_DEEPSEEK_API_KEY: options.apiKey,
        AICE_DEEPSEEK_BASE_URL: options.baseURL,
        AICE_DEEPSEEK_MODEL: options.model,
      }
    }

    case 'openai': {
      return {
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

function buildDeepseekEnv(env: EnvValues, providerId: ProviderId): ProviderEnv {
  return {
    apiKey: requireEnvValue(env, 'AICE_DEEPSEEK_API_KEY'),
    baseURL: env.AICE_DEEPSEEK_BASE_URL ?? env.AICE_OPENAI_BASE_URL,
    model: env.AICE_DEEPSEEK_MODEL,
    providerId,
  }
}

function buildOpenAIEnv(env: EnvValues, providerId: ProviderId): ProviderEnv {
  return {
    apiKey: requireEnvValue(env, 'AICE_OPENAI_API_KEY'),
    baseURL: env.AICE_OPENAI_BASE_URL,
    model: env.AICE_OPENAI_MODEL ?? env.AICE_MODEL,
    providerId,
  }
}

function mapFromEnvValues(env: EnvValues): Map<string, string> {
  const envMap = new Map<string, string>()

  for (const [key, value] of Object.entries(env)) {
    if (value === undefined) continue
    envMap.set(key, value)
  }

  return envMap
}

function mapToEnvValues(envMap: Map<string, string>): EnvValues {
  const values: EnvValues = {}

  for (const [key, value] of envMap.entries()) {
    values[key] = value
  }

  return values
}

function readEnvFile(envPath: string, io: EnvIO): EnvValues {
  if (!io.exists(envPath)) {
    return {}
  }

  const content = io.readFile(envPath)
  const lines = content.split('\n')
  const envMap: EnvValues = {}

  for (const line of lines) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) continue

    const separatorIndex = trimmed.indexOf('=')
    if (separatorIndex === -1) continue

    const key = trimmed.slice(0, separatorIndex)
    const value = trimmed.slice(separatorIndex + 1)

    envMap[key] = value
  }

  return envMap
}

function resolveEnvValues(options?: LoadProviderEnvOptions): EnvValues {
  if (options?.env) {
    return { ...options.env }
  }

  const envPath = options?.envPath ?? DEFAULT_ENV_PATH
  const io = options?.io ?? fileEnvIO
  const envFromFile = readEnvFile(envPath, io)

  return { ...process.env, ...envFromFile }
}

function resolveProviderId(env: EnvValues, providerId?: ProviderId): ProviderId {
  if (providerId) return providerId

  const rawProviderId = env.AICE_PROVIDER
  if (!rawProviderId) return DEFAULT_PROVIDER_ID

  const parsedProviderId = parseProviderId(rawProviderId)
  if (parsedProviderId) return parsedProviderId

  throw new Error(
    `Unsupported provider: ${rawProviderId}. Supported providers: ${KNOWN_PROVIDERS.join(', ')}`,
  )
}

function requireEnvValue(env: EnvValues, key: string): string {
  const value = env[key]

  if (!value) {
    throw new Error(`Missing ${key}`)
  }

  return value
}

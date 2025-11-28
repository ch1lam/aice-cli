import dotenv from 'dotenv'

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

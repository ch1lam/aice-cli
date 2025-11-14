import dotenv from 'dotenv'

dotenv.config()

export interface ProviderEnv {
  apiKey: string
  baseURL?: string
  model?: string
  providerId: 'openai'
}

export function loadProviderEnv(): ProviderEnv {
  const providerId = (process.env.AICE_PROVIDER ?? 'openai') as ProviderEnv['providerId']

  if (providerId !== 'openai') {
    throw new Error(`Unsupported provider for MVP: ${providerId}`)
  }

  const apiKey = process.env.AICE_OPENAI_API_KEY

  if (!apiKey) {
    throw new Error('Missing AICE_OPENAI_API_KEY')
  }

  return {
    apiKey,
    baseURL: process.env.AICE_OPENAI_BASE_URL,
    model: process.env.AICE_MODEL,
    providerId,
  }
}

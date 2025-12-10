import { OpenAI } from 'openai'

import type { ProviderEnv } from '../config/env.js'

const DEFAULT_TIMEOUT_MS = 8000

type OpenAIModelsClient = {
  list?: (params?: {limit?: number; signal?: AbortSignal}) => Promise<unknown>
  retrieve?: (model: string, options?: {signal?: AbortSignal}) => Promise<unknown>
}

type OpenAIClient = {
  models?: OpenAIModelsClient
}

export interface ProviderPingClients {
  deepseek?: OpenAIClient
  openai?: OpenAIClient
}

export interface ProviderPingOptions {
  clients?: ProviderPingClients
  timeoutMs?: number
}

export async function pingProvider(
  env: ProviderEnv,
  options: ProviderPingOptions = {},
): Promise<void> {
  const { clients = {}, timeoutMs = DEFAULT_TIMEOUT_MS } = options

  switch (env.providerId) {
    case 'deepseek': {
      await pingOpenAI(
        env,
        clients.deepseek,
        env.model ?? 'deepseek-chat',
        { defaultBaseURL: 'https://api.deepseek.com', timeoutMs },
      )
      return
    }

    case 'openai': {
      await pingOpenAI(env, clients.openai, env.model ?? 'gpt-4o-mini', { timeoutMs })
      return
    }

    default: {
      throw new Error(`Unsupported provider ${env.providerId}`)
    }
  }
}

async function pingOpenAI(
  env: ProviderEnv,
  client: OpenAIClient | undefined,
  model: string,
  options?: {defaultBaseURL?: string; timeoutMs?: number},
): Promise<void> {
  const { defaultBaseURL, timeoutMs = DEFAULT_TIMEOUT_MS } = options ?? {}
  const { models } =
    client ?? new OpenAI({ apiKey: env.apiKey, baseURL: env.baseURL ?? defaultBaseURL })

  if (!models?.retrieve && !models?.list) {
    throw new Error('OpenAI client is missing model operations')
  }

  await withAbortTimeout(
    signal => {
      if (models?.retrieve) {
        return models.retrieve(model, { signal })
      }

      return models?.list?.({ limit: 1, signal })
    },
    timeoutMs,
  )
}

async function withAbortTimeout<T>(
  operation: (signal?: AbortSignal) => Promise<T> | undefined,
  timeoutMs: number,
): Promise<T> {
  const controller = new AbortController()
  const timeoutError = new Error('Connectivity check timed out')

  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      controller.abort(timeoutError)
      reject(timeoutError)
    }, timeoutMs)

    Promise.resolve()
      .then(() => operation(controller.signal))
      .then(result => {
        clearTimeout(timer)
        resolve(result as T)
      })
      .catch(error => {
        clearTimeout(timer)

        if (controller.signal.aborted) {
          reject(timeoutError)
          return
        }

        reject(error)
      })
  })
}

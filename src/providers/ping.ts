import { createDeepSeek, type DeepSeekProvider as DeepSeekModelProvider } from '@ai-sdk/deepseek'
import { type LanguageModel, streamText } from 'ai'

import type { ProviderEnv } from '../types/env.js'

import { isProviderId, resolveDefaultBaseURL, resolveDefaultModel } from '../config/provider-defaults.js'

const DEFAULT_TIMEOUT_MS = 8000
const PING_PROMPT = 'ping'
const PING_MAX_TOKENS = 1

type StreamTextResult = {
  consumeStream: (options?: { onError?: (error: unknown) => void }) => PromiseLike<void>
}

type StreamTextFn = (options: {
  abortSignal?: AbortSignal
  maxOutputTokens?: number
  maxRetries?: number
  model: LanguageModel
  prompt: string
}) => StreamTextResult

export type ProviderPingClients = {
  deepseek?: DeepSeekModelProvider
}

export interface ProviderPingOptions {
  clients?: ProviderPingClients
  streamText?: StreamTextFn
  timeoutMs?: number
}

export async function pingProvider(
  env: ProviderEnv,
  options: ProviderPingOptions = {},
): Promise<void> {
  if (!isProviderId(env.providerId)) {
    throw new Error(`Unsupported provider: ${env.providerId}`)
  }

  const {
    clients = {},
    streamText: streamTextFn = streamText,
    timeoutMs = DEFAULT_TIMEOUT_MS,
  } = options
  const modelId = resolveDefaultModel(env.providerId, env.model)
  const baseURL = resolveDefaultBaseURL(env.providerId, env.baseURL)

  const provider = resolveProvider(env.providerId, clients, env.apiKey, baseURL)
  const model = provider(modelId)

  await withAbortTimeout(async signal => {
    const result = streamTextFn({
      abortSignal: signal,
      maxOutputTokens: PING_MAX_TOKENS,
      maxRetries: 0,
      model,
      prompt: PING_PROMPT,
    })

    await result.consumeStream()
  }, timeoutMs)
}

function resolveProvider(
  providerId: string,
  clients: ProviderPingClients,
  apiKey: string,
  baseURL?: string,
): DeepSeekModelProvider {
  if (providerId !== 'deepseek') {
    throw new Error(`Unsupported provider: ${providerId}`)
  }

  return clients.deepseek ?? createDeepSeek({ apiKey, baseURL })
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

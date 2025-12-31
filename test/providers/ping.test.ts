import type { DeepSeekProvider as DeepSeekModelProvider } from '@ai-sdk/deepseek'
import type { LanguageModel } from 'ai'

import { expect } from 'chai'

import { pingProvider } from '../../src/providers/ping.ts'

type StreamTextResult = {
  consumeStream: () => PromiseLike<void>
}

function createProvider(calls: string[]): DeepSeekModelProvider {
  return ((modelId: string) => {
    calls.push(modelId)
    return { modelId } as LanguageModel
  }) as DeepSeekModelProvider
}

function createStreamText(consumeStream: () => PromiseLike<void>): () => StreamTextResult {
  return () => ({ consumeStream })
}

describe('pingProvider', () => {
  it('pings DeepSeek using the default model when none is provided', async () => {
    const calls: string[] = []
    const provider = createProvider(calls)

    await pingProvider(
      { apiKey: 'key', providerId: 'deepseek' },
      { clients: { deepseek: provider }, streamText: createStreamText(async () => {}) },
    )

    expect(calls).to.deep.equal(['deepseek-chat'])
  })

  it('rejects unsupported providers', async () => {
    let error: unknown

    try {
      await pingProvider({ apiKey: 'key', providerId: 'unknown' })
    } catch (error_) {
      error = error_
    }

    expect(error).to.be.instanceOf(Error)
    expect((error as Error).message).to.equal('Unsupported provider: unknown')
  })

  it('surfaces connectivity failures', async () => {
    const calls: string[] = []
    const provider = createProvider(calls)
    let error: unknown

    try {
      await pingProvider(
        { apiKey: 'bad', providerId: 'deepseek' },
        {
          clients: { deepseek: provider },
          streamText: createStreamText(async () => {
            throw new Error('bad auth')
          }),
        },
      )
    } catch (error_) {
      error = error_
    }

    expect(error).to.be.instanceOf(Error)
    expect((error as Error).message).to.equal('bad auth')
  })

  it('times out when the provider call hangs', async () => {
    const calls: string[] = []
    const provider = createProvider(calls)
    let error: unknown

    try {
      await pingProvider(
        { apiKey: 'key', providerId: 'deepseek' },
        {
          clients: { deepseek: provider },
          streamText: createStreamText(() => new Promise(() => {})),
          timeoutMs: 10,
        },
      )
    } catch (error_) {
      error = error_
    }

    expect(error).to.be.instanceOf(Error)
    expect((error as Error).message).to.equal('Connectivity check timed out')
  })
})

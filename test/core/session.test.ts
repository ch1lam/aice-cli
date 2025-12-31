import { expect } from 'chai'

import type { ProviderStreamChunk } from '../../src/types/stream.ts'

import { type LLMProvider, runSession, type SessionRequest } from '../../src/core/session.ts'
import { createUsage } from '../helpers/usage.ts'

function createProvider(chunks: ProviderStreamChunk[]): LLMProvider {
  return {
    id: 'deepseek',
    async *stream() {
      for (const chunk of chunks) {
        yield chunk
      }
    },
  }
}

describe('runSession', () => {
  it('emits meta first and forwards provider chunks', async () => {
    const provider = createProvider([
      { type: 'start' },
      { id: 'text-0', text: 'Hello', type: 'text-delta' },
      { id: 'text-0', text: ' world', type: 'text-delta' },
      {
        finishReason: 'stop',
        rawFinishReason: 'stop',
        totalUsage: createUsage({ inputTokens: 1, outputTokens: 2, totalTokens: 3 }),
        type: 'finish',
      },
    ])

    const request: SessionRequest = {
      model: 'deepseek-chat',
      prompt: 'Hi',
      providerId: 'deepseek',
    }

    const seen: string[] = []

    for await (const chunk of runSession(provider, request)) {
      seen.push(chunk.type)
    }

    expect(seen).to.deep.equal(['meta', 'start', 'text-delta', 'text-delta', 'finish'])
  })

  it('throws when provider IDs mismatch the request', async () => {
    const provider = createProvider([
      { id: 'text-0', text: 'should not stream', type: 'text-delta' },
    ])

    const request: SessionRequest = {
      model: 'deepseek-chat',
      prompt: 'Hi',
      providerId: 'other',
    }

    const stream = runSession(provider, request)
    let error: unknown

    try {
      await stream.next()
    } catch (error_) {
      error = error_
    }

    expect(error).to.be.instanceOf(Error)
    expect((error as Error).message).to.equal('Provider mismatch: expected other, got deepseek')
  })

  it('passes through error chunks from the provider', async () => {
    const provider = createProvider([
      { id: 'text-0', text: 'partial', type: 'text-delta' },
      { error: new Error('boom'), type: 'error' },
      { id: 'text-0', text: 'still-here', type: 'text-delta' },
    ])

    const request: SessionRequest = {
      model: 'deepseek-chat',
      prompt: 'trigger error',
      providerId: 'deepseek',
    }

    const seen: string[] = []

    for await (const chunk of runSession(provider, request)) {
      seen.push(chunk.type)
    }

    expect(seen).to.deep.equal(['meta', 'text-delta', 'error', 'text-delta'])
  })
})

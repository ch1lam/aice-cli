import { expect } from 'chai'

import type { ProviderStreamChunk } from '../../src/types/stream.ts'

import { type LLMProvider, runSession, type SessionRequest } from '../../src/core/session.ts'

function createProvider(chunks: ProviderStreamChunk[]): LLMProvider {
  return {
    id: 'openai',
    async *stream() {
      for (const chunk of chunks) {
        yield chunk
      }
    },
  }
}

describe('runSession', () => {
  it('emits meta first, indexes tokens, and ends with done', async () => {
    const provider = createProvider([
      { status: 'running', type: 'status' },
      { text: 'Hello', type: 'text' },
      { text: ' world', type: 'text' },
      { type: 'usage', usage: { outputTokens: 2 } },
    ])

    const request: SessionRequest = {
      model: 'gpt-4o-mini',
      prompt: 'Hi',
      providerId: 'openai',
    }

    const seen: string[] = []
    const textIndexes: number[] = []

    for await (const chunk of runSession(provider, request)) {
      seen.push(chunk.type)
      if (chunk.type === 'text') {
        textIndexes.push(chunk.index)
      }
    }

    expect(seen).to.deep.equal(['meta', 'status', 'text', 'text', 'usage', 'done'])
    expect(textIndexes).to.deep.equal([0, 1])
  })

  it('throws when provider IDs mismatch the request', async () => {
    const provider = createProvider([{ text: 'should not stream', type: 'text' }])

    const request: SessionRequest = {
      model: 'gpt-4o-mini',
      prompt: 'Hi',
      providerId: 'deepseek',
    }

    const stream = runSession(provider, request)
    let error: unknown

    try {
      await stream.next()
    } catch (error_) {
      error = error_
    }

    expect(error).to.be.instanceOf(Error)
    expect((error as Error).message).to.equal('Provider mismatch: expected deepseek, got openai')
  })

  it('stops streaming after an error chunk and omits done', async () => {
    const provider = createProvider([
      { text: 'partial', type: 'text' },
      { error: new Error('boom'), type: 'error' },
      { text: 'should not appear', type: 'text' },
    ])

    const request: SessionRequest = {
      model: 'gpt-4o-mini',
      prompt: 'trigger error',
      providerId: 'openai',
    }

    const seen: string[] = []

    for await (const chunk of runSession(provider, request)) {
      seen.push(chunk.type)
    }

    expect(seen).to.deep.equal(['meta', 'text', 'error'])
  })
})

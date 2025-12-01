import {expect} from 'chai'

import type {SessionStreamChunk} from '../../src/core/stream.ts'

import {renderStream} from '../../src/chat/chat-runner.js'

async function* chunkStream(chunks: SessionStreamChunk[]) {
  for (const chunk of chunks) {
    yield chunk
  }
}

describe('renderStream helper', () => {
  it('renders meta, text, usage, and status chunks to IO', async () => {
    const stream = chunkStream([
      {model: 'gpt-4o-mini', providerId: 'openai', type: 'meta'},
      {index: 0, text: 'Hello', type: 'text'},
      {index: 1, text: ' world', type: 'text'},
      {type: 'usage', usage: {outputTokens: 2}},
      {detail: 'finished', status: 'completed', type: 'status'},
      {type: 'done'},
    ])

    const logs: string[] = []
    let output = ''

    await renderStream(stream, {
      log(message) {
        logs.push(message)
      },
      write(text) {
        output += text
      },
    })

    expect(logs[0]).to.equal('provider=openai model=gpt-4o-mini')
    expect(output).to.equal('Hello world')
    expect(logs.find(message => message.includes('usage'))).to.exist
    expect(logs.at(-2)).to.equal('status=completed detail=finished')
    expect(logs.at(-1)).to.equal('')
  })

  it('delegates error chunks to io.error', async () => {
    const stream = chunkStream([
      {model: 'gpt-4o-mini', providerId: 'openai', type: 'meta'},
      {index: 0, text: 'partial', type: 'text'},
      {error: new Error('boom'), type: 'error'},
    ])

    const errors: string[] = []

    await renderStream(stream, {
      error(error) {
        errors.push(error.message)
      },
      log() {},
      write() {},
    })

    expect(errors).to.deep.equal(['boom'])
  })
})

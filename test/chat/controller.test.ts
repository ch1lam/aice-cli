import {expect} from 'chai'

import type {LLMProvider, SessionRequest} from '../../src/core/session.ts'

import {ChatController} from '../../src/chat/controller.ts'

function createProvider(): LLMProvider {
  return {
    id: 'openai',
    async *stream() {
      yield {text: 'hi', type: 'text'}
    },
  }
}

describe('ChatController', () => {
  it('creates session streams through the binding factory', async () => {
    const provider = createProvider()
    const inputs: unknown[] = []
    let capturedRequest: SessionRequest | undefined

    const controller = new ChatController({
      bindingFactory() {
        return {
          createRequest(input) {
            inputs.push(input)
            capturedRequest = {
              model: 'test-model',
              prompt: input.prompt,
              providerId: provider.id,
              systemPrompt: input.systemPrompt,
            }
            return capturedRequest
          },
          provider,
        }
      },
      env: {
        apiKey: 'key',
        providerId: 'openai',
      },
    })

    const stream = controller.createStream({
      prompt: 'Hello',
      systemPrompt: 'You are helpful',
      temperature: 1,
    })

    const types: string[] = []
    for await (const chunk of stream) {
      types.push(chunk.type)
    }

    expect(inputs).to.have.lengthOf(1)
    expect(inputs[0]).to.include({prompt: 'Hello', systemPrompt: 'You are helpful', temperature: 1})
    expect(capturedRequest).to.exist
    expect(types).to.deep.equal(['meta', 'text', 'done'])
  })

  it('throws when provider IDs do not match the environment', () => {
    const controller = new ChatController({
      bindingFactory() {
        throw new Error('should not be called')
      },
      env: {
        apiKey: 'key',
        providerId: 'openai',
      },
    })

    expect(() => controller.createStream({prompt: 'hi', providerId: 'anthropic'})).to.throw(
      'Configured provider openai does not match requested anthropic',
    )
  })
})

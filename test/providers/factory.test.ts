import {expect} from 'chai'

import {createProviderBinding} from '../../src/providers/factory.ts'

describe('createProviderBinding', () => {
  it('builds an OpenAI binding with a default model fallback', () => {
    const binding = createProviderBinding({
      env: {apiKey: 'key', providerId: 'openai'},
      providerId: 'openai',
    })

    const request = binding.createRequest({prompt: 'Hello'})

    expect(request).to.include({prompt: 'Hello', providerId: 'openai'})
    expect(request.model).to.equal('gpt-4o-mini')
  })

  it('builds an Anthropic binding with a default model fallback', () => {
    const binding = createProviderBinding({
      env: {apiKey: 'key', providerId: 'anthropic'},
      providerId: 'anthropic',
    })

    const request = binding.createRequest({prompt: 'Hi'})

    expect(request).to.include({prompt: 'Hi', providerId: 'anthropic'})
    expect(request.model).to.equal('claude-3-5-sonnet-latest')
  })

  it('builds a DeepSeek binding with a default model fallback', () => {
    const binding = createProviderBinding({
      env: {apiKey: 'key', providerId: 'deepseek'},
      providerId: 'deepseek',
    })

    const request = binding.createRequest({prompt: 'Hi'})

    expect(request).to.include({prompt: 'Hi', providerId: 'deepseek'})
    expect(request.model).to.equal('deepseek-chat')
  })
})

import { expect } from 'chai'

import { createProviderBinding } from '../../src/providers/factory.ts'

describe('createProviderBinding', () => {
  it('builds a DeepSeek binding with a default model fallback', () => {
    const binding = createProviderBinding({
      env: { apiKey: 'key', providerId: 'deepseek' },
      providerId: 'deepseek',
    })

    const request = binding.createRequest({ prompt: 'Hi' })

    expect(request).to.include({ prompt: 'Hi', providerId: 'deepseek' })
    expect(request.model).to.equal('deepseek-chat')
  })
})

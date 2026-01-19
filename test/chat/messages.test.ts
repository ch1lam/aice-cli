import { expect } from 'chai'

import type { PromptMessage } from '../../src/types/chat.ts'

import { buildMessages } from '../../src/chat/messages.ts'

describe('buildMessages', () => {
  it('maps history into model messages', () => {
    const history: PromptMessage[] = [
      { role: 'system', text: 'Be helpful.' },
      { role: 'user', text: 'Hello' },
      { role: 'assistant', text: 'Hi there' },
    ]

    const messages = buildMessages(history)

    expect(messages).to.deep.equal([
      { content: 'Be helpful.', role: 'system' },
      { content: 'Hello', role: 'user' },
      { content: 'Hi there', role: 'assistant' },
    ])
  })

  it('truncates to the most recent messages when a max is provided', () => {
    const history: PromptMessage[] = [
      { role: 'user', text: 'First' },
      { role: 'assistant', text: 'One' },
      { role: 'user', text: 'Second' },
      { role: 'assistant', text: 'Two' },
    ]

    const messages = buildMessages(history, { maxMessages: 2 })

    expect(messages).to.deep.equal([
      { content: 'Second', role: 'user' },
      { content: 'Two', role: 'assistant' },
    ])
  })
})

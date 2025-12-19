import { expect } from 'chai'

import type { PromptMessage } from '../../src/types/chat.ts'

import { buildPrompt } from '../../src/chat/prompt.ts'

describe('buildPrompt', () => {
  it('formats history with role labels and ignores system messages', () => {
    const history: PromptMessage[] = [
      { role: 'system', text: 'ignored' },
      { role: 'user', text: 'Hello' },
      { role: 'assistant', text: 'Hi there' },
    ]

    const prompt = buildPrompt(history)

    expect(prompt).to.equal('User: Hello\nAssistant: Hi there\nAssistant:')
  })

  it('truncates to the most recent messages when a max is provided', () => {
    const history: PromptMessage[] = [
      { role: 'user', text: 'First' },
      { role: 'assistant', text: 'One' },
      { role: 'user', text: 'Second' },
      { role: 'assistant', text: 'Two' },
    ]

    const prompt = buildPrompt(history, { maxMessages: 2 })

    expect(prompt).to.equal('User: Second\nAssistant: Two\nAssistant:')
  })
})

import React from 'react'
import {describe, it} from 'mocha'
import {expect} from 'chai'
import {render} from 'ink-testing-library'

import {ChatWindow} from '../../src/ui/chat-window.ts'

describe('ChatWindow', () => {
  it('renders prompt and placeholder while awaiting response', () => {
    const {lastFrame} = render(React.createElement(ChatWindow, {prompt: 'Hello?', responseChunks: []}))

    expect(lastFrame()).to.contain('You')
    expect(lastFrame()).to.contain('Hello?')
    expect(lastFrame()).to.contain('Assistant')
    expect(lastFrame()).to.contain('…')
  })

  it('appends streamed assistant chunks in order', () => {
    const {lastFrame, rerender} = render(
      React.createElement(ChatWindow, {
        prompt: 'Summarise the repo',
        responseChunks: [{index: 0, text: 'Sure, ', type: 'text'}],
      }),
    )

    expect(lastFrame()).to.contain('Sure, ')

    rerender(
      React.createElement(ChatWindow, {
        prompt: 'Summarise the repo',
        responseChunks: [
          {index: 0, text: 'Sure, ', type: 'text'},
          {index: 1, text: 'here is the summary.', type: 'text'},
        ],
      }),
    )

    expect(lastFrame()).to.contain('Sure, here is the summary.')
  })
})

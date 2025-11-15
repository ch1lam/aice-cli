import React from 'react'
import {describe, it} from 'mocha'
import {expect} from 'chai'
import {render} from 'ink-testing-library'

import {StatusBar} from '../../src/ui/status-bar.ts'

describe('StatusBar', () => {
  it('shows provider metadata, status, and usage tokens', () => {
    const {lastFrame} = render(
      React.createElement(StatusBar, {
        model: 'gpt-4o-mini',
        providerId: 'openai',
        status: 'running',
        statusDetail: 'generating',
        usage: {inputTokens: 12, outputTokens: 3, totalTokens: 15},
      }),
    )

    expect(lastFrame()).to.contain('provider=openai')
    expect(lastFrame()).to.contain('model=gpt-4o-mini')
    expect(lastFrame()).to.contain('status=running detail=generating')
    expect(lastFrame()).to.contain('usage in=12 out=3 total=15')
  })

  it('falls back to idle status and placeholder usage, rendering errors in red', () => {
    const {lastFrame} = render(React.createElement(StatusBar, {error: new Error('Boom'), usage: {}}))

    expect(lastFrame()).to.contain('provider=-')
    expect(lastFrame()).to.contain('status=idle')
    expect(lastFrame()).to.contain('usage in=- out=- total=-')
    expect(lastFrame()).to.contain('error=Boom')
  })
})

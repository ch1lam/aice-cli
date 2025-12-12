import { expect } from 'chai'

import { isProviderId, KNOWN_PROVIDERS, parseProviderId } from '../../src/core/stream.ts'

describe('provider id helpers', () => {
  it('recognizes known providers', () => {
    for (const providerId of KNOWN_PROVIDERS) {
      expect(isProviderId(providerId)).to.equal(true)
      expect(parseProviderId(providerId)).to.equal(providerId)
    }
  })

  it('rejects unknown providers', () => {
    expect(isProviderId('')).to.equal(false)
    expect(isProviderId('unknown')).to.equal(false)
    expect(parseProviderId('unknown')).to.equal(undefined)
  })
})


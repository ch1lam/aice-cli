import { expect } from 'chai'

import { isProviderId, parseProviderId, providerIds } from '../../src/config/provider-defaults.ts'

describe('provider defaults helpers', () => {
  it('recognizes known providers', () => {
    for (const providerId of providerIds) {
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

import { expect } from 'chai'

import { toError } from '../../src/core/errors.ts'

describe('toError', () => {
  it('returns the original Error when no formatting is needed', () => {
    const original = new Error('boom')

    const resolved = toError(original, 'fallback')

    expect(resolved).to.equal(original)
  })

  it('prefixes codes on Error instances when missing from the message', () => {
    const original = Object.assign(new Error('boom'), { code: 'rate_limit_exceeded' })

    const resolved = toError(original, 'fallback')

    expect(resolved).to.not.equal(original)
    expect(resolved.message).to.equal('rate_limit_exceeded: boom')
  })

  it('falls back to the default message when error objects omit message', () => {
    const resolved = toError({ code: 'rate_limit_exceeded' }, 'DeepSeek stream error')

    expect(resolved.message).to.equal('rate_limit_exceeded: DeepSeek stream error')
  })

  it('reads nested error shapes', () => {
    const resolved = toError({ error: { code: 'oops', message: 'nested' } }, 'fallback')

    expect(resolved.message).to.equal('oops: nested')
  })

  it('wraps unknown values with the fallback message', () => {
    const resolved = toError(null, 'fallback')

    expect(resolved.message).to.equal('fallback')
  })
})

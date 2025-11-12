import {runCommand} from '@oclif/test'
import {expect} from 'chai'

describe('aice-cli bootstrap', () => {
  it('prints help output', async () => {
    const {stdout} = await runCommand('--help')
    expect(stdout).to.include('USAGE')
  })
})

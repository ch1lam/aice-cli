import { expect } from 'chai'

import type { SlashCommandDefinition } from '../../src/types/slash-commands.js'

import { createSlashCommandRouter } from '../../src/ui/slash-commands.js'

function buildRouter(callLog: string[] = []) {
  const definitions: SlashCommandDefinition[] = [
    {
      command: 'help',
      description: 'help',
      handler(_args, context) {
        callLog.push(`help:${context.definitions.length}`)
      },
      hint: '/help',
      usage: '/help',
    },
    {
      command: 'login',
      description: 'login',
      handler() {
        callLog.push('login')
      },
      hint: '/login',
      usage: '/login',
    },
    {
      command: 'model',
      description: 'model',
      handler(args) {
        callLog.push(`model:${args.join(',')}`)
      },
      hint: '/model',
      usage: '/model',
    },
    {
      command: 'clear',
      description: 'clear',
      handler() {
        callLog.push('clear')
      },
      hint: '/clear',
      usage: '/clear',
    },
  ]

  return createSlashCommandRouter(definitions)
}

describe('slash command router', () => {
  it('routes commands and normalizes arguments', () => {
    const calls: string[] = []
    const router = buildRouter(calls)

    const result = router.handle('/model   ')

    expect(result.type).to.equal('handled')
    expect(result.command).to.equal('model')
    expect(calls).to.deep.equal(['model:'])
  })

  it('reports empty commands', () => {
    const calls: string[] = []
    const router = buildRouter(calls)

    const result = router.handle('/   ')

    expect(result.type).to.equal('empty')
    expect(calls).to.be.empty
  })

  it('reports unknown commands without invoking handlers', () => {
    const calls: string[] = []
    const router = buildRouter(calls)

    const result = router.handle('/unknown command')

    expect(result.type).to.equal('unknown')
    expect(result.command).to.equal('unknown')
    expect(calls).to.be.empty
  })
})

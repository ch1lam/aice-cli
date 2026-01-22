import { render } from 'ink'
import React from 'react'

import { SetupService } from '../services/setup-service.js'
import { AiceApp } from './aice-app.js'

export async function runTui(): Promise<void> {
  const setupService = new SetupService()

  const runOnce = async (): Promise<boolean> => {
    clearTerminal()
    const { env, error } = setupService.tryLoadEnv()

    return new Promise<boolean>(resolve => {
      let resolved = false
      const settle = (value: boolean) => {
        if (resolved) return
        resolved = true
        resolve(value)
      }

      const instanceRef: {current?: ReturnType<typeof render>} = {}
      const handleNewSession = () => {
        settle(true)
        instanceRef.current?.unmount()
      }

      instanceRef.current = render(
        React.createElement(AiceApp, {
          initialEnv: env,
          initialError: error,
          onNewSession: handleNewSession,
        }),
      )

      instanceRef.current.waitUntilExit().then(() => settle(false))
    })
  }

  const runLoop = async (): Promise<void> => {
    const restart = await runOnce()
    if (restart) {
      await runLoop()
    }
  }

  await runLoop()
}

function clearTerminal(stdout: NodeJS.WritableStream = process.stdout): void {
  // Clear the visible screen and jump to top-left, but keep scrollback intact for debugging.
  stdout.write('\u001B[2J\u001B[H')
}

import { render } from 'ink'
import React from 'react'

import { SetupService } from '../services/setup-service.js'
import { AiceApp } from './aice-app.js'

export async function runTui(): Promise<void> {
  const setupService = new SetupService()
  const { env, error } = setupService.tryLoadEnv()
  const instance = render(
    React.createElement(AiceApp, {
      initialEnv: env,
      initialError: error,
    }),
    // Keep Ink on the conservative full-frame diff path. This app prepends
    // completed transcript lines via <Static> while keeping a live footer
    // below it, and incremental line updates can leave stale footer rows in
    // scrollback as the transcript grows.
  )

  await instance.waitUntilExit()
}

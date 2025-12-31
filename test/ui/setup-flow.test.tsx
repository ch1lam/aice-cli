import { expect } from 'chai'
import { Text } from 'ink'
import { render } from 'ink-testing-library'
import { type ReactElement, useEffect } from 'react'

import type { ProviderEnv } from '../../src/types/env.js'
import type { SetupServiceOptions } from '../../src/types/setup-service.js'
import type { UseSetupFlowResult } from '../../src/ui/hooks/use-setup-flow.js'

import { useSetupFlow } from '../../src/ui/hooks/use-setup-flow.js'

function delay(duration: number): Promise<void> {
  return new Promise(resolve => {
    setTimeout(resolve, duration)
  })
}

async function waitFor(condition: () => boolean, timeoutMs = 1000, intervalMs = 10): Promise<void> {
  const start = Date.now()

  while (Date.now() - start < timeoutMs) {
    if (condition()) return
    // eslint-disable-next-line no-await-in-loop
    await delay(intervalMs)
  }

  throw new Error('Timed out waiting for condition')
}

interface SetupFlowHarnessProps {
  initialEnv?: ProviderEnv
  onEnvReady?: (env: ProviderEnv) => void
  onMessage: (message: string) => void
  onState: (state: UseSetupFlowResult) => void
  persistEnv?: SetupServiceOptions['persistEnv']
  ping?: SetupServiceOptions['ping']
  tryLoadEnv?: SetupServiceOptions['tryLoadEnv']
}

function SetupFlowHarness(props: SetupFlowHarnessProps): ReactElement {
  const {
    initialEnv,
    onEnvReady,
    onMessage,
    onState,
    persistEnv,
    ping,
    tryLoadEnv,
  } = props
  const state = useSetupFlow({
    initialEnv,
    onEnvReady,
    onMessage,
    persistEnv,
    ping,
    tryLoadEnv,
  })

  useEffect(() => {
    onState(state)
  }, [onState, state])

  return (
    <Text>{`mode:${state.mode} step:${state.setupState.step} mask:${state.maskInput}`}</Text>
  )
}

describe('useSetupFlow', () => {
  it('requires an API key before advancing setup', async () => {
    let state: undefined | UseSetupFlowResult
    const messages: string[] = []

    const { unmount } = render(
      <SetupFlowHarness
        onMessage={message => messages.push(message)}
        onState={next => {
          state = next
        }}
      />,
    )

    await waitFor(() => state?.setupState.step === 'apiKey')
    await state?.handleSetupInput('   ')

    await waitFor(() => messages.includes('API key is required.'))
    expect(state?.maskInput).to.equal(true)
    expect(state?.setupState.step).to.equal('apiKey')
    unmount()
  })

  it('completes setup and publishes provider env', async () => {
    let state: undefined | UseSetupFlowResult
    const messages: string[] = []
    const persisted: ProviderEnv[] = []
    let envReady: ProviderEnv | undefined

    const persistEnv: SetupServiceOptions['persistEnv'] = options => {
      persisted.push({
        apiKey: options.apiKey,
        baseURL: options.baseURL,
        model: options.model,
        providerId: options.providerId,
      })
      return {}
    }

    const tryLoadEnv: SetupServiceOptions['tryLoadEnv'] = () => {
      const latest = persisted.at(-1)
      if (!latest) {
        return { error: new Error('Missing persisted env') }
      }

      return { env: latest }
    }

    const { unmount } = render(
      <SetupFlowHarness
        onEnvReady={env => {
          envReady = env
        }}
        onMessage={message => messages.push(message)}
        onState={next => {
          state = next
        }}
        persistEnv={persistEnv}
        ping={async () => {}}
        tryLoadEnv={tryLoadEnv}
      />,
    )

    await waitFor(() => state?.setupState.step === 'apiKey')
    await state?.handleSetupInput('sk-test')
    await waitFor(() => state?.setupState.step === 'baseURL')

    await state?.handleSetupInput('')
    await waitFor(() => state?.setupState.step === 'model')

    await state?.handleSetupInput('deepseek-chat')
    await waitFor(() => state?.mode === 'chat')

    expect(envReady).to.deep.equal({
      apiKey: 'sk-test',
      baseURL: undefined,
      model: 'deepseek-chat',
      providerId: 'deepseek',
    })
    expect(state?.maskInput).to.equal(false)
    expect(state?.providerEnv).to.deep.equal(envReady)
    expect(state?.setupSubmitting).to.equal(false)
    expect(persisted).to.have.lengthOf(1)
    expect(messages).to.deep.equal([
      'API key captured. Optional: enter base URL override, or press Enter to skip.',
      'Optional: enter model override, or press Enter to skip.',
      'Checking provider connectivity...',
      'Configured deepseek (deepseek-chat). Type /help to see commands.',
    ])
    unmount()
  })

  it('resets setup when connectivity checks fail', async () => {
    let state: undefined | UseSetupFlowResult
    const messages: string[] = []
    const persisted: ProviderEnv[] = []

    const persistEnv: SetupServiceOptions['persistEnv'] = options => {
      persisted.push({
        apiKey: options.apiKey,
        baseURL: options.baseURL,
        model: options.model,
        providerId: options.providerId,
      })
      return {}
    }

    const tryLoadEnv: SetupServiceOptions['tryLoadEnv'] = () => {
      const latest = persisted.at(-1)
      if (!latest) {
        return { error: new Error('Missing persisted env') }
      }

      return { env: latest }
    }

    const { unmount } = render(
      <SetupFlowHarness
        onMessage={message => messages.push(message)}
        onState={next => {
          state = next
        }}
        persistEnv={persistEnv}
        ping={async () => {
          throw new Error('bad auth')
        }}
        tryLoadEnv={tryLoadEnv}
      />,
    )

    await waitFor(() => state?.setupState.step === 'apiKey')
    await state?.handleSetupInput('sk-bad')
    await waitFor(() => state?.setupState.step === 'baseURL')

    await state?.handleSetupInput('')
    await waitFor(() => state?.setupState.step === 'model')

    await state?.handleSetupInput('deepseek-chat')
    await waitFor(
      () =>
        state?.mode === 'setup' &&
        state?.maskInput === true &&
        state?.setupState.step === 'apiKey',
    )

    expect(state?.maskInput).to.equal(true)
    expect(state?.providerEnv).to.equal(undefined)
    expect(state?.setupState.step).to.equal('apiKey')
    expect(state?.setupSubmitting).to.equal(false)
    expect(messages).to.include('Checking provider connectivity...')
    expect(messages.at(-1)).to.equal(
      'Connectivity check failed for deepseek: bad auth. Please verify your API key or base URL.',
    )
    unmount()
  })
})

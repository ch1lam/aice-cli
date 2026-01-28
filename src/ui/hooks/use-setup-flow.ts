import { type Dispatch, type SetStateAction, useCallback, useMemo, useState } from 'react'

import type { ProviderEnv } from '../../types/env.js'
import type { AppMode, SetupState } from '../../types/setup-flow.js'
import type { SetupServiceOptions } from '../../types/setup-service.js'

import { DEFAULT_PROVIDER_ID, resolveDefaultModel } from '../../config/provider-defaults.js'
import {
  ProviderEnvLoadError,
  ProviderEnvPersistError,
  SetupService,
} from '../../services/setup-service.js'

const defaultCreateSetupService = (serviceOptions: SetupServiceOptions) =>
  new SetupService(serviceOptions)

interface UseSetupFlowOptions {
  createSetupService?: (options: SetupServiceOptions) => SetupService
  initialEnv?: ProviderEnv
  onEnvReady?: (env: ProviderEnv) => void
  onMessage: (message: string) => void
  persistEnv?: SetupServiceOptions['persistEnv']
  ping?: SetupServiceOptions['ping']
  setupService?: SetupService
  tryLoadEnv?: SetupServiceOptions['tryLoadEnv']
}

export interface UseSetupFlowResult {
  cancelSetup(): void
  handleSetupInput(value: string): Promise<void>
  maskInput: boolean
  mode: AppMode
  providerEnv?: ProviderEnv
  resetSetup(): void
  setProviderEnv: Dispatch<SetStateAction<ProviderEnv | undefined>>
  setupState: SetupState
  setupSubmitting: boolean
}

export function useSetupFlow(options: UseSetupFlowOptions): UseSetupFlowResult {
  const {
    createSetupService = defaultCreateSetupService,
    initialEnv,
    onEnvReady,
    onMessage,
    persistEnv,
    ping,
    setupService: providedSetupService,
    tryLoadEnv,
  } = options

  const setupService = useMemo(
    () => providedSetupService ?? createSetupService({ persistEnv, ping, tryLoadEnv }),
    [createSetupService, persistEnv, ping, providedSetupService, tryLoadEnv],
  )

  const initialProviderId = initialEnv?.providerId ?? DEFAULT_PROVIDER_ID

  const [mode, setMode] = useState<AppMode>(initialEnv ? 'chat' : 'setup')
  const [providerEnv, setProviderEnv] = useState<ProviderEnv | undefined>(initialEnv)
  const [setupSubmitting, setSetupSubmitting] = useState(false)
  const [maskInput, setMaskInput] = useState(!initialEnv)
  const [setupState, setSetupState] = useState<SetupState>({
    apiKey: undefined,
    baseURL: undefined,
    model: undefined,
    providerId: initialProviderId,
    step: 'apiKey',
  })

  const resetSetup = useCallback(() => {
    setMode('setup')
    setMaskInput(true)
    setSetupState(current => ({
      apiKey: undefined,
      baseURL: undefined,
      model: undefined,
      providerId: current.providerId,
      step: 'apiKey',
    }))
  }, [])

  const cancelSetup = useCallback(() => {
    if (!providerEnv) {
      onMessage('Provider not configured. Setup is required.')
      return
    }

    setMode('chat')
    setMaskInput(false)
    setSetupState(current => ({
      apiKey: undefined,
      baseURL: undefined,
      model: undefined,
      providerId: current.providerId,
      step: 'apiKey',
    }))
    onMessage('Setup cancelled. Returning to chat.')
  }, [onMessage, providerEnv])

  const handleMissingApiKey = useCallback(() => {
    onMessage('Missing API key; restart setup with /login.')
    resetSetup()
  }, [onMessage, resetSetup])

  const persistSetupEnv = useCallback(
    (overrides: {model?: string}): ProviderEnv | undefined => {
      const { apiKey, baseURL, model, providerId } = setupState
      if (!apiKey) {
        handleMissingApiKey()
        return undefined
      }

      try {
        return setupService.persistAndLoad({
          apiKey,
          baseURL,
          model: overrides.model ?? model,
          providerId,
        })
      } catch (error) {
        if (error instanceof ProviderEnvPersistError) {
          onMessage(`Failed to write .env: ${error.message}`)
          return undefined
        }

        if (error instanceof ProviderEnvLoadError) {
          onMessage(`Failed to load provider config. ${error.message}`)
          resetSetup()
          return undefined
        }

        const message = error instanceof Error ? error.message : String(error)
        onMessage(`Failed to write .env: ${message}`)
        return undefined
      }
    },
    [handleMissingApiKey, onMessage, resetSetup, setupService, setupState],
  )

  const finalizeSetup = useCallback(
    async (env: ProviderEnv) => {
      setSetupSubmitting(true)
      onMessage('Checking provider connectivity...')

      try {
        await setupService.verifyConnectivity(env)
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error)
        onMessage(
          `Connectivity check failed for ${env.providerId}: ${message}. Please verify your API key or base URL.`,
        )
        setMode('setup')
        setMaskInput(true)
        setSetupState({
          apiKey: undefined,
          baseURL: undefined,
          model: undefined,
          providerId: env.providerId,
          step: 'apiKey',
        })
        return
      } finally {
        setSetupSubmitting(false)
      }

      setProviderEnv(env)
      setMode('chat')
      setSetupState({
        apiKey: undefined,
        baseURL: undefined,
        model: undefined,
        providerId: env.providerId,
        step: 'apiKey',
      })
      onEnvReady?.(env)
      const resolvedModel = resolveDefaultModel(env.providerId, env.model)
      onMessage(
        `Configured ${env.providerId} (${resolvedModel}). Type /help to see commands.`,
      )
    },
    [onEnvReady, onMessage, setupService],
  )

  const handleSetupInput = useCallback(
    async (rawValue: string) => {
      if (setupSubmitting) {
        onMessage('Setup in progress. Please wait.')
        return
      }

      const trimmed = rawValue.trim()

      switch (setupState.step) {
        case 'apiKey': {
          if (!trimmed) {
            onMessage('API key is required.')
            return
          }

          setSetupState(current => ({
            ...current,
            apiKey: trimmed,
            step: 'baseURL',
          }))
          setMaskInput(false)
          onMessage(
            'API key captured. Optional: enter base URL override, or press Enter to skip.',
          )
          return
        }

        case 'baseURL': {
          setSetupState(current => ({
            ...current,
            baseURL: trimmed || undefined,
            step: 'model',
          }))
          onMessage('Optional: enter model override, or press Enter to skip.')
          return
        }

        case 'model': {
          if (!setupState.apiKey) {
            handleMissingApiKey()
            return
          }

          const nextModel = trimmed || undefined
          setSetupState(current => ({ ...current, model: nextModel }))
          const env = persistSetupEnv({ model: nextModel })

          if (env) {
            await finalizeSetup(env)
          }
        }
      }
    },
    [
      finalizeSetup,
      handleMissingApiKey,
      onMessage,
      persistSetupEnv,
      setupState.apiKey,
      setupState.step,
      setupSubmitting,
    ],
  )

  return {
    cancelSetup,
    handleSetupInput,
    maskInput,
    mode,
    providerEnv,
    resetSetup,
    setProviderEnv,
    setupState,
    setupSubmitting,
  }
}

import { type Dispatch, type SetStateAction, useCallback, useMemo, useState } from 'react'

import type { ProviderId } from '../../core/stream.js'
import type { ProviderEnv } from '../../types/env.js'

import {
  ProviderEnvLoadError,
  ProviderEnvPersistError,
  SetupService,
  type SetupServiceOptions,
} from '../../services/setup-service.js'
import { providerIdFromIndex, providerOptionIndex } from '../provider-options.js'

export type AppMode = 'chat' | 'setup'

export type SetupStep = 'apiKey' | 'baseURL' | 'model' | 'provider'

const defaultCreateSetupService = (serviceOptions: SetupServiceOptions) =>
  new SetupService(serviceOptions)

export interface SetupState {
  apiKey?: string
  baseURL?: string
  model?: string
  providerId: ProviderId
  step: SetupStep
}

interface UseSetupFlowOptions {
  createSetupService?: (options: SetupServiceOptions) => SetupService
  initialEnv?: ProviderEnv
  onEnvReady?: (env: ProviderEnv) => void
  onMessage: (message: string) => void
  persistEnv?: SetupServiceOptions['persistEnv']
  ping?: SetupServiceOptions['ping']
  tryLoadEnv?: SetupServiceOptions['tryLoadEnv']
}

export interface UseSetupFlowResult {
  handleSetupInput(value: string): Promise<void>
  maskInput: boolean
  mode: AppMode
  providerChoiceIndex: number
  providerEnv?: ProviderEnv
  providerSelection: ProviderId
  resetSetup(nextProviderId?: ProviderId): void
  setProviderChoiceIndex: Dispatch<SetStateAction<number>>
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
    tryLoadEnv,
  } = options

  const setupService = useMemo(
    () => createSetupService({ persistEnv, ping, tryLoadEnv }),
    [createSetupService, persistEnv, ping, tryLoadEnv],
  )

  const initialProviderId = initialEnv?.providerId ?? 'openai'

  const [mode, setMode] = useState<AppMode>(initialEnv ? 'chat' : 'setup')
  const [providerEnv, setProviderEnv] = useState<ProviderEnv | undefined>(initialEnv)
  const [setupSubmitting, setSetupSubmitting] = useState(false)
  const [maskInput, setMaskInput] = useState(false)
  const [setupState, setSetupState] = useState<SetupState>({
    apiKey: undefined,
    baseURL: undefined,
    model: undefined,
    providerId: initialProviderId,
    step: 'provider',
  })
  const [providerChoiceIndex, setProviderChoiceIndex] = useState(
    providerOptionIndex(initialProviderId),
  )

  const providerSelection = providerIdFromIndex(providerChoiceIndex)

  const resetSetup = useCallback((nextProviderId: ProviderId = 'openai') => {
    setMode('setup')
    setMaskInput(false)
    setProviderChoiceIndex(providerOptionIndex(nextProviderId))
    setSetupState({
      apiKey: undefined,
      baseURL: undefined,
      model: undefined,
      providerId: nextProviderId,
      step: 'provider',
    })
  }, [])

  const handleMissingApiKey = useCallback(() => {
    onMessage('Missing API key; restart setup with /login.')
    resetSetup(setupState.providerId)
  }, [onMessage, resetSetup, setupState.providerId])

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
          resetSetup(providerId)
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
        setProviderChoiceIndex(providerOptionIndex(env.providerId))
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
      setProviderChoiceIndex(providerOptionIndex(env.providerId))
      setSetupState({
        apiKey: undefined,
        baseURL: undefined,
        model: undefined,
        providerId: env.providerId,
        step: 'provider',
      })
      onEnvReady?.(env)
      onMessage(
        `Configured ${env.providerId} (${env.model ?? 'default model'}). Type /help to see commands.`,
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

          return
        }

        case 'provider': {
          const providerId = providerSelection

          setProviderChoiceIndex(providerOptionIndex(providerId))
          setSetupState({
            apiKey: undefined,
            baseURL: undefined,
            model: undefined,
            providerId,
            step: 'apiKey',
          })
          setMaskInput(true)
          setMode('setup')
          onMessage(`Using provider ${providerId}. Enter API key:`)
          break
        }
      }
    },
    [
      finalizeSetup,
      handleMissingApiKey,
      onMessage,
      persistSetupEnv,
      providerSelection,
      setProviderChoiceIndex,
      setupState.apiKey,
      setupState.providerId,
      setupState.step,
      setupSubmitting,
    ],
  )

  return {
    handleSetupInput,
    maskInput,
    mode,
    providerChoiceIndex,
    providerEnv,
    providerSelection,
    resetSetup,
    setProviderChoiceIndex,
    setProviderEnv,
    setupState,
    setupSubmitting,
  }
}

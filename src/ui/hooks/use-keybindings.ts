import type { Dispatch, SetStateAction } from 'react'

import { useApp, useInput } from 'ink'

import type { AppMode, SetupState } from './use-setup-flow.js'
import type { SlashSuggestionsState } from './use-slash-suggestions-state.js'

import { cycleProviderChoice } from '../utils.js'

type InkKey = Parameters<Parameters<typeof useInput>[0]>[1]

interface UseKeybindingsOptions {
  input: string
  mode: AppMode
  onSubmit: () => void
  providerOptionCount: number
  setInput: Dispatch<SetStateAction<string>>
  setProviderChoiceIndex: Dispatch<SetStateAction<number>>
  setupState: SetupState
  setupSubmitting: boolean
  slashSuggestions: SlashSuggestionsState
  streaming: boolean
}

export function useKeybindings(options: UseKeybindingsOptions): void {
  const { exit } = useApp()
  const {
    input,
    mode,
    onSubmit,
    providerOptionCount,
    setInput,
    setProviderChoiceIndex,
    setupState,
    setupSubmitting,
    slashSuggestions,
    streaming,
  } = options

  const handleProviderSelectKey = (key: InkKey): void => {
    if (key.upArrow) {
      setProviderChoiceIndex(current => cycleProviderChoice(current, -1, providerOptionCount))
      return
    }

    if (key.downArrow) {
      setProviderChoiceIndex(current => cycleProviderChoice(current, 1, providerOptionCount))
      return
    }

    if (key.return) {
      onSubmit()
    }
  }

  const handleSlashSuggestionKey = (key: InkKey): boolean => {
    const canUseSlashSuggestions =
      slashSuggestions.active && slashSuggestions.suggestions.length > 0 && !streaming

    if (!canUseSlashSuggestions) return false

    if (key.tab) {
      const nextValue = slashSuggestions.applyActiveSuggestion(input)
      if (nextValue) {
        setInput(nextValue)
      }

      return true
    }

    if (key.downArrow) {
      slashSuggestions.selectNext()
      return true
    }

    if (key.upArrow) {
      slashSuggestions.selectPrevious()
      return true
    }

    return false
  }

  useInput((receivedInput, key) => {
    if (key.ctrl && receivedInput === 'c') {
      exit()
      return
    }

    if (setupSubmitting) return

    if (mode === 'setup' && setupState.step === 'provider') {
      handleProviderSelectKey(key)
      return
    }

    if (handleSlashSuggestionKey(key)) return

    if (key.return) {
      onSubmit()
      return
    }

    if (key.backspace || key.delete) {
      setInput(current => current.slice(0, -1))
      return
    }

    if (key.escape) return

    if (receivedInput) {
      setInput(current => `${current}${receivedInput}`)
    }
  })
}

import type { Dispatch, SetStateAction } from 'react'

import { useApp, useInput } from 'ink'

import type { SlashSuggestionsState } from '../../types/slash-suggestions-state.js'

type InkKey = Parameters<Parameters<typeof useInput>[0]>[1]

interface UseKeybindingsOptions {
  input: string
  modelMenu?: {
    active: boolean
    cancel: () => void
    confirm: () => void
    selectNext: () => void
    selectPrevious: () => void
  }
  onSetupCancel?: () => void
  onSubmit: () => void
  setInput: Dispatch<SetStateAction<string>>
  setupMode?: boolean
  setupSubmitting: boolean
  slashSuggestions: SlashSuggestionsState
  streaming: boolean
}

export function useKeybindings(options: UseKeybindingsOptions): void {
  const { exit } = useApp()
  const {
    input,
    modelMenu,
    onSetupCancel,
    onSubmit,
    setInput,
    setupMode,
    setupSubmitting,
    slashSuggestions,
    streaming,
  } = options

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

    if (modelMenu?.active) {
      if (key.return) {
        modelMenu.confirm()
        return
      }

      if (key.escape) {
        modelMenu.cancel()
        return
      }

      if (key.downArrow) {
        modelMenu.selectNext()
        return
      }

      if (key.upArrow) {
        modelMenu.selectPrevious()
        return
      }

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

    if (key.escape) {
      if (setupMode && onSetupCancel) {
        onSetupCancel()
      }

      return
    }

    if (receivedInput) {
      setInput(current => `${current}${receivedInput}`)
    }
  })
}

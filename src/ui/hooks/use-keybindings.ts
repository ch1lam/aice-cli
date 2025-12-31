import type { Dispatch, SetStateAction } from 'react'

import { useApp, useInput } from 'ink'

import type { SlashSuggestionsState } from '../../types/slash-suggestions-state.js'

type InkKey = Parameters<Parameters<typeof useInput>[0]>[1]

interface UseKeybindingsOptions {
  input: string
  onSubmit: () => void
  setInput: Dispatch<SetStateAction<string>>
  setupSubmitting: boolean
  slashSuggestions: SlashSuggestionsState
  streaming: boolean
}

export function useKeybindings(options: UseKeybindingsOptions): void {
  const { exit } = useApp()
  const {
    input,
    onSubmit,
    setInput,
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

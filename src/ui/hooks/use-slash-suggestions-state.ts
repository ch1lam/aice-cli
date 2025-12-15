import { useCallback, useEffect, useMemo, useState } from 'react'

import type { SlashSuggestion } from '../slash-suggestions.js'
import type { AppMode } from './use-setup-flow.js'

import { isSlashCommandInput } from '../slash-commands.js'
import { clampIndex, cycleIndex } from '../utils.js'

export interface SlashSuggestionsState {
  active: boolean
  activeIndex: number
  applyActiveSuggestion(baseInput: string): string | undefined
  getSubmissionValue(baseInput: string): string
  selectNext(): void
  selectPrevious(): void
  suggestions: SlashSuggestion[]
}

interface UseSlashSuggestionsStateOptions {
  input: string
  mode: AppMode
  suggestionsForQuery: (query: string) => SlashSuggestion[]
}

export function useSlashSuggestionsState(
  options: UseSlashSuggestionsStateOptions,
): SlashSuggestionsState {
  const { input, mode, suggestionsForQuery } = options
  const active = mode === 'chat' && isSlashCommandInput(input)
  const [activeIndex, setActiveIndex] = useState(0)

  const query = useMemo(() => {
    if (!active) return ''
    const [commandPart] = input.slice(1).split(' ')
    return (commandPart ?? '').toLowerCase()
  }, [active, input])

  const suggestions = useMemo(
    () => (active ? suggestionsForQuery(query) : []),
    [active, query, suggestionsForQuery],
  )

  useEffect(() => {
    if (!active || suggestions.length === 0) {
      setActiveIndex(0)
      return
    }

    setActiveIndex(current => clampIndex(current, suggestions.length))
  }, [active, suggestions.length])

  useEffect(() => {
    if (!active) return
    setActiveIndex(0)
  }, [active, query])

  const getSubmissionValue = useCallback(
    (baseInput: string) => buildSlashSubmissionValue(activeIndex, baseInput, suggestions),
    [activeIndex, suggestions],
  )

  const applyActiveSuggestion = useCallback(
    (baseInput: string) => {
      if (suggestions.length === 0) return
      const submission = getSubmissionValue(baseInput)
      if (!submission) return
      const needsSpace = submission.endsWith(' ') ? '' : ' '
      return `${submission}${needsSpace}`
    },
    [getSubmissionValue, suggestions.length],
  )

  const selectNext = useCallback(() => {
    setActiveIndex(current => cycleIndex(current, 1, suggestions.length))
  }, [suggestions.length])

  const selectPrevious = useCallback(() => {
    setActiveIndex(current => cycleIndex(current, -1, suggestions.length))
  }, [suggestions.length])

  return {
    active,
    activeIndex,
    applyActiveSuggestion,
    getSubmissionValue,
    selectNext,
    selectPrevious,
    suggestions,
  }
}

function buildSlashSubmissionValue(
  targetIndex: number,
  baseInput: string,
  suggestions: SlashSuggestion[],
): string {
  const suggestion = suggestions[clampIndex(targetIndex, suggestions.length)]
  if (!suggestion) return baseInput.trim()

  const [, ...args] = baseInput.slice(1).split(' ')
  const argsText = args.join(' ').trim()
  const argSegment = argsText ? ` ${argsText}` : ''
  return `/${suggestion.command}${argSegment}`.trim()
}

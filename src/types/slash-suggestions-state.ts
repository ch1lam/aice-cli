import type { SlashSuggestion } from './slash-suggestions.js'

export interface SlashSuggestionsState {
  active: boolean
  activeIndex: number
  applyActiveSuggestion(baseInput: string): string | undefined
  getSubmissionValue(baseInput: string): string
  selectNext(): void
  selectPrevious(): void
  suggestions: SlashSuggestion[]
}

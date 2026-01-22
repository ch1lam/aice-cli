import type { SlashSuggestion } from './slash-suggestions.js'

export type SlashCommandId = 'help' | 'login' | 'model' | 'new' | 'provider'

export interface SlashCommandDefinition {
  command: SlashCommandId
  description: string
  handler: SlashCommandHandler
  hint?: string
  usage: string
}

export interface SlashCommandContext {
  definitions: SlashCommandDefinition[]
}

export type SlashCommandHandler = (args: string[], context: SlashCommandContext) => void

export type SlashCommandResultType = 'empty' | 'handled' | 'unknown'

export interface SlashCommandResult {
  args: string[]
  command?: string
  definition?: SlashCommandDefinition
  type: SlashCommandResultType
}

export interface SlashCommandRouter {
  definitions: SlashCommandDefinition[]
  handle(rawInput: string): SlashCommandResult
  suggestions(query: string): SlashSuggestion[]
}

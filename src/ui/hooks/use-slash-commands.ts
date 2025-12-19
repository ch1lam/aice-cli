import { useCallback, useMemo } from 'react'

import type {
  SlashCommandDefinition,
  SlashCommandResult,
  SlashCommandRouter,
} from '../../types/slash-commands.js'
import type { SlashSuggestion } from '../../types/slash-suggestions.js'

import { createSlashCommandRouter } from '../slash-commands.js'

interface UseSlashCommandsOptions {
  onClear: () => void
  onEmpty?: () => void
  onHelp: (definitions: SlashCommandDefinition[]) => void
  onLogin: () => void
  onModel: (args: string[]) => void
  onProvider: (args: string[]) => void
  onUnknown?: (command?: string) => void
}

export interface UseSlashCommandsResult {
  definitions: SlashCommandDefinition[]
  handleSlashCommand(raw: string): SlashCommandResult
  router: SlashCommandRouter
  suggestions(query: string): SlashSuggestion[]
}

export function useSlashCommands(options: UseSlashCommandsOptions): UseSlashCommandsResult {
  const { onClear, onEmpty, onHelp, onLogin, onModel, onProvider, onUnknown } = options

  const definitions = useMemo<SlashCommandDefinition[]>(
    () => [
      {
        command: 'help',
        description: 'Show available commands and usage.',
        handler: (_args, context) => onHelp(context.definitions),
        hint: '/help',
        usage: '/help',
      },
      {
        command: 'login',
        description: 'Restart setup and enter a provider API key.',
        handler: () => onLogin(),
        hint: '/login',
        usage: '/login',
      },
      {
        command: 'provider',
        description: 'Switch between configured providers.',
        handler: args => onProvider(args),
        hint: '/provider openai',
        usage: '/provider <openai|deepseek>',
      },
      {
        command: 'model',
        description: 'Set or change the active model override.',
        handler: args => onModel(args),
        hint: '/model gpt-4o-mini',
        usage: '/model <model-name>',
      },
      {
        command: 'clear',
        description: 'Clear the transcript.',
        handler: () => onClear(),
        hint: '/clear',
        usage: '/clear',
      },
    ],
    [onClear, onHelp, onLogin, onModel, onProvider],
  )

  const router = useMemo(() => createSlashCommandRouter(definitions), [definitions])

  const handleSlashCommand = useCallback(
    (raw: string) => {
      const result = router.handle(raw)

      if (result.type === 'empty') {
        onEmpty?.()
      } else if (result.type === 'unknown') {
        onUnknown?.(result.command)
      }

      return result
    },
    [onEmpty, onUnknown, router],
  )

  return {
    definitions,
    handleSlashCommand,
    router,
    suggestions: (query: string) => router.suggestions(query),
  }
}

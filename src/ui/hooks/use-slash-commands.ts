import { useCallback, useMemo } from 'react'

import type {
  SlashCommandDefinition,
  SlashCommandResult,
  SlashCommandRouter,
} from '../../types/slash-commands.js'
import type { SlashSuggestion } from '../../types/slash-suggestions.js'

import { createSlashCommandRouter } from '../slash-commands.js'

interface UseSlashCommandsOptions {
  onEmpty?: () => void
  onHelp: (definitions: SlashCommandDefinition[]) => void
  onLogin: () => void
  onModel: (args: string[]) => void
  onNew: () => void
  onUnknown?: (command?: string) => void
}

export interface UseSlashCommandsResult {
  definitions: SlashCommandDefinition[]
  handleSlashCommand(raw: string): SlashCommandResult
  router: SlashCommandRouter
  suggestions(query: string): SlashSuggestion[]
}

export function useSlashCommands(options: UseSlashCommandsOptions): UseSlashCommandsResult {
  const { onEmpty, onHelp, onLogin, onModel, onNew, onUnknown } = options

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
        description: 'Restart setup and enter the API key.',
        handler: () => onLogin(),
        hint: '/login',
        usage: '/login',
      },
      {
        command: 'model',
        description: 'Set or change the active model override.',
        handler: args => onModel(args),
        hint: '/model deepseek-chat',
        usage: '/model <model-name>',
      },
      {
        command: 'new',
        description: 'Start a new session.',
        handler: () => onNew(),
        hint: '/new',
        usage: '/new',
      },
    ],
    [onHelp, onLogin, onModel, onNew],
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

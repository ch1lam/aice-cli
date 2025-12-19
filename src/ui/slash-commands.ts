import type {
  SlashCommandDefinition,
  SlashCommandResult,
  SlashCommandRouter,
} from '../types/slash-commands.js'
import type { SlashSuggestion } from '../types/slash-suggestions.js'

export function createSlashCommandRouter(
  definitions: SlashCommandDefinition[],
): SlashCommandRouter {
  const index = new Map<string, SlashCommandDefinition>(
    definitions.map(definition => [definition.command, definition]),
  )

  function handle(rawInput: string): SlashCommandResult {
    const parts = parseCommand(rawInput)

    if (!parts.command) {
      return { args: parts.args, type: 'empty' }
    }

    const definition = index.get(parts.command)
    if (!definition) {
      return { args: parts.args, command: parts.command, type: 'unknown' }
    }

    definition.handler(parts.args, { definitions })
    return { args: parts.args, command: parts.command, definition, type: 'handled' }
  }

  function suggestions(query: string): SlashSuggestion[] {
    const search = query.trim().toLowerCase()

    return definitions
      .filter(definition => {
        if (!search) return true

        return (
          definition.command.includes(search) ||
          definition.description.toLowerCase().includes(search) ||
          (definition.hint?.toLowerCase().includes(search) ?? false) ||
          definition.usage.toLowerCase().includes(search)
        )
      })
      .map(definition => ({
        command: definition.command,
        description: definition.description,
        hint: definition.hint ?? definition.usage,
        value: `/${definition.command}`,
      }))
  }

  return {
    definitions,
    handle,
    suggestions,
  }
}

export function isSlashCommandInput(value: string): boolean {
  return value.startsWith('/')
}

function parseCommand(rawInput: string): {args: string[]; command?: string} {
  const trimmed = rawInput.trim()
  if (!trimmed) {
    return { args: [] }
  }

  const withoutSlash = trimmed.startsWith('/') ? trimmed.slice(1) : trimmed
  const parts = withoutSlash
    .split(' ')
    .map(part => part.trim())
    .filter(Boolean)

  const [command, ...args] = parts

  if (!command) {
    return { args: [] }
  }

  return {
    args,
    command,
  }
}

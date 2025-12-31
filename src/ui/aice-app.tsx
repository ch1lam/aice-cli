import { Box, Text } from 'ink'

import type { MessageRole } from '../types/chat.js'
import type { ProviderEnv } from '../types/env.js'
import type { AppMode, SetupStep } from '../types/setup-flow.js'

import { useChatInputController } from './hooks/use-chat-input-controller.js'
import { InputPanel } from './input-panel.js'
import { SlashSuggestions } from './slash-suggestions.js'
import { StatusBar } from './status-bar.js'
import { theme } from './theme.js'

export interface AiceAppProps {
  initialEnv?: ProviderEnv
  initialError?: Error
}

const messageColors = theme.components.messages

export function AiceApp(props: AiceAppProps) {
  const controller = useChatInputController({
    initialEnv: props.initialEnv,
    initialError: props.initialError,
  })

  const inputLabel = '✧'
  const renderedInput = controller.maskInput ? '*'.repeat(controller.input.length) : controller.input
  const hint = resolveHint(controller.mode, controller.setupStateStep)
  const placeholder = resolvePlaceholder(controller.streaming, controller.setupSubmitting, hint)
  const showCursor = !controller.streaming && !controller.setupSubmitting
  const normalizedCurrentResponse = stripAssistantPadding(controller.currentResponse || '')
  const showSlashSuggestions = shouldShowSlashSuggestions(
    controller.mode,
    controller.streaming,
    controller.slashSuggestions.active,
    controller.slashSuggestions.suggestions.length,
  )

  return (
    <Box flexDirection="column">
      <Text color={theme.components.app.title}>AICE</Text>
      <Box flexDirection="column" marginBottom={1}>
        {controller.messages.map(message => (
          <Box key={message.id}>
            <Text color={colorForRole(message.role)}>{`${labelForRole(message.role)}`}</Text>
            <Text color={colorForRole(message.role)} key={message.id} wrap="wrap">
              {`${message.role === 'assistant' ? stripAssistantPadding(message.text) : message.text}`}
            </Text>
          </Box>
        ))}
        {controller.streaming ? (
          <Box>
            <Text color={messageColors.assistant}>{` ♤ `}</Text>
            <Text color={messageColors.assistant} wrap="wrap">
              {`${normalizedCurrentResponse}${controller.sessionStatus === 'completed' ? '  ' : ' ▌'}`}
            </Text>
          </Box>
        ) : null}
      </Box>
      <Box flexDirection="column" width="100%">
        <InputPanel
          cursorVisible={showCursor}
          disabled={controller.streaming || controller.setupSubmitting}
          label={inputLabel}
          placeholder={placeholder}
          value={renderedInput}
        />
        <SlashSuggestions
          activeIndex={controller.slashSuggestions.activeIndex}
          items={controller.slashSuggestions.suggestions}
          visible={showSlashSuggestions}
        />
      </Box>
      <Box width="100%">
        <StatusBar
          meta={controller.providerMeta}
          status={controller.sessionStatus}
          statusMessage={controller.sessionStatusMessage}
          usage={controller.sessionUsage}
        />
      </Box>
    </Box>
  )
}

function colorForRole(role: MessageRole): string {
  if (role === 'assistant') return messageColors.assistant
  if (role === 'user') return messageColors.user
  return messageColors.system
}

function labelForRole(role: MessageRole): string {
  if (role === 'assistant') return ' ♠ '
  if (role === 'user') return ' ✧ '
  return ' • '
}

function stripAssistantPadding(text: string): string {
  if (!text.startsWith(' ')) return text
  if (text.length === 1) return ''

  return /\s/.test(text[1]) ? text : text.slice(1)
}

function setupPrompt(step: SetupStep): string {
  if (step === 'apiKey') {
    return 'Enter API key (hidden as you type).'
  }

  if (step === 'baseURL') {
    return 'Optional: enter a base URL override, or press Enter to use the default.'
  }

  if (step === 'model') {
    return 'Optional: enter model override, or press Enter to use the default.'
  }

  return ''
}

function resolveHint(mode: AppMode, step: SetupStep): string {
  if (mode === 'setup') {
    return setupPrompt(step)
  }

  return 'Type a prompt or use /help, /login, /model, /clear'
}

function resolvePlaceholder(streaming: boolean, setupSubmitting: boolean, hint: string): string {
  if (streaming) return 'Processing response...'
  if (setupSubmitting) return 'Validating connection...'
  return hint
}

function shouldShowSlashSuggestions(
  mode: AppMode,
  streaming: boolean,
  slashCommandActive: boolean,
  suggestionCount: number,
): boolean {
  return !streaming && mode === 'chat' && slashCommandActive && suggestionCount > 0
}

import { Box, Static, Text } from 'ink'

import type { ChatMessage, MessageRole } from '../types/chat.js'
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
const INPUT_MAX_LINES = 6

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
  const staticItems: StaticItem[] = [
    { key: 'title', kind: 'title' },
    ...(controller.messages.length > 0 ? [{ key: 'title-spacer', kind: 'spacer' }] : []),
    ...controller.messages.map(message => ({
      key: `message-${message.id}`,
      kind: 'message',
      message,
    })),
  ]

  return (
    <Box flexDirection="column">
      <Static items={staticItems}>
        {item => {
          if (item.kind === 'title') {
            return (
              <Text color={theme.components.app.title} key={item.key}>
                AICE
              </Text>
            )
          }

          if (item.kind === 'spacer') {
            return (
              <Text key={item.key}>
                {' '}
              </Text>
            )
          }

          return (
            <Box key={item.key}>
              <Text color={colorForRole(item.message.role)}>
                {`${labelForRole(item.message.role)}`}
              </Text>
              <Text color={colorForRole(item.message.role)} wrap="wrap">
                {`${
                  item.message.role === 'assistant'
                    ? stripAssistantPadding(item.message.text)
                    : item.message.text
                }`}
              </Text>
            </Box>
          )
        }}
      </Static>
      <Box flexDirection="column" marginTop={1} width="100%">
        {controller.streaming ? (
          <Box marginBottom={1}>
            <Text color={messageColors.assistant}>{` ♤ `}</Text>
            <Text color={messageColors.assistant} wrap="wrap">
              {`${normalizedCurrentResponse}${controller.sessionStatus === 'completed' ? '  ' : ' ▌'}`}
            </Text>
          </Box>
        ) : null}
        <InputPanel
          cursorVisible={showCursor}
          disabled={controller.streaming || controller.setupSubmitting}
          label={inputLabel}
          maxLines={INPUT_MAX_LINES}
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

type StaticItem =
  | { key: 'title'; kind: 'title'; }
  | { key: string; kind: 'message'; message: ChatMessage }
  | { key: string; kind: 'spacer'; }

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

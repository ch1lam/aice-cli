import { Box, Text } from 'ink'
import { useEffect, useState } from 'react'

import type { MessageRole } from '../types/chat.js'
import type { ProviderEnv } from '../types/env.js'
import type { AppMode, SetupStep } from '../types/setup-flow.js'
import type { ProviderId } from '../types/stream.js'

import { DEFAULT_PROVIDER_ID } from '../config/provider-defaults.js'
import { useChatInputController } from './hooks/use-chat-input-controller.js'
import { InputPanel } from './input-panel.js'
import { providerOptions } from './provider-options.js'
import { SelectInput } from './select-input.js'
import { SlashSuggestions } from './slash-suggestions.js'
import { StatusBar } from './status-bar.js'
import { theme } from './theme.js'

export interface AiceAppProps {
  initialEnv?: ProviderEnv
  initialError?: Error
}

const messageColors = theme.components.messages

export function AiceApp(props: AiceAppProps) {
  const [cursorVisible, setCursorVisible] = useState(true)
  const controller = useChatInputController({
    initialEnv: props.initialEnv,
    initialError: props.initialError,
  })

  useEffect(() => {
    const intervalId = setInterval(() => {
      setCursorVisible(current => !current)
    }, 500)

    return () => {
      clearInterval(intervalId)
    }
  }, [])

  const inputLabel = '✧'
  const renderedInput = controller.maskInput ? '*'.repeat(controller.input.length) : controller.input
  const providerPrompt =
    providerOptions[controller.providerChoiceIndex]?.value ?? controller.providerSelection
  const hint = resolveHint(controller.mode, controller.setupStateStep, providerPrompt)
  const placeholder = resolvePlaceholder(controller.streaming, controller.setupSubmitting, hint)
  const showCursor = !controller.streaming && !controller.setupSubmitting && cursorVisible
  const showProviderSelect = isProviderSelectVisible(controller.mode, controller.setupStateStep)
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
          <Text color={colorForRole(message.role)} key={message.id} wrap="wrap">
            {`${labelForRole(message.role)} ${message.text}`}
          </Text>
        ))}
        {controller.streaming ? (
          <Text color={messageColors.assistant} wrap="wrap">
            {` ♤  ${controller.currentResponse || ''}${controller.sessionStatus === 'completed' || !cursorVisible ? '  ' : ' ▌'}`}
          </Text>
        ) : null}
      </Box>
      <Box flexDirection="column" width="100%">
        {showProviderSelect ? (
          <SelectInput
            active
            items={providerOptions}
            selectedIndex={controller.providerChoiceIndex}
            title="Choose a provider"
          />
        ) : (
          <>
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
          </>
        )}
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

function setupPrompt(step: SetupStep, providerId: ProviderId = DEFAULT_PROVIDER_ID): string {
  if (step === 'provider') {
    return `Use arrow keys to choose provider (current: ${providerId}). Press Enter to confirm.`
  }

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

function resolveHint(mode: AppMode, step: SetupStep, providerId: ProviderId): string {
  if (mode === 'setup') {
    return setupPrompt(step, providerId)
  }

  return 'Type a prompt or use /help, /login, /provider, /model, /clear'
}

function resolvePlaceholder(streaming: boolean, setupSubmitting: boolean, hint: string): string {
  if (streaming) return 'Processing response...'
  if (setupSubmitting) return 'Validating provider...'
  return hint
}

function isProviderSelectVisible(mode: AppMode, step: SetupStep): boolean {
  return mode === 'setup' && step === 'provider'
}

function shouldShowSlashSuggestions(
  mode: AppMode,
  streaming: boolean,
  slashCommandActive: boolean,
  suggestionCount: number,
): boolean {
  return !streaming && mode === 'chat' && slashCommandActive && suggestionCount > 0
}

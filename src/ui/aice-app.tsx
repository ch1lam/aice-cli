import { Box, Static, Text, useStdout } from 'ink'
import { useCallback, useEffect, useRef, useState } from 'react'
import stringWidth from 'string-width'

import type { ChatMessage, MessageRole } from '../types/chat.js'
import type { ProviderEnv } from '../types/env.js'
import type { AppMode, SetupStep } from '../types/setup-flow.js'

import { useChatInputController } from './hooks/use-chat-input-controller.js'
import { InputPanel } from './input-panel.js'
import { SlashSuggestions } from './slash-suggestions.js'
import { StatusBar } from './status-bar.js'
import { theme } from './theme.js'
import { wrapByWidth } from './utils.js'

export interface AiceAppProps {
  initialEnv?: ProviderEnv
  initialError?: Error
}

const messageColors = theme.components.messages
const INPUT_MAX_LINES = 6
const DEFAULT_COLUMNS = 80
const STREAM_LABEL = ' ♤ '

export function AiceApp(props: AiceAppProps) {
  const { stdout } = useStdout()
  const [columns, setColumns] = useState<number | undefined>(stdout?.columns)
  const controller = useChatInputController({
    initialEnv: props.initialEnv,
    initialError: props.initialError,
  })

  useEffect(() => {
    if (!stdout) return

    const handleResize = () => {
      setColumns(stdout.columns)
    }

    handleResize()
    stdout.on('resize', handleResize)
    return () => {
      stdout.off('resize', handleResize)
    }
  }, [stdout])

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
  const assistantLabel = labelForRole('assistant')
  const assistantIndent = ' '.repeat(stringWidth(assistantLabel))
  const streamIndent = ' '.repeat(stringWidth(STREAM_LABEL))
  const contentWidth = Math.max(
    1,
    (typeof columns === 'number' && Number.isFinite(columns) && columns > 0
      ? columns
      : DEFAULT_COLUMNS) - stringWidth(STREAM_LABEL),
  )
  const [staticItems, setStaticItems] = useState<StaticItem[]>(() => [
    { key: 'title', kind: 'title' },
  ])
  const [liveStreamLine, setLiveStreamLine] = useState<LiveStreamLine | null>(null)
  const staticKeyRef = useRef(0)
  const renderedMessageCount = useRef(0)
  const hasStaticContent = useRef(false)
  const skipNextAssistant = useRef(false)
  const streamCompletedCount = useRef(0)
  const streamLabelEmitted = useRef(false)
  const prevStreaming = useRef(false)

  const nextStaticKey = useCallback((prefix: string) => {
    const key = `${prefix}-${staticKeyRef.current}`
    staticKeyRef.current += 1
    return key
  }, [])

  const appendStaticItems = useCallback((items: StaticItem[]) => {
    if (items.length === 0) return
    setStaticItems(current => [...current, ...items])
  }, [])

  const ensureStaticSpacer = useCallback(
    (items: StaticItem[]) => {
      if (hasStaticContent.current) return
      items.push({ key: nextStaticKey('spacer'), kind: 'spacer' })
      hasStaticContent.current = true
    },
    [nextStaticKey],
  )

  useEffect(() => {
    if (controller.messages.length <= renderedMessageCount.current) return

    const newMessages = controller.messages.slice(renderedMessageCount.current)
    const items: StaticItem[] = []

    if (newMessages.length > 0) {
      ensureStaticSpacer(items)
    }

    for (const message of newMessages) {
      if (skipNextAssistant.current && message.role === 'assistant') {
        skipNextAssistant.current = false
        continue
      }

      items.push({
        key: `message-${message.id}`,
        kind: 'message',
        message,
      })
    }

    appendStaticItems(items)
    renderedMessageCount.current = controller.messages.length
  }, [appendStaticItems, controller.messages, ensureStaticSpacer])

  useEffect(() => {
    const isStreaming = controller.streaming
    const wasStreaming = prevStreaming.current

    if (isStreaming && !wasStreaming) {
      streamCompletedCount.current = 0
      streamLabelEmitted.current = false
      setLiveStreamLine(null)
    }

    if (isStreaming) {
      const streamLines = wrapByWidth(normalizedCurrentResponse, contentWidth)
      const completedLines = streamLines.slice(0, -1)
      const newCompletedCount = completedLines.length

      if (newCompletedCount > streamCompletedCount.current) {
        const newLines = completedLines.slice(streamCompletedCount.current)
        const items: StaticItem[] = []
        ensureStaticSpacer(items)

        for (const [index, line] of newLines.entries()) {
          const isFirst = !streamLabelEmitted.current && index === 0
          const prefix = isFirst ? assistantLabel : assistantIndent
          items.push({
            color: messageColors.assistant,
            key: nextStaticKey('stream-line'),
            kind: 'line',
            text: `${prefix}${line}`,
          })
        }

        if (items.length > 0) {
          appendStaticItems(items)
          streamLabelEmitted.current = true
        }

        streamCompletedCount.current = newCompletedCount
      }

      const live = streamLines.at(-1) ?? ''
      const livePrefix = streamLabelEmitted.current ? streamIndent : STREAM_LABEL
      const cursor = controller.sessionStatus === 'completed' ? '  ' : ' ▌'
      setLiveStreamLine({
        color: messageColors.assistant,
        key: 'live-stream',
        text: `${livePrefix}${live}${cursor}`,
      })
    }

    if (!isStreaming && wasStreaming) {
      const streamLines = wrapByWidth(normalizedCurrentResponse, contentWidth)
      const remainingLines = streamLines.slice(streamCompletedCount.current)

      if (remainingLines.length > 0) {
        const items: StaticItem[] = []
        ensureStaticSpacer(items)

        for (const [index, line] of remainingLines.entries()) {
          const isFirst = !streamLabelEmitted.current && index === 0
          const prefix = isFirst ? assistantLabel : assistantIndent
          items.push({
            color: messageColors.assistant,
            key: nextStaticKey('stream-line'),
            kind: 'line',
            text: `${prefix}${line}`,
          })
        }

        appendStaticItems(items)
      }

      if (normalizedCurrentResponse && controller.sessionStatus === 'completed') {
        skipNextAssistant.current = true
      }

      streamCompletedCount.current = 0
      streamLabelEmitted.current = false
      setLiveStreamLine(null)
    }

    prevStreaming.current = isStreaming
  }, [
    appendStaticItems,
    assistantIndent,
    assistantLabel,
    contentWidth,
    controller.sessionStatus,
    controller.streaming,
    ensureStaticSpacer,
    normalizedCurrentResponse,
    streamIndent,
  ])

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

          if (item.kind === 'line') {
            return (
              <Text color={item.color} key={item.key} wrap="truncate">
                {item.text}
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
        {liveStreamLine ? (
          <Box marginBottom={1}>
            <Text color={liveStreamLine.color} wrap="truncate">
              {liveStreamLine.text}
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
  | { color: string; key: string; kind: 'line'; text: string }
  | { key: 'title'; kind: 'title' }
  | { key: string; kind: 'message'; message: ChatMessage }
  | { key: string; kind: 'spacer' }

type LiveStreamLine = {
  color: string
  key: 'live-stream'
  text: string
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

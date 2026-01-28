import type { ReactNode } from 'react'

import { Box, Static, Text, useStdout } from 'ink'
import { useCallback, useEffect, useRef, useState } from 'react'
import stringWidth from 'string-width'

import type { MessageRole } from '../types/chat.js'
import type { ProviderEnv } from '../types/env.js'
import type { AppMode, SetupStep } from '../types/setup-flow.js'
import type { RenderedLine } from './markdown/render-markdown.js'

import { useChatInputController } from './hooks/use-chat-input-controller.js'
import { InputPanel } from './input-panel.js'
import { renderDisplayMarkdownLines, renderStreamMarkdown } from './markdown/render-markdown.js'
import { SelectInput } from './select-input.js'
import { SlashSuggestions } from './slash-suggestions.js'
import { StatusBar } from './status-bar.js'
import { theme } from './theme.js'

export interface AiceAppProps {
  initialEnv?: ProviderEnv
  initialError?: Error
  onNewSession?: () => void
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
    onNewSession: props.onNewSession,
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
  const hint = resolveHint(controller.mode, controller.setupStateStep, controller.modelMenu.active)
  const placeholder = resolvePlaceholder(
    controller.streaming,
    controller.setupSubmitting,
    controller.modelMenu.active,
    hint,
  )
  const showCursor =
    !controller.streaming && !controller.setupSubmitting && !controller.modelMenu.active
  const inputDisabled =
    controller.streaming || controller.setupSubmitting || controller.modelMenu.active
  const normalizedCurrentResponse = stripAssistantPadding(controller.currentResponse || '')
  const inputTopMargin = controller.streaming ? 0 : 1
  const showSlashSuggestions = shouldShowSlashSuggestions({
    mode: controller.mode,
    modelMenuActive: controller.modelMenu.active,
    slashCommandActive: controller.slashSuggestions.active,
    streaming: controller.streaming,
    suggestionCount: controller.slashSuggestions.suggestions.length,
  })
  const assistantLabel = labelForRole('assistant')
  const assistantIndent = indentForLabel(assistantLabel)
  const streamIndent = indentForLabel(STREAM_LABEL)
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

  const buildMarkdownOptions = useCallback(
    (baseColor: string) => ({
      baseColor,
      codeColor: theme.semantic.accentCode,
      contentWidth,
      headingColor: theme.semantic.accentPrimary,
      linkColor: theme.semantic.accentInfo,
      mutedColor: theme.semantic.textMuted,
    }),
    [contentWidth],
  )

  const toStaticLine = useCallback(
    (line: RenderedLine, prefix: string): StaticItem => ({
      bodyPrefix: line.bodyPrefix,
      color: line.color,
      key: nextStaticKey('line'),
      kind: 'line',
      parts: line.parts,
      prefix,
    }),
    [nextStaticKey],
  )

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

      const label = labelForRole(message.role)
      const indent = indentForLabel(label)
      const body = message.role === 'assistant' ? stripAssistantPadding(message.text) : message.text
      const lines = renderDisplayMarkdownLines(body, buildMarkdownOptions(colorForRole(message.role)))

      for (const [index, line] of lines.entries()) {
        items.push(toStaticLine(line, index === 0 ? label : indent))
      }
    }

    appendStaticItems(items)
    renderedMessageCount.current = controller.messages.length
  }, [appendStaticItems, buildMarkdownOptions, controller.messages, ensureStaticSpacer, toStaticLine])

  useEffect(() => {
    const isStreaming = controller.streaming
    const wasStreaming = prevStreaming.current

    if (isStreaming && !wasStreaming) {
      streamCompletedCount.current = 0
      setLiveStreamLine(null)
    }

    if (isStreaming) {
      const { committed, live } = renderStreamMarkdown(
        normalizedCurrentResponse,
        buildMarkdownOptions(messageColors.assistant),
      )
      const newCompletedLines = committed.slice(streamCompletedCount.current)

      if (newCompletedLines.length > 0) {
        const items: StaticItem[] = []
        ensureStaticSpacer(items)

        for (const [index, line] of newCompletedLines.entries()) {
          const lineIndex = streamCompletedCount.current + index
          const prefix = lineIndex === 0 ? assistantLabel : assistantIndent
          items.push(toStaticLine(line, prefix))
        }

        appendStaticItems(items)
      }

      streamCompletedCount.current = committed.length

      if (live) {
        const livePrefix = committed.length > 0 ? streamIndent : STREAM_LABEL
        const cursor = controller.sessionStatus === 'completed' ? '  ' : ' ▌'
        setLiveStreamLine({
          bodyPrefix: live.bodyPrefix,
          color: live.color,
          key: 'live-stream',
          parts: [...live.parts, <Text color={messageColors.caret} key="cursor">{cursor}</Text>],
          prefix: livePrefix,
        })
      } else {
        setLiveStreamLine(null)
      }
    }

    if (!isStreaming && wasStreaming) {
      const finalLines = renderDisplayMarkdownLines(
        normalizedCurrentResponse,
        buildMarkdownOptions(messageColors.assistant),
      )
      const remainingLines = finalLines.slice(streamCompletedCount.current)

      if (remainingLines.length > 0) {
        const items: StaticItem[] = []
        ensureStaticSpacer(items)

        for (const [index, line] of remainingLines.entries()) {
          const lineIndex = streamCompletedCount.current + index
          const prefix = lineIndex === 0 ? assistantLabel : assistantIndent
          items.push(toStaticLine(line, prefix))
        }

        appendStaticItems(items)
      }

      if (normalizedCurrentResponse && controller.sessionStatus === 'completed') {
        skipNextAssistant.current = true
      }

      streamCompletedCount.current = 0
      setLiveStreamLine(null)
    }

    prevStreaming.current = isStreaming
  }, [
    appendStaticItems,
    assistantIndent,
    assistantLabel,
    buildMarkdownOptions,
    controller.sessionStatus,
    controller.streaming,
    ensureStaticSpacer,
    normalizedCurrentResponse,
    streamIndent,
    toStaticLine,
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
                {item.prefix}
                {item.bodyPrefix}
                {item.parts}
              </Text>
            )
          }

          return null
        }}
      </Static>
      <Box flexDirection="column" marginTop={inputTopMargin} width="100%">
        {liveStreamLine ? (
          <Box marginBottom={1}>
            <Text color={liveStreamLine.color} wrap="truncate">
              {liveStreamLine.prefix}
              {liveStreamLine.bodyPrefix}
              {liveStreamLine.parts}
            </Text>
          </Box>
        ) : null}
        {controller.modelMenu.active ? (
          <Box marginBottom={1} width="100%">
            <SelectInput
              active
              items={controller.modelMenu.items}
              selectedIndex={controller.modelMenu.selectedIndex}
              title={controller.modelMenu.title}
            />
          </Box>
        ) : null}
        <InputPanel
          cursorVisible={showCursor}
          disabled={inputDisabled}
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

function indentForLabel(label: string): string {
  return ' '.repeat(stringWidth(label))
}

type StaticItem =
  | { bodyPrefix: string; color: string; key: string; kind: 'line'; parts: ReactNode[]; prefix: string; }
  | { key: 'title'; kind: 'title' }
  | { key: string; kind: 'spacer' }

type LiveStreamLine = {
  bodyPrefix: string
  color: string
  key: 'live-stream'
  parts: ReactNode[]
  prefix: string
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

function resolveHint(mode: AppMode, step: SetupStep, modelMenuActive: boolean): string {
  if (mode === 'setup') {
    return setupPrompt(step)
  }

  if (modelMenuActive) {
    return 'Choose a model from the menu above.'
  }

  return 'Type a prompt or use /help, /login, /model, /new'
}

function resolvePlaceholder(
  streaming: boolean,
  setupSubmitting: boolean,
  modelMenuActive: boolean,
  hint: string,
): string {
  if (streaming) return 'Processing response...'
  if (setupSubmitting) return 'Validating connection...'
  if (modelMenuActive) return 'Model menu open...'
  return hint
}

type SlashSuggestionVisibility = {
  mode: AppMode
  modelMenuActive: boolean
  slashCommandActive: boolean
  streaming: boolean
  suggestionCount: number
}

function shouldShowSlashSuggestions({
  mode,
  modelMenuActive,
  slashCommandActive,
  streaming,
  suggestionCount,
}: SlashSuggestionVisibility): boolean {
  return !streaming && !modelMenuActive && mode === 'chat' && slashCommandActive && suggestionCount > 0
}

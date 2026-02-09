import { createDeepSeek } from '@ai-sdk/deepseek'
import {
  type LanguageModel,
  type ModelMessage,
  stepCountIs,
  type TextStreamPart,
  ToolLoopAgent,
  type ToolLoopAgentSettings,
  type ToolSet,
} from 'ai'

import type { LLMProvider, SessionRequest } from '../types/session.js'
import type { ProviderStream } from '../types/stream.js'

import { createWorkspaceTools, type WorkspaceToolsFactory } from '../agents/workspace-tools.js'

type StreamTextPart = TextStreamPart<ToolSet>

type AgentStreamResult = {
  fullStream: AsyncIterable<StreamTextPart>
}

type AgentStreamOptions = {
  abortSignal?: AbortSignal
  messages: ModelMessage[]
}

type AgentStreamFn = (options: AgentStreamOptions) => PromiseLike<AgentStreamResult>

type ToolLoopAgentLike = {
  stream: AgentStreamFn
}

type ModelFactory = (modelId: string) => LanguageModel

type AgentFactory = (settings: ToolLoopAgentSettings<never, ToolSet>) => ToolLoopAgentLike

export interface DeepSeekProviderConfig {
  apiKey: string
  baseURL?: string
  maxSteps?: number
  model?: string
  workspaceRoot?: string
}

export interface DeepSeekSessionRequest extends SessionRequest {
  temperature?: number
}

type DeepSeekProviderDependencies = {
  agentFactory?: AgentFactory
  modelFactory?: ModelFactory
  toolsFactory?: WorkspaceToolsFactory
}

const DEFAULT_MAX_STEPS = 8
const DEFAULT_WORKSPACE_ROOT = process.cwd()
const AICE_AGENT_INSTRUCTIONS = [
  'You are AICE, a CLI coding agent focused on accurate, verifiable output.',
  'Use tools to inspect the workspace before claiming file contents or code behavior.',
  'If a tool reports an error, explain the failure and request a narrower follow-up.',
  'Keep answers concise and technical.',
].join('\n')

export class DeepSeekProvider implements LLMProvider<DeepSeekSessionRequest> {
  readonly id = 'deepseek' as const
  #agentFactory: AgentFactory
  #defaultModel?: string
  #maxSteps: number
  #modelFactory: ModelFactory
  #toolsFactory: WorkspaceToolsFactory
  #workspaceRoot: string

  constructor(config: DeepSeekProviderConfig, dependencies: DeepSeekProviderDependencies = {}) {
    if (!config.apiKey) {
      throw new Error('Missing DeepSeek API key')
    }

    this.#defaultModel = config.model
    this.#maxSteps = normalizeMaxSteps(config.maxSteps)
    this.#workspaceRoot = config.workspaceRoot ?? DEFAULT_WORKSPACE_ROOT
    this.#modelFactory =
      dependencies.modelFactory
      ?? createDeepSeek({ apiKey: config.apiKey, baseURL: config.baseURL })
    this.#agentFactory = dependencies.agentFactory ?? createToolLoopAgent
    this.#toolsFactory = dependencies.toolsFactory ?? createWorkspaceTools
  }

  stream(request: DeepSeekSessionRequest): ProviderStream {
    const modelId = request.model ?? this.#defaultModel

    if (!modelId) {
      throw new Error('DeepSeek model is required')
    }

    const model = this.#modelFactory(modelId)
    const agent = this.#agentFactory({
      instructions: AICE_AGENT_INSTRUCTIONS,
      model,
      stopWhen: stepCountIs(this.#maxSteps),
      temperature: request.temperature,
      tools: this.#toolsFactory({ workspaceRoot: this.#workspaceRoot }),
    })

    return streamAgent(agent, {
      abortSignal: request.signal,
      messages: request.messages,
    })
  }
}

function createToolLoopAgent(settings: ToolLoopAgentSettings<never, ToolSet>): ToolLoopAgentLike {
  return new ToolLoopAgent(settings)
}

function normalizeMaxSteps(maxSteps?: number): number {
  if (typeof maxSteps !== 'number' || !Number.isFinite(maxSteps)) {
    return DEFAULT_MAX_STEPS
  }

  return Math.max(1, Math.floor(maxSteps))
}

async function *streamAgent(
  agent: ToolLoopAgentLike,
  options: AgentStreamOptions,
): AsyncIterable<StreamTextPart> {
  const stream = await agent.stream(options)

  for await (const chunk of stream.fullStream) {
    yield chunk
  }
}

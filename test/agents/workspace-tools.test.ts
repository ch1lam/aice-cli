import type { ToolExecutionOptions, ToolSet } from 'ai'

import { expect } from 'chai'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'

import { createWorkspaceTools } from '../../src/agents/workspace-tools.ts'

const TOOL_OPTIONS: ToolExecutionOptions = {
  messages: [],
  toolCallId: 'tool-call-0',
}

describe('workspace tools', () => {
  let workspaceRoot: string

  beforeEach(async () => {
    workspaceRoot = await mkdtemp(path.join(os.tmpdir(), 'aice-tools-'))
    await mkdir(path.join(workspaceRoot, 'src'), { recursive: true })
    await writeFile(
      path.join(workspaceRoot, 'src', 'demo.ts'),
      ['line one', 'line two', 'line three'].join('\n'),
      'utf8',
    )
  })

  afterEach(async () => {
    await rm(workspaceRoot, { force: true, recursive: true })
  })

  it('reads file ranges with line numbers', async () => {
    const tools = createWorkspaceTools({ workspaceRoot })
    const readFileTool = getTool(tools, 'read_file')
    const result = await readFileTool.execute?.(
      { endLine: 3, path: 'src/demo.ts', startLine: 2 },
      TOOL_OPTIONS,
    )

    expect(result).to.include({
      endLine: 3,
      path: 'src/demo.ts',
      startLine: 2,
      totalLines: 3,
      truncated: false,
    })
    expect((result as {content: string}).content).to.equal('2: line two\n3: line three')
  })

  it('rejects traversal outside workspace root', async () => {
    const tools = createWorkspaceTools({ workspaceRoot })
    const readFileTool = getTool(tools, 'read_file')
    const result = await readFileTool.execute?.({ path: '../secrets.txt' }, TOOL_OPTIONS)

    expect((result as {error: string}).error).to.contain('outside workspace root')
  })
})

function getTool(tools: ToolSet, toolName: string) {
  const tool = tools[toolName]
  expect(tool, `Missing tool ${toolName}`).to.not.equal(undefined)
  return tool
}

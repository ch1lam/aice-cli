import { tool, type ToolSet } from 'ai'
import { execFile } from 'node:child_process'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { promisify } from 'node:util'
import { z } from 'zod'

const DEFAULT_MAX_FILE_CHARS = 12_000
const DEFAULT_MAX_LIST_RESULTS = 200
const DEFAULT_MAX_SEARCH_RESULTS = 40
const RG_TIMEOUT_MS = 6000
const RG_MAX_BUFFER = 8 * 1024 * 1024
const execFileAsync = promisify(execFile)

export interface WorkspaceToolsFactoryOptions {
  workspaceRoot?: string
}

export type WorkspaceToolsFactory = (options?: WorkspaceToolsFactoryOptions) => ToolSet

export function createWorkspaceTools(options: WorkspaceToolsFactoryOptions = {}): ToolSet {
  const workspaceRoot = path.resolve(options.workspaceRoot ?? process.cwd())

  return {
    'get_current_time': tool({
      description: 'Get the current local and UTC timestamps.',
      async execute() {
        return {
          local: new Date().toString(),
          timezoneOffsetMinutes: new Date().getTimezoneOffset(),
          utc: new Date().toISOString(),
        }
      },
      inputSchema: z.object({}),
    }),

    'list_files': tool({
      description: 'List files under a workspace path.',
      async execute(input) {
        const safePath = resolveSafePath(workspaceRoot, input.path ?? '.')
        if (!safePath) {
          return {
            error: `Path is outside workspace root (${workspaceRoot}).`,
          }
        }

        const targetPath = toRelativePath(workspaceRoot, safePath)
        const result = await runRipgrep(['--files', targetPath], workspaceRoot)
        const lines = result.stdout.split('\n').map(line => line.trim()).filter(Boolean)
        const limit = input.maxResults ?? DEFAULT_MAX_LIST_RESULTS
        const files = lines.slice(0, limit)

        return {
          fileCount: files.length,
          files,
          root: workspaceRoot,
          totalMatches: lines.length,
          truncated: lines.length > files.length,
        }
      },
      inputSchema: z.object({
        maxResults: z.number().int().min(1).max(1000).optional(),
        path: z.string().min(1).optional(),
      }),
    }),

    'read_file': tool({
      description: 'Read a UTF-8 text file from the workspace with optional line range.',
      async execute(input) {
        const safePath = resolveSafePath(workspaceRoot, input.path)
        if (!safePath) {
          return {
            error: `Path is outside workspace root (${workspaceRoot}).`,
          }
        }

        let contents: string
        try {
          contents = await readFile(safePath, 'utf8')
        } catch (error) {
          return {
            error: `Failed to read file: ${describeError(error)}`,
            path: toRelativePath(workspaceRoot, safePath),
          }
        }

        const lines = contents.split(/\r?\n/)
        const startLine = input.startLine ?? 1
        const requestedEndLine = input.endLine ?? lines.length
        const endLine = Math.min(requestedEndLine, lines.length)

        if (endLine < startLine) {
          return {
            error: 'endLine must be greater than or equal to startLine.',
            path: toRelativePath(workspaceRoot, safePath),
          }
        }

        const selected = lines.slice(startLine - 1, endLine)
        const numberedContent = selected.map((line, index) => `${startLine + index}: ${line}`).join('\n')
        const limit = input.maxChars ?? DEFAULT_MAX_FILE_CHARS
        const { truncated, value } = truncateText(numberedContent, limit)

        return {
          content: value,
          endLine,
          path: toRelativePath(workspaceRoot, safePath),
          startLine,
          totalLines: lines.length,
          truncated,
        }
      },
      inputSchema: z.object({
        endLine: z.number().int().min(1).optional(),
        maxChars: z.number().int().min(200).max(100_000).optional(),
        path: z.string().min(1),
        startLine: z.number().int().min(1).optional(),
      }),
    }),

    'search_files': tool({
      description: 'Search text in workspace files using ripgrep.',
      async execute(input) {
        const safePath = resolveSafePath(workspaceRoot, input.path ?? '.')
        if (!safePath) {
          return {
            error: `Path is outside workspace root (${workspaceRoot}).`,
          }
        }

        const targetPath = toRelativePath(workspaceRoot, safePath)
        const result = await runRipgrep(
          [
            '--color',
            'never',
            '--column',
            '--line-number',
            '--no-heading',
            '--smart-case',
            input.query,
            targetPath,
          ],
          workspaceRoot,
        )

        const lines = result.stdout.split('\n').map(line => line.trim()).filter(Boolean)
        const limit = input.maxResults ?? DEFAULT_MAX_SEARCH_RESULTS
        const matches = lines.slice(0, limit)

        return {
          matches,
          query: input.query,
          totalMatches: lines.length,
          truncated: lines.length > matches.length,
        }
      },
      inputSchema: z.object({
        maxResults: z.number().int().min(1).max(1000).optional(),
        path: z.string().min(1).optional(),
        query: z.string().min(1),
      }),
    }),
  }
}

function resolveSafePath(workspaceRoot: string, inputPath: string): string | undefined {
  const resolvedPath = path.resolve(workspaceRoot, inputPath)
  const relative = path.relative(workspaceRoot, resolvedPath)

  if (relative === '') return resolvedPath
  if (relative.startsWith('..') || path.isAbsolute(relative)) return undefined
  return resolvedPath
}

function toRelativePath(workspaceRoot: string, inputPath: string): string {
  const relative = path.relative(workspaceRoot, inputPath)
  return relative === '' ? '.' : relative
}

async function runRipgrep(args: string[], cwd: string): Promise<{stdout: string}> {
  try {
    const result = await execFileAsync('rg', args, {
      cwd,
      encoding: 'utf8',
      maxBuffer: RG_MAX_BUFFER,
      timeout: RG_TIMEOUT_MS,
    })

    return { stdout: result.stdout }
  } catch (error) {
    if (isNoMatchExit(error)) {
      return { stdout: '' }
    }

    throw new Error(`Failed to execute ripgrep: ${describeError(error)}`)
  }
}

function isNoMatchExit(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  if (!('code' in error)) return false
  const { code } = error as {code?: unknown}
  return code === 1
}

function truncateText(value: string, limit: number): {truncated: boolean; value: string} {
  if (value.length <= limit) {
    return { truncated: false, value }
  }

  return {
    truncated: true,
    value: `${value.slice(0, limit)}\n...<truncated>`,
  }
}

function describeError(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

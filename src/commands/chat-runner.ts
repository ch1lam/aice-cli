import type {SessionStream, SessionStreamChunk} from '../core/stream.js'

export interface ChatIO {
  error?(error: Error): never | void
  log(message: string): void
  write(text: string): void
}

export async function renderStream(stream: SessionStream, io: ChatIO): Promise<void> {
  for await (const chunk of stream) {
    renderChunk(chunk, io)
  }
}

export function renderChunk(chunk: SessionStreamChunk, io: ChatIO): void {
  switch (chunk.type) {
    case 'done': {
      io.log('')
      break
    }

    case 'error': {

      if (io.error) {
        io.error(chunk.error)
      } else {
        io.log(chunk.error.message)
      }

      break
    }

    case 'meta': {
      io.log(`provider=${chunk.providerId} model=${chunk.model}`)
      break
    }

    case 'status': {
      io.log(`status=${chunk.status}${chunk.detail ? ` detail=${chunk.detail}` : ''}`)
      break
    }

    case 'text': {
      io.write(chunk.text)
      break
    }

    case 'usage': {
      io.log(
        `\nusage input=${chunk.usage.inputTokens ?? '-'} output=${chunk.usage.outputTokens ?? '-'} total=${chunk.usage.totalTokens ?? '-'}`,
      )
      break
    }
  }
}

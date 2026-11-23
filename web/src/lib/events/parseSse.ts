// Incremental Server Sent Events parser (WHATWG rules), no dependencies.

export interface SseFrame {
  id: string | null
  event: string
  data: string
}

export class SseParser {
  private decoder = new TextDecoder('utf-8')
  private buffer = ''
  private first = true
  private dataLines: string[] = []
  private eventName = ''
  private lastId: string | null = null
  /** Counts complete lines seen, comments included. Used as "there was activity". */
  activity = 0

  push(chunk: Uint8Array): SseFrame[] {
    let text = this.decoder.decode(chunk, { stream: true })
    if (this.first && text.length > 0) {
      this.first = false
      if (text.charCodeAt(0) === 0xfeff) text = text.slice(1)
    }
    this.buffer += text
    const frames: SseFrame[] = []
    for (;;) {
      let idx = -1
      for (let i = 0; i < this.buffer.length; i++) {
        const c = this.buffer.charCodeAt(i)
        if (c === 10 || c === 13) {
          idx = i
          break
        }
      }
      if (idx < 0) break
      const isCR = this.buffer.charCodeAt(idx) === 13
      // A trailing CR may be the first half of CRLF: wait for the next chunk.
      if (isCR && idx === this.buffer.length - 1) break
      const skip = isCR && this.buffer.charCodeAt(idx + 1) === 10 ? 2 : 1
      const line = this.buffer.slice(0, idx)
      this.buffer = this.buffer.slice(idx + skip)
      const frame = this.line(line)
      if (frame) frames.push(frame)
    }
    return frames
  }

  /** Stream ended: a trailing partial frame is discarded. */
  end(): void {
    this.decoder.decode()
    this.buffer = ''
    this.dataLines = []
    this.eventName = ''
  }

  private line(line: string): SseFrame | null {
    this.activity++
    if (line === '') {
      if (this.dataLines.length === 0) {
        this.eventName = ''
        return null
      }
      const frame: SseFrame = {
        id: this.lastId,
        event: this.eventName || 'message',
        data: this.dataLines.join('\n'),
      }
      this.dataLines = []
      this.eventName = ''
      return frame
    }
    if (line.startsWith(':')) return null
    const colon = line.indexOf(':')
    const field = colon < 0 ? line : line.slice(0, colon)
    let value = colon < 0 ? '' : line.slice(colon + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    switch (field) {
      case 'data':
        this.dataLines.push(value)
        break
      case 'event':
        this.eventName = value
        break
      case 'id':
        if (!value.includes('\0')) this.lastId = value
        break
    }
    return null
  }
}

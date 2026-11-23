import { describe, expect, it } from 'vitest'
import { SseParser } from './parseSse'

const enc = new TextEncoder()
const parse = (s: string) => new SseParser().push(enc.encode(s))

describe('SseParser', () => {
  it('parses id, event and data', () => {
    expect(parse('id: 42\nevent: metric\ndata: {"a":1}\n\n')).toEqual([
      { id: '42', event: 'metric', data: '{"a":1}' },
    ])
  })

  it('defaults the event to message and joins multi-line data', () => {
    expect(parse('data: a\ndata: b\n\n')).toEqual([{ id: null, event: 'message', data: 'a\nb' }])
  })

  it('ignores comments and unknown fields, drops frames without data', () => {
    expect(parse(': keepalive\n\nretry: 5\nfoo: bar\n\n')).toEqual([])
  })

  it('handles CRLF, lone CR and a CR at a chunk end', () => {
    expect(parse('data: x\r\n\r\ndata: y\r\r\n')).toHaveLength(2)
    const p = new SseParser()
    expect(p.push(enc.encode('data: x\r'))).toEqual([])
    expect(p.push(enc.encode('\n\r'))).toEqual([])
    expect(p.push(enc.encode('\n'))).toHaveLength(1)
  })

  it('removes only one leading space and treats a colonless line as empty value', () => {
    expect(parse('data:  two\ndata\n\n')[0].data).toBe(' two\n')
  })

  it('strips a BOM', () => {
    expect(parse('﻿data: x\n\n')).toHaveLength(1)
  })

  it('splits a multi-byte character at every byte offset', () => {
    const bytes = enc.encode('event: e\ndata: price €5\n\n')
    for (let i = 1; i < bytes.length; i++) {
      const p = new SseParser()
      const frames = [...p.push(bytes.slice(0, i)), ...p.push(bytes.slice(i))]
      expect(frames).toEqual([{ id: null, event: 'e', data: 'price €5' }])
    }
  })

  it('discards a trailing partial frame at end', () => {
    const p = new SseParser()
    expect(p.push(enc.encode('data: partial\n'))).toEqual([])
    p.end()
    expect(p.push(enc.encode('\n'))).toEqual([])
  })

  it('counts comment lines as activity', () => {
    const p = new SseParser()
    p.push(enc.encode(': keepalive\n'))
    expect(p.activity).toBe(1)
  })
})

import { describe, expect, it } from 'vitest'

import golden from '../../../internal/web/testdata/frames.golden.json'
import { frameSchema, parseFrame, payloadSchemas } from './wire'

const frames = golden as Record<string, unknown>

describe('the wire contract', () => {
  it('carries at least the M1 frames', () => {
    for (const type of [
      'bootstrap',
      'session_snapshot',
      'session_status',
      'message',
      'file',
      'shell_output',
      'shell_output_done',
      'run_complete',
    ]) {
      expect(frames[type], type).toBeDefined()
    }
  })

  // The key of an entry is its frame type, or "type_variant" when one type
  // has two shapes (a shell chunk and its end), so the schema is looked up by
  // the frame's own type.
  for (const [name, frame] of Object.entries(frames)) {
    it(`parses the ${name} entry`, () => {
      const parsed = frameSchema.parse(frame)
      const schema = payloadSchemas[parsed.type]
      expect(schema, `no schema is registered for ${parsed.type}`).toBeDefined()
      expect(() => schema.parse(parsed.payload)).not.toThrow()
      expect(parseFrame(JSON.stringify(frame)).type).toBe(parsed.type)
    })
  }

  it('rejects a frame type it does not know', () => {
    expect(() => parseFrame(JSON.stringify({ seq: 1, type: 'not_a_frame', payload: {} }))).toThrow(
      /unknown frame type/,
    )
  })

  it('registers the event frames the golden does not carry yet', () => {
    for (const type of ['skills_event', 'host_theme', 'client_session']) {
      const frame = { seq: 1, type, payload: { type: 'created', payload: {} } }
      expect(() => parseFrame(JSON.stringify(frame)), type).not.toThrow()
      expect(payloadSchemas[type], type).toBeDefined()
    }
  })

  it('exposes the session of a web_error frame, which is global', () => {
    const parsed = parseFrame(
      JSON.stringify({ seq: 1, type: 'web_error', payload: { session_id: 's1', code: 'agent_busy', error: 'busy' } }),
    )
    expect(parsed.sessionId).toBeUndefined()
    expect(parsed.payload).toMatchObject({ session_id: 's1', code: 'agent_busy' })
  })

  it('accepts payload fields the golden does not show', () => {
    const done = { seq: 1, type: 'shell_output', payload: { run_id: 'r', session_id: 's1', done: true, exit_code: 0 } }
    expect(() => parseFrame(JSON.stringify(done))).not.toThrow()

    const signin = { seq: 1, type: 'signin', payload: { flow_id: 'f1' } }
    expect(() => parseFrame(JSON.stringify(signin))).not.toThrow()

    const extra = {
      seq: 1,
      type: 'session_status',
      payload: { session_id: 's1', status: 'idle', extra_field: true },
    }
    expect(() => parseFrame(JSON.stringify(extra))).not.toThrow()
  })

  it('rejects a payload the schema cannot type', () => {
    expect(() =>
      parseFrame(JSON.stringify({ seq: 1, type: 'session_status', payload: { session_id: 's1', status: 'napping' } })),
    ).toThrow()
  })
})

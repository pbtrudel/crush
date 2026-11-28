import { z, type ZodTypeAny } from 'zod'

export const sessionStatusSchema = z.enum(['idle', 'running', 'queued', 'pending_interaction'])
export type SessionStatus = z.infer<typeof sessionStatusSchema>

const sessionSchema = z
  .object({
    id: z.string(),
    parent_session_id: z.string().optional().default(''),
    title: z.string(),
    message_count: z.number(),
    updated_at: z.number(),
  })
  .passthrough()

export const sessionRowSchema = sessionSchema.extend({ status: sessionStatusSchema })
export type SessionRow = z.infer<typeof sessionRowSchema>

export const modelInfoSchema = z
  .object({
    id: z.string(),
    name: z.string(),
    provider: z.string(),
    context_window: z.number(),
    agent_ready: z.boolean(),
  })
  .passthrough()

export const serverInfoSchema = z.object({ state: z.string() }).passthrough()

export const bootstrapSchema = z
  .object({
    version: z.string(),
    working_dir: z.string(),
    branch: z.string().optional(),
    current_session_id: z.string(),
    sessions: z.array(sessionRowSchema),
    model: modelInfoSchema,
    agent_ready: z.boolean(),
    permission_mode: z.string(),
    input_mode: z.enum(['plan', 'code']),
    lsp: z.record(z.string(), serverInfoSchema),
    mcp: z.record(z.string(), serverInfoSchema),
    agent_runs: z.array(z.unknown()),
  })
  .passthrough()
export type Bootstrap = z.infer<typeof bootstrapSchema>

export const messageSchema = z
  .object({
    id: z.string(),
    role: z.string(),
    session_id: z.string(),
    parts: z.array(z.object({ type: z.string(), data: z.unknown() })),
    created_at: z.number(),
    updated_at: z.number(),
  })
  .passthrough()
export type WireMessage = z.infer<typeof messageSchema>

export const sessionSnapshotSchema = z
  .object({
    session_id: z.string(),
    session: sessionSchema.optional(),
    messages: z.array(messageSchema),
    has_more: z.boolean(),
    files: z.array(z.object({ path: z.string() }).passthrough()),
    busy: z.boolean(),
    queued: z.array(z.string()),
  })
  .passthrough()
export type SessionSnapshot = z.infer<typeof sessionSnapshotSchema>

// webErrorSchema is the payload of a web_error frame. The frame is global, so
// the session it concerns arrives here and is what the tab filters on before
// it shows the message.
export const webErrorSchema = z
  .object({ session_id: z.string(), code: z.string(), error: z.string() })
  .passthrough()
export type WebError = z.infer<typeof webErrorSchema>

// signInStateSchema is the payload of a signin frame. Only the flow is
// always there: the gateway omits every field it has nothing to say about,
// and a flow that ended says why in the fields beside it.
export const signInStateSchema = z
  .object({
    flow_id: z.string(),
    provider: z.string().optional(),
    provider_name: z.string().optional(),
    state: z.string().optional(),
    kind: z.string().optional(),
    user_code: z.string().optional(),
    verification_url: z.string().optional(),
    expires_at: z.number().optional(),
    code_entry: z.boolean().optional(),
    error: z.string().optional(),
    model_applied: z.boolean().optional(),
    imported: z.boolean().optional(),
  })
  .passthrough()
export type SignInState = z.infer<typeof signInStateSchema>

// sessionStatusPayloadSchema is the payload of a session_status frame: the
// row's run state, which is the one place the tab reads it from.
export const sessionStatusPayloadSchema = z
  .object({ session_id: z.string(), status: sessionStatusSchema })
  .passthrough()
export type SessionStatusPayload = z.infer<typeof sessionStatusPayloadSchema>

// eventEnvelope wraps the pubsub envelope every event-derived payload
// carries. A session the workspace no longer has answers an empty entity,
// which is why the snapshot's session field is optional.
const eventEnvelope = <T extends ZodTypeAny>(payload: T) =>
  z.object({ type: z.string(), payload }).passthrough()

// The two envelopes the store reduces keep their type: a message frame and a
// session frame are the ones whose payload a reducer reads field by field.
export const messageEventSchema = eventEnvelope(messageSchema)
export type MessageEvent = z.infer<typeof messageEventSchema>

export const sessionEventSchema = eventEnvelope(sessionSchema)
export type SessionEvent = z.infer<typeof sessionEventSchema>

// Payload fields the golden file does not carry yet still pass: the schemas
// pin the fields the tab reads, never the whole shape.
export const payloadSchemas: Record<string, ZodTypeAny> = {
  bootstrap: bootstrapSchema,
  session_snapshot: sessionSnapshotSchema,
  session_status: sessionStatusPayloadSchema,
  session_follow: z.object({ session_id: z.string() }).passthrough(),
  input_mode_changed: z.object({ mode: z.enum(['plan', 'code']) }).passthrough(),
  plan_ready: z.object({ session_id: z.string() }).passthrough(),
  team_state: z.object({ team_id: z.string() }).passthrough(),
  agent_runs: z.array(z.unknown()),
  model_changed: modelInfoSchema,
  gateway_closing: z.object({ reason: z.string() }).passthrough(),
  web_error: webErrorSchema,
  secure_prompt: z.object({ prompts: z.array(z.unknown()) }).passthrough(),
  signin: signInStateSchema,
  // A shell run streams chunks and then one end frame of the same type, so
  // the schema accepts either shape. Only an end frame carries the fixed
  // failure sentence, and only when the run could not start.
  shell_output: z.union([
    z.object({ run_id: z.string(), session_id: z.string(), chunk: z.string() }).passthrough(),
    z
      .object({
        run_id: z.string(),
        session_id: z.string(),
        done: z.boolean(),
        exit_code: z.number(),
        error: z.string().optional(),
      })
      .passthrough(),
  ]),
  message: messageEventSchema,
  file: eventEnvelope(z.object({ session_id: z.string(), path: z.string() }).passthrough()),
  run_complete: eventEnvelope(z.object({ session_id: z.string(), message_id: z.string() }).passthrough()),
  session: sessionEventSchema,
  permission_request: eventEnvelope(z.object({ session_id: z.string() }).passthrough()),
  permission_notification: eventEnvelope(z.object({ session_id: z.string() }).passthrough()),
  permission_mode_changed: eventEnvelope(z.object({ mode: z.string() }).passthrough()),
  question_batch_request: eventEnvelope(z.object({ session_id: z.string() }).passthrough()),
  question_batch_notification: eventEnvelope(z.object({ session_id: z.string() }).passthrough()),
  agent_event: eventEnvelope(z.unknown()),
  lsp_event: eventEnvelope(z.unknown()),
  mcp_event: eventEnvelope(z.unknown()),
  config_stale: eventEnvelope(z.unknown()),
  config_changed: eventEnvelope(z.unknown()),
  update_available: eventEnvelope(z.unknown()),
  skills_event: eventEnvelope(z.unknown()),
  host_theme: eventEnvelope(z.unknown()),
  client_session: eventEnvelope(z.unknown()),
}

export const frameSchema = z
  .object({
    seq: z.number(),
    type: z.string(),
    session_id: z.string().optional(),
    payload: z.unknown(),
  })
  .passthrough()

export interface ParsedFrame {
  seq: number
  type: string
  sessionId?: string
  payload: unknown
}

// parseFrame validates one frame at the boundary. An unknown type is a
// contract break, so it throws instead of rendering nothing.
export function parseFrame(raw: string): ParsedFrame {
  const frame = frameSchema.parse(JSON.parse(raw))
  const schema = payloadSchemas[frame.type]
  if (!schema) {
    throw new Error(`unknown frame type ${frame.type}`)
  }
  const payload = schema.parse(frame.payload)
  return { seq: frame.seq, type: frame.type, sessionId: frame.session_id, payload }
}

export type EventKind = 'created' | 'updated' | 'deleted';
export interface WireEvent<T> { type: EventKind; payload: T }
export interface Frame { seq: number; type: string; payload: unknown }

export type Part =
  | { type: 'text'; data: { text: string; hidden?: boolean } }
  | { type: 'reasoning'; data: { thinking: string; started_at?: number; finished_at?: number } }
  | { type: 'tool_call'; data: { id: string; name: string; input: string; finished?: boolean } }
  | { type: 'tool_result'; data: ToolResultData }
  | { type: 'finish'; data: { reason: string; time: number; message?: string; details?: string } }
  | { type: 'image_url'; data: { url: string; detail?: string } }
  | { type: 'binary'; data: { path?: string; mime_type?: string } }
  | { type: 'shell_command'; data: { command: string; output: string; exit_code: number } }
  | { type: 'compaction_stats'; data: { tokens_before: number; saved_tokens: number; kept_messages: number } }
  | { type: 'sub_agent_report'; data: { label: string; output: string; failed?: boolean; model?: string } };

export interface ToolResultData {
  tool_call_id: string; name: string; content: string; data?: string;
  mime_type?: string; metadata: string; is_error: boolean;
}

export type Role = 'user' | 'assistant' | 'system' | 'tool';
export interface Message {
  id: string; role: Role; session_id: string; parts: Part[];
  model: string; provider: string; created_at: number; updated_at: number;
  is_summary_message?: boolean;
}
export interface Todo { content: string; status: string; active_form: string }
export interface Session {
  id: string; parent_session_id: string; title: string; message_count: number;
  prompt_tokens: number; completion_tokens: number; cost: number; todos?: Todo[];
  is_busy: boolean; created_at: number; updated_at: number;
}
export interface PermissionRequest {
  id: string; session_id: string; tool_call_id: string; tool_name: string;
  description: string; action: string; params: unknown; path: string; danger?: string;
}
export interface PermissionNotification {
  session_id: string; tool_call_id: string; granted: boolean; denied: boolean; cancelled: boolean;
}
export interface QuestionChoice { id: string; label: string; description?: string }
export type QuestionType = 'yes_no' | 'single_choice' | 'multi_choice' | 'free_text';
export interface Question {
  id: string; type: QuestionType; label?: string; question: string;
  description?: string; choices?: QuestionChoice[];
}
export interface QuestionRequest {
  id: string; session_id: string; tool_call_id: string; questions: Question[];
  confirm_title?: string; confirm_description?: string;
}
export interface QuestionAnswer {
  question_id: string; selected_ids?: string[]; fill_in_text?: string; yes?: boolean;
  notes?: Record<string, string>;
}
export type PermissionMode = 'normal' | 'yolo' | 'sysadmin';
export type InputMode = 'plan' | 'code';
export interface ModelInfo { id: string; name: string; provider: string; context_window: number }
export interface ServerInfo { state: string; diagnostic_count?: number; tool_count?: number; error?: string }
export interface Snapshot {
  version: string; working_dir: string; branch?: string; current_session_id: string;
  session?: Session; messages: Message[]; permissions: PermissionRequest[];
  questions?: QuestionRequest[]; permission_mode: PermissionMode; input_mode: InputMode; model: ModelInfo;
  busy: boolean; queued: string[];
  lsp?: Record<string, ServerInfo>; mcp?: Record<string, ServerInfo>;
}

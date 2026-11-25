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
export interface ModelRef { id: string; provider: string }
export interface ModelInfo {
  id: string; name: string; provider: string; context_window: number;
  can_reason?: boolean; effort_levels?: string[]; reasoning_effort?: string; think?: boolean;
  small?: ModelRef; agent_ready: boolean;
}
export interface ModelChoice {
  provider: string; provider_name: string; id: string; name: string; context_window: number;
  can_reason: boolean; effort_levels?: string[]; default_effort?: string;
}
export interface ServerInfo { state: string; diagnostic_count?: number; tool_count?: number; error?: string }
export type SecureKind = 'passphrase' | 'pin' | 'password' | 'confirm' | 'touch';
export interface SecurePrompt {
  source: 'pinentry' | 'ssh'; id: string; kind: SecureKind; title: string; message: string;
  details?: string[]; retry?: string;
}
export interface SignInTarget {
  id: string; name: string; configured: boolean; methods: ('oauth' | 'api_key')[];
  oauth_kind?: 'device' | 'browser';
}
export type SignInPhase = 'waiting' | 'saving' | 'done' | 'error' | 'cancelled';
export interface SignInState {
  flow_id: string; provider: string; provider_name: string; state: SignInPhase;
  kind?: 'device' | 'browser'; user_code?: string; verification_url?: string; expires_at?: number;
  code_entry?: boolean; error?: string; model_applied?: boolean; imported?: boolean;
}
export interface ModelTarget { type: 'main' | 'small'; model: string }
export interface SignInResult {
  signed_in: true; model_applied: boolean;
  reason?: 'agent_busy' | 'agent_starting' | 'model_unavailable' | 'model_failed';
}
// AwsSso is the AWS SSO refresh card. A snapshot carries only the command
// and the URL; an error reaches the card from the result frame.
export interface AwsSso { command: string; url?: string; error?: string }
// Theme names the tab's look: the translucent Holodeck glass or the solid
// glass with neither blur nor scan lines.
export type Theme = 'holodeck' | 'solid';
// DisplaySettings is what the settings dialog writes and what the gateway
// publishes after every write. The empty motion follows the tab's own
// remembered motion, then the browser's reduced motion setting.
export interface DisplaySettings {
  theme: Theme; motion: '' | 'full' | 'reduced' | 'off'; low_power: boolean; cinematic_hint: boolean;
}
export interface ConfigView { display: DisplaySettings; agent_teams: boolean }
export interface Snapshot {
  version: string; working_dir: string; branch?: string; current_session_id: string;
  session?: Session; messages: Message[]; permissions: PermissionRequest[];
  questions?: QuestionRequest[]; permission_mode: PermissionMode; input_mode: InputMode; model: ModelInfo;
  secure_prompts?: SecurePrompt[]; signin?: SignInState; aws_sso?: { command: string; url?: string };
  agent_ready: boolean; plan_ready_session_id?: string;
  // display is the tab's settings. A snapshot without it keeps whatever the
  // tab already shows.
  display?: DisplaySettings;
  busy: boolean; queued: string[];
  lsp?: Record<string, ServerInfo>; mcp?: Record<string, ServerInfo>;
  files?: FileSummary[]; team?: TeamState; agent_runs?: AgentRun[];
}

export interface FileSummary {
  path: string; versions: number; version_ids: number[]; additions: number; removals: number;
  diagnostics: { error: number; warning: number }; updated_at: number; session_ids: string[];
  stats_unknown?: boolean;
}
export interface FileDiff {
  path: string; from: number; to: number; old: string; new: string; unified: string;
  language: string; truncated: boolean;
}
export type MemberStatus = 'running' | 'inactive' | 'provisioning' | 'failed';
export interface TeamMember {
  id: string; name: string; role: 'lead' | 'teammate'; description?: string; model?: string;
  model_provider?: string; session_id?: string; parent_session_id?: string; status?: MemberStatus;
  failed?: boolean; diagnostics?: string[];
}
export type TaskStatus = 'pending' | 'in_progress' | 'completed';
export interface TeamTask {
  id: string; revision: number; subject: string; description: string; status: TaskStatus;
  owner_id?: string; owner_name?: string; blocked_by: string[]; ready: boolean; session_id?: string;
}
export interface TeamState { team_id: string; members: TeamMember[]; tasks: TeamTask[] }
export type RunStatus = 'running' | 'done' | 'failed';
export interface AgentRun {
  id: string; kind: 'subagent' | 'teammate'; name: string; session_id?: string;
  parent_session_id?: string; status: RunStatus; started_at: string; finished_at?: string;
}

export interface CommandArg { id: string; title: string; description?: string; required: boolean }
export interface CommandEntry {
  id: string; title: string; description?: string; kind: 'custom' | 'skill'; args: CommandArg[];
}
export interface McpPrompt {
  id: string; client_id: string; prompt_id: string; title: string; description?: string; args: CommandArg[];
}
export interface SkillEntry { name: string; description?: string; disabled: boolean; disabled_globally: boolean }
export interface McpServer {
  name: string; state: string; tools: number; prompts: number; error?: string;
  disabled: boolean; needs_auth: boolean; channel: boolean;
}
export interface DockerMcp { available: boolean; enabled: boolean }
export interface DiagnosticCounts { error: number; warning: number; information: number; hint: number }
export interface LspServer { name: string; state: string; error?: string; diagnostics: DiagnosticCounts }
export interface ShellOutput {
  run_id: string; session_id: string; chunk?: string; done?: boolean; exit_code?: number; error?: string;
}
export interface WebError { session_id?: string; code?: string; error: string }
// AgentEvent is the notification the agent publishes about a run: a finished
// turn, an error, or a sign-in the terminal has to perform.
export interface AgentEvent {
  type: string;
  session_id?: string;
  session_title?: string;
  error?: string;
  run_id?: string;
  provider_id?: string;
  aws_sso_command?: string;
  aws_sso_url?: string;
}
export interface UpdateAvailable {
  current_version: string; latest_version: string; is_development: boolean;
}
export interface Attachment { name: string; mime_type: string; content: string }

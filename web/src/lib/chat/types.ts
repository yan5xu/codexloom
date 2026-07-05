// AI Agent types — aligned with backend /ai/ API (docs/ai-api.md)

export type ChatRole = "user" | "assistant" | "tool";

/** Agent configuration. */
export interface ChatAgent {
  id: string;
  name: string;
  system_prompt: string;
  pinned: string[];
  scope?: string[];
  temperature: number;
  max_tokens: number;
  created_at: string;
  updated_at: string;
}

/** Conversation thread belonging to an agent. */
export interface ChatTopic {
  id: string;
  agent_id: string;
  title: string;
  mode?: string;
  turn_count?: number;
  active_turn_id?: string | null;
  pinned?: boolean;
  archived?: boolean;
  created_at: string;
  updated_at: string;
}

/** A single message in a topic. */
export interface ChatMessage {
  id: string;
  topic_id: string;
  turn_id?: string;
  role: ChatRole;
  content: string;
  reasoning?: string;
  tool_call_id?: string;
  tool_name?: string;
  tool_args?: string;
  usage?: string;
  created_at: string;
}

/** A single user-message + AI-response cycle. */
export interface ChatTurn {
  id: string;
  topic_id: string;
  mode?: string;
  status: "running" | "done" | "error" | "cancelled";
  prompt_tokens?: number;
  completion_tokens?: number;
  cache_hit_tokens?: number;
  cache_miss_tokens?: number;
  reasoning_tokens?: number;
  clip_invocations?: number;
  cost_credits?: number;
  started_at: string;
  finished_at?: string | null;
}

/** Token usage breakdown for a single LLM call. */
export interface TokenUsage {
  prompt_tokens?: number;
  prompt_cache_hit_tokens?: number;
  prompt_cache_miss_tokens?: number;
  completion_tokens?: number;
  reasoning_tokens?: number;
  total_tokens?: number;
}

/** SSE event from GET /ai/turns/{id}/events. */
export interface AgentStreamEvent {
  type: "turn_start" | "text" | "thinking" | "tool_call" | "tool_result" | "usage" | "done" | "error";
  turn_id?: string;
  topic_id?: string;
  content?: string;
  id?: string;
  name?: string;
  arguments?: string;
  prompt_tokens?: number;
  prompt_cache_hit_tokens?: number;
  prompt_cache_miss_tokens?: number;
  completion_tokens?: number;
  reasoning_tokens?: number;
  total_tokens?: number;
  error?: string;
}

/** Platform-configured mode (e.g. "flash", "pro"). */
export interface ModeConfig {
  id: string;
  name: string;
  description: string;
  model: string;
  default: boolean;
}

export interface AgentSchedule {
  id: string;
  agent_id: string;
  user_id: string;
  topic_id: string;
  cron_expr: string;
  message: string;
  timezone: string;          // e.g. "Asia/Shanghai"
  enabled: boolean;
  last_run_at?: string;
  next_run_at: string;
  created_at: string;
  agent_name?: string;       // enriched by API
  topic_title?: string;      // enriched by API
}

export interface LoomEvent {
  seq: number;
  ts: string;
  type: string;
  data: any;
}

export interface Approval {
  approvalId: string;
  method: string;
  params: any;
  ts?: string;
}

export interface Agent {
  id: string;
  name: string;
  cwd: string;
  threadId: string;
  sandbox: string;
  approvalPolicy: string;
  model?: string;
  effort?: string;
  profileVersionSeen?: number;
  status: string;
  currentTask: string;
  currentTurnId: string;
  lastError: string;
  createdAt: string;
  updatedAt: string;
  processAlive: boolean;
  pendingApprovals: Approval[];
  lastSeq: number;
}

export interface RemoteConfig {
  enabled: boolean;
  updatedAt?: string;
}

export interface RemoteStatus {
  state: "disabled" | "starting" | "connecting" | "connected" | "error" | string;
  serverName?: string;
  systemHostname: string;
  installationId?: string;
  environmentId?: string;
  codexPath?: string;
  lastError?: string;
  updatedAt: string;
}

export interface RemotePairing {
  pairingCode: string;
  manualPairingCode?: string;
  environmentId: string;
  expiresAt: number;
  claimed: boolean;
}

export interface RemoteDevice {
  clientId: string;
  displayName?: string;
  deviceType?: string;
  platform?: string;
  osVersion?: string;
  deviceModel?: string;
  appVersion?: string;
  lastSeenAt?: number;
}

export interface RemoteSnapshot {
  config: RemoteConfig;
  status: RemoteStatus;
  pairing?: RemotePairing;
}

export interface AgentMessage {
  id: string;
  fromAgentId: string;
  toAgentId: string;
  from: string;
  to: string;
  subject: string;
  body: string;
  response: "required" | "none";
  replyTo?: string;
  status: "open" | "answered" | "closed";
  resolution?: "reply" | "no_reply";
  deliveryStatus: "queued" | "delivering" | "delivered" | "failed" | "cancelled";
  createdAt: string;
  updatedAt: string;
  deliveredAt?: string;
  lastDeliveryError?: string;
  deliveredAgentId?: string;
  deliveredSessionId?: string;
  deliveredTurnId?: string;
}

export interface PlatformConnection {
  id: string;
  provider: string;
  accountRef?: string;
  credentialRef?: string;
  status: "disconnected" | "connecting" | "connected" | "degraded";
  capabilities: string[];
  cursor?: string;
  lastEventAt?: string;
  lastHeartbeatAt?: string;
  lastError?: string;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface AgentAddress {
  id: string;
  agentId: string;
  connectionId: string;
  externalIdentity: string;
  displayName?: string;
  triggerPolicy: "direct" | "mention" | "explicit_dispatch" | "all" | "allowlist";
  replyPolicy: "explicit" | "final_answer" | "none";
  trustDomain: string;
  allowActors?: string[];
  allowConversations?: string[];
  blockActors?: string[];
  blockConversations?: string[];
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface ConversationMembership {
  id: string;
  addressId: string;
  conversationId: string;
  displayName?: string;
  purpose?: string;
  role?: string;
  guidance?: string;
  triggerPolicy: AgentAddress["triggerPolicy"];
  replyPolicy: AgentAddress["replyPolicy"];
  trustDomain: string;
  enabled: boolean;
  version: number;
  createdAt: string;
  updatedAt: string;
}

export interface ActorRef {
  provider: string;
  connectionId?: string;
  externalId: string;
  displayName?: string;
  kind?: string;
  linkedAgentId?: string;
}

export interface ConversationRef {
  provider: string;
  connectionId: string;
  conversationId: string;
  threadId?: string;
  messageId?: string;
  conversationType?: string;
  audience?: string;
}

export interface MessageContent {
  text?: string;
  attachments?: Array<{ id?: string; name?: string; mimeType?: string; size?: number; url?: string; path?: string }>;
}

export interface InboxMessage {
  id: string;
  origin: string;
  externalKey: string;
  externalEventId?: string;
  externalMessageId?: string;
  sender: ActorRef;
  conversation: ConversationRef;
  content: MessageContent;
  replyTo?: string;
  responseExpectation: "required" | "optional" | "none";
  occurredAt?: string;
  receivedAt: string;
  providerMetadata?: Record<string, unknown>;
}

export interface InboxItem {
  id: string;
  agentId: string;
  messageId: string;
  addressId: string;
  membershipId?: string;
  state: "queued" | "handling" | "deferred" | "handled" | "failed" | "cancelled";
  outcome?: "reply" | "no_reply";
  priority?: number;
  availableAt?: string;
  attemptCount: number;
  activeAttemptId?: string;
  lastError?: string;
  note?: string;
  createdAt: string;
  updatedAt: string;
}

export interface HandlingAttempt {
  id: string;
  inboxItemId: string;
  agentId: string;
  sessionId?: string;
  turnId?: string;
  membershipId?: string;
  membershipVersion?: number;
  effectiveReplyPolicy?: AgentAddress["replyPolicy"];
  status: string;
  finalAnswer?: string;
  startedAt: string;
  completedAt?: string;
  error?: string;
}

export interface OutboxItem {
  id: string;
  agentId: string;
  addressId: string;
  inReplyTo?: string;
  conversation: ConversationRef;
  content: MessageContent;
  responseExpectation?: "required" | "optional" | "none";
  idempotencyKey: string;
  state: "pending" | "sending" | "sent" | "failed";
  externalMessageId?: string;
  attemptCount: number;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
  sentAt?: string;
}

export interface InboxEntry {
  item: InboxItem;
  message: InboxMessage;
  address: AgentAddress;
  membership?: ConversationMembership;
  attempt?: HandlingAttempt;
  agentName: string;
  outboxItem?: OutboxItem;
  internalMessage?: AgentMessage;
}

export interface AgentProfile {
  agentId: string;
  identity?: string;
  domain?: string;
  scope?: string;
  version: number;
  updatedAt?: string;
}

export interface Schedule {
  id: string;
  name: string;
  to: string;
  subject: string;
  body: string;
  response: "required" | "none";
  at?: string;
  cron?: string;
  timezone: string;
  enabled: boolean;
  lastRunAt?: string;
  nextRunAt?: string;
  lastMessageId?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
}

export interface TeamAgent {
  name: string;
  id: string;
  cwd?: string;
  status?: string;
  source?: string;
  profile: AgentProfile;
  messageIn: number;
  messageOut: number;
  openIn: number;
  openOut: number;
  scheduledIn: number;
  lastMessageAt?: string;
}

export interface TeamObservedLink {
  fromAgentId: string;
  toAgentId: string;
  from: string;
  to: string;
  messageCount: number;
  replyCount: number;
  openCount: number;
  answeredCount: number;
  closedCount: number;
  queuedCount: number;
  failedCount: number;
  lastMessageAt?: string;
  lastReplyAt?: string;
  subjects: string[];
}

export interface TeamRelationship {
  id: string;
  fromAgentId: string;
  toAgentId: string;
  from: string;
  to: string;
  description: string;
  createdAt: string;
  updatedAt: string;
}

export interface TeamView {
  agents: TeamAgent[];
  observedLinks: TeamObservedLink[];
  explicitLinks: TeamRelationship[];
}

export async function api(method: string, path: string, body?: unknown) {
  const resp = await fetch(path, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || resp.statusText);
  return data;
}

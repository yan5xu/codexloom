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
  goal?: ThreadGoal;
  lastSeq: number;
}

export interface ThreadArtifact {
  id: string;
  agentId: string;
  name: string;
  mimeType: string;
  size: number;
  sha256: string;
  path: string;
  url: string;
  createdAt: string;
  publishedAt?: string;
}

export type ThreadGoalStatus = "active" | "paused" | "blocked" | "usageLimited" | "budgetLimited" | "complete";

export interface ThreadGoal {
  threadId: string;
  objective: string;
  status: ThreadGoalStatus;
  tokenBudget: number | null;
  tokensUsed: number;
  timeUsedSeconds: number;
  createdAt: number;
  updatedAt: number;
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

export interface BackupPruneReport {
  beforeCount: number;
  afterCount: number;
  beforeBytes: number;
  afterBytes: number;
  removedCount: number;
  removedBytes: number;
}

export interface BackupSnapshot {
  name: string;
  path: string;
  createdAt: string;
  reason: string;
  sizeBytes: number;
  fileCount: number;
  rolloutCount: number;
  warnings?: string[];
  prune?: BackupPruneReport;
}

export interface BackupStatus {
  backups: BackupSnapshot[];
  dir: string;
  count: number;
  totalBytes: number;
  retention: {
    minCount: number;
    maxCount: number;
    maxBytes: number;
    maxAgeDays: number;
  };
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
  sourceTurnId?: string;
  status: "open" | "answered" | "closed";
  resolution?: "reply" | "no_reply" | "cancelled" | "completed_elsewhere" | "superseded";
  resolutionReason?: string;
  resolvedBy?: string;
  resolvedAt?: string;
  deliveryStatus: "queued" | "delivering" | "delivered" | "failed" | "cancelled";
  createdAt: string;
  updatedAt: string;
  deliveredAt?: string;
  lastDeliveryError?: string;
  deliveredAgentId?: string;
  deliveredSessionId?: string;
  deliveredTurnId?: string;
  deliveryMode?: "turn_start" | "turn_steer" | string;
  handlingStatus?: "pending" | "running" | "completed" | "interrupted" | "failed";
  activeHandlingAttemptId?: string;
  lastHandlingError?: string;
  handlingAttempts?: AgentMessageHandlingAttempt[];
}

export interface AgentMessageHandlingAttempt {
  id: string;
  turnId?: string;
  status: "running" | "completed" | "interrupted" | "failed";
  startedAt: string;
  completedAt?: string;
  error?: string;
}

export interface HumanRequestOption {
  label: string;
  description?: string;
}

export interface HumanRequest {
  id: string;
  agentId: string;
  agentName: string;
  threadId?: string;
  sourceTurnId?: string;
  sourceTask?: string;
  expectation: "required" | "optional";
  question: string;
  context?: string;
  blockedWork?: string;
  options?: HumanRequestOption[];
  state: "open" | "answered" | "cancelled";
  answer?: string;
  deliveryStatus: "waiting" | "queued" | "delivering" | "delivered" | "failed" | "cancelled";
  resumedTurnId?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
  answeredAt?: string;
  deliveredAt?: string;
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
  supersededBy?: string;
  archivedAt?: string;
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
  dmPolicy?: "open" | "managed" | "closed";
  trustDomain: string;
  allowActors?: string[];
  allowConversations?: string[];
  blockActors?: string[];
  blockConversations?: string[];
  enabled: boolean;
  supersededBy?: string;
  archivedAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface ConversationMembership {
  id: string;
  addressId: string;
  conversationId: string;
  conversationType?: "group" | "dm";
  actorId?: string;
  displayName?: string;
  purpose?: string;
  role?: string;
  guidance?: string;
  triggerPolicy: AgentAddress["triggerPolicy"];
  replyPolicy: AgentAddress["replyPolicy"];
  outboundPolicy?: "reply_only" | "proactive" | "none";
  trustDomain: string;
  enabled: boolean;
  supersededBy?: string;
  archivedAt?: string;
  version: number;
  createdAt: string;
  updatedAt: string;
}

export interface ConversationCandidate {
  id: string;
  addressId: string;
  conversationId: string;
  conversationType: "group" | "dm";
  displayName?: string;
  description?: string;
  available: boolean;
  firstSeenAt: string;
  lastSeenAt: string;
  updatedAt: string;
}

export interface LarkChatDiscovery {
  id: string;
  name: string;
  description?: string;
  avatar?: string;
  external: boolean;
}

export interface LarkDiscovery {
  available: boolean;
  runtime: "native" | string;
  appId?: string;
  credentialStored: boolean;
  botReady: boolean;
  botOpenId?: string;
  botName?: string;
  chats: LarkChatDiscovery[];
  error?: string;
}

export interface SlackChannelDiscovery {
  id: string;
  name: string;
  description?: string;
  private: boolean;
  member: boolean;
}

export interface SlackDiscovery {
  available: boolean;
  runtime: "managed-socket-mode" | string;
  appId?: string;
  teamId?: string;
  teamName?: string;
  credentialStored: boolean;
  botReady: boolean;
  socketReady: boolean;
  botUserId?: string;
  botName?: string;
  channels: SlackChannelDiscovery[];
  missingScopes?: string[];
  error?: string;
}

export interface ParallAgentDiscovery {
  id: string;
  name: string;
  status: string;
  online: boolean;
  presence?: string;
  lastSeenAt?: string;
  credentialStored: boolean;
}

export interface ParallChatDiscovery {
  id: string;
  name: string;
  description?: string;
  type: "direct" | "group" | string;
  visibility?: string;
  member: boolean;
}

export interface ParallDiscovery {
  available: boolean;
  runtime: "managed-websocket" | string;
  apiUrl?: string;
  orgId?: string;
  orgName?: string;
  ownerCredentialStored: boolean;
  ownerReady: boolean;
  ownerName?: string;
  ownerRole?: string;
  ownerError?: string;
  selectedAgentId?: string;
  agentCredentialStored: boolean;
  externalReady: boolean;
  socketReady: boolean;
  agents: ParallAgentDiscovery[];
  chats: ParallChatDiscovery[];
  error?: string;
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
  attachments?: Array<{ id?: string; name?: string; mimeType?: string; size?: number; sha256?: string; url?: string; path?: string }>;
}

export interface ThreadContextMessage {
  externalMessageId: string;
  role?: "root" | "reply";
  sender: ActorRef;
  content: MessageContent;
  occurredAt?: string;
  textTruncated?: boolean;
}

export interface ThreadContext {
  rootExternalMessageId: string;
  messages?: ThreadContextMessage[];
  truncated?: boolean;
  unavailableReason?: string;
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
  threadContext?: ThreadContext;
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
  state: "pending_access" | "queued" | "handling" | "interrupted" | "awaiting_delivery" | "deferred" | "handled" | "failed" | "cancelled";
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
  inboxItemId?: string;
  membershipId?: string;
  inReplyTo?: string;
  conversation: ConversationRef;
  content: MessageContent;
  responseExpectation?: "required" | "optional" | "none";
  idempotencyKey: string;
  state: "pending" | "sending" | "sent" | "failed";
  externalMessageId?: string;
  externalMessageIds?: string[];
  deliveryReceipts?: OutboxDeliveryReceipt[];
  attemptCount: number;
  attemptToken?: string;
  claimExpiresAt?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
  sentAt?: string;
}

export interface OutboxDeliveryReceipt {
  kind: "text" | "attachment" | string;
  artifactId?: string;
  externalMessageId?: string;
  externalAttachmentId?: string;
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

export interface TokenUsage {
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  reasoningOutputTokens: number;
  totalTokens: number;
  calls: number;
}

export interface UsageDay {
  date: string;
  usage: TokenUsage;
}

export interface UsageModel {
  model: string;
  usage: TokenUsage;
}

export interface AgentTokenUsage {
  agentId: string;
  agentName: string;
  threadId?: string;
  status: string;
  available: boolean;
  lifetime: TokenUsage;
  period: TokenUsage;
  previous: TokenUsage;
  today: TokenUsage;
  latestCall: TokenUsage;
  latestModel?: string;
  cacheHitPercent: number;
  context: {
    inputTokens: number;
    windowTokens: number;
    usedPercent: number;
  };
  daily: UsageDay[];
  models: UsageModel[];
  lastUpdatedAt?: string;
}

export interface TokenUsageOverview {
  days: number;
  since: string;
  through: string;
  timezone: string;
  generatedAt: string;
  live: boolean;
  trackedAgents: number;
  lifetime: TokenUsage;
  period: TokenUsage;
  previous: TokenUsage;
  today: TokenUsage;
  daily: UsageDay[];
  models: UsageModel[];
  agents: AgentTokenUsage[];
}

export interface WorkloadWaitStats {
  samples: number;
  p50Ms: number;
  p90Ms: number;
  maxMs: number;
}

export interface WorkloadBacklog {
  count: number;
  oldestMs: number;
}

export interface WorkloadDay {
  date: string;
  observedSeconds: number;
  executingSeconds: number;
  executingPercent: number;
  turnCount: number;
}

export interface WorkloadSource {
  source: string;
  wait: WorkloadWaitStats;
  backlog: WorkloadBacklog;
}

export interface WorkloadEvidence {
  id: string;
  agentId: string;
  agentName: string;
  source: string;
  provider?: string;
  state: string;
  queuedAt: string;
  startedAt?: string;
  waitMs: number;
  waitReason: string;
  evidenceHref: string;
}

export interface AgentWorkload {
  agentId: string;
  agentName: string;
  status: string;
  activityAvailable: boolean;
  observedSeconds: number;
  executingSeconds: number;
  executingPercent: number;
  idleProxyPercent: number;
  turnCount: number;
  openTurns: number;
  inferredTurns: number;
  wait: WorkloadWaitStats;
  backlog: WorkloadBacklog;
  daily: WorkloadDay[];
  sources: WorkloadSource[];
  evidence: WorkloadEvidence[];
}

export interface WorkloadOverview {
  days: number;
  since: string;
  through: string;
  timezone: string;
  generatedAt: string;
  live: boolean;
  observedSeconds: number;
  executingSeconds: number;
  executingPercent: number;
  idleProxyPercent: number;
  wait: WorkloadWaitStats;
  backlog: WorkloadBacklog;
  daily: WorkloadDay[];
  sources: WorkloadSource[];
  agents: AgentWorkload[];
  evidence: WorkloadEvidence[];
  dataQuality: {
    activityBasis: string;
    idleBasis: string;
    historicalWaitReasons: string;
    trackedActivityAgents: number;
    totalAgents: number;
    limitations: string[];
  };
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
  goal?: ThreadGoal;
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

export interface OrganizationRelationship {
  id: string;
  parentAgentId: string;
  childAgentId: string;
  parent: string;
  child: string;
  description: string;
  createdAt: string;
  updatedAt: string;
}

export interface TeamView {
  agents: TeamAgent[];
  organizationLinks: OrganizationRelationship[];
  collaborationLinks: TeamRelationship[];
  observedLinks: TeamObservedLink[];
  /** Compatibility alias for pre-separation clients. */
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

export async function uploadThreadArtifact(agentId: string, file: File, publish = false): Promise<ThreadArtifact> {
  const form = new FormData();
  form.append("file", file, file.name);
  const suffix = publish ? "?publish=true" : "";
  const resp = await fetch(`/api/agents/${encodeURIComponent(agentId)}/artifacts${suffix}`, {
    method: "POST",
    body: form,
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || resp.statusText);
  return data.artifact as ThreadArtifact;
}

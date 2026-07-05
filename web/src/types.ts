export interface HubEvent {
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

export interface Session {
  id: string;
  name: string;
  cwd: string;
  threadId: string;
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

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  IntegrationsPane,
} from "./IntegrationsPane";
import {
  canRepairParallGateway,
  credentialSourceKind,
  gatewayCommand,
  isLegacyCredentialRef,
  isNativeFeishuConnection,
} from "./external-credentials";
import { resetGlobalEventsForTests } from "./global-events";
import type { Agent, AgentAddress, LarkDiscovery, ParallDiscovery, PlatformConnection } from "./types";

const now = "2026-08-07T00:00:00Z";
const testAgent: Agent = {
  id: "agent-external",
  name: "loom-external-test",
  cwd: "/workspace/external",
  threadId: "thread-external",
  sandbox: "workspace-write",
  approvalPolicy: "on-request",
  status: "idle",
  currentTask: "",
  currentTurnId: "",
  lastError: "",
  createdAt: now,
  updatedAt: now,
  processAlive: true,
  pendingApprovals: [],
  lastSeq: 0,
};

function connection(overrides: Partial<PlatformConnection> = {}): PlatformConnection {
  return {
    id: "conn-parall",
    provider: "parall",
    accountRef: "org-test",
    credentialRef: "managed:opaque-test-id",
    status: "disconnected",
    capabilities: ["receive_events"],
    enabled: true,
    createdAt: now,
    updatedAt: now,
    ...overrides,
  };
}

function address(overrides: Partial<AgentAddress> = {}): AgentAddress {
  return {
    id: "addr-parall",
    agentId: testAgent.id,
    connectionId: "conn-parall",
    externalIdentity: "prll://external-test",
    displayName: "External test",
    triggerPolicy: "explicit_dispatch",
    replyPolicy: "final_answer",
    trustDomain: "external",
    enabled: true,
    createdAt: now,
    updatedAt: now,
    ...overrides,
  };
}

function discovery(overrides: Partial<ParallDiscovery> = {}): ParallDiscovery {
  return {
    available: true,
    runtime: "managed-websocket",
    apiUrl: "https://api.parall.example",
    orgId: "org-test",
    ownerCredentialStored: true,
    ownerReady: true,
    selectedAgentId: "external-test",
    agentCredentialStored: true,
    externalReady: true,
    socketReady: true,
    agents: [{ id: "external-test", name: "External test", status: "active", online: false, credentialStored: true }],
    chats: [],
    ...overrides,
  };
}

function mockExternalAPI(items: PlatformConnection[], addresses: AgentAddress[], parallDiscovery: ParallDiscovery | ((connectionID: string) => ParallDiscovery) = discovery()) {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    const url = new URL(raw, "http://localhost");
    const method = init?.method || "GET";
    let body: unknown = {};
    if (url.pathname === "/api/integrations/connections") body = { connections: items };
    else if (url.pathname === "/api/integrations/addresses") body = { addresses };
    else if (url.pathname === "/api/integrations/conversations") body = { memberships: [] };
    else if (url.pathname === "/api/integrations/conversation-candidates") body = { candidates: [] };
    else if (url.pathname === "/api/inbox") body = { entries: [] };
    else if (url.pathname === "/api/integrations/providers/parall/discovery") body = { discovery: typeof parallDiscovery === "function" ? parallDiscovery(url.searchParams.get("connectionId") || "") : parallDiscovery };
    else if (method === "POST" && url.pathname === "/api/integrations/providers/parall/gateway") body = { gateway: { state: "started" }, discovery: typeof parallDiscovery === "function" ? parallDiscovery("conn-parall") : parallDiscovery };
    return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
  });
}

function renderPane(item: PlatformConnection, itemAddress = address(), parallDiscovery = discovery()) {
  const fetchMock = mockExternalAPI([item], [itemAddress], parallDiscovery);
  vi.stubGlobal("fetch", fetchMock);
  const onError = vi.fn();
  render(<IntegrationsPane agents={[testAgent]} onError={onError} />);
  return { fetchMock, onError };
}

afterEach(() => {
  cleanup();
  resetGlobalEventsForTests();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "#external");
  delete window.codexLoom;
  delete window.codexHub;
});

describe("External managed credential contract", () => {
  it.each([
    ["managed:opaque-id", "managed"],
    ["keychain:compatibility.service", "keychain"],
    ["env:COMPATIBILITY_TOKEN", "environment"],
    ["", "missing"],
    ["managed:", "unknown"],
    ["file:/tmp/secret", "unknown"],
  ] as const)("classifies %s without interpreting provider identity", (value, expected) => {
    expect(credentialSourceKind(value)).toBe(expected);
  });

  it("only accepts env and keychain references in the legacy manual path", () => {
    expect(isLegacyCredentialRef("env:EXISTING_TOKEN")).toBe(true);
    expect(isLegacyCredentialRef("keychain:existing.service")).toBe(true);
    expect(isLegacyCredentialRef("managed:opaque-id")).toBe(false);
  });

  it("allows managed recovery only when backend discovery is ready", () => {
    const item = connection({ credentialRef: "managed:opaque-id" });
    expect(canRepairParallGateway(item, address(), discovery())).toBe(true);
    expect(canRepairParallGateway(item, address(), discovery({ agentCredentialStored: false }))).toBe(false);
    expect(canRepairParallGateway(item, address(), discovery({ externalReady: false }))).toBe(false);
    expect(canRepairParallGateway(item, address(), discovery({ socketReady: false }))).toBe(false);
  });

  it.each(["keychain:existing.service", "env:EXISTING_TOKEN"])("keeps legacy %s outside manual repair even when discovery is ready", (credentialRef) => {
    expect(canRepairParallGateway(connection({ credentialRef }), address(), discovery())).toBe(false);
  });

  it("fails recovery closed for connected, disabled, archived, malformed, or incomplete inputs", () => {
    expect(canRepairParallGateway(connection({ status: "connected" }), address(), discovery())).toBe(false);
    expect(canRepairParallGateway(connection({ status: "connecting" }), address(), discovery())).toBe(false);
    expect(canRepairParallGateway(connection({ enabled: false }), address(), discovery())).toBe(false);
    expect(canRepairParallGateway(connection({ archivedAt: now }), address(), discovery())).toBe(false);
    expect(canRepairParallGateway(connection({ credentialRef: "managed:" }), address(), discovery())).toBe(false);
    expect(canRepairParallGateway(connection(), address({ enabled: false }), discovery())).toBe(false);
    expect(canRepairParallGateway(connection(), address({ archivedAt: now }), discovery())).toBe(false);
    expect(canRepairParallGateway(connection(), address({ deletedAt: now }), discovery())).toBe(false);
    expect(canRepairParallGateway(connection(), address({ connectionId: "another-connection" }), discovery())).toBe(false);
    expect(canRepairParallGateway(connection(), address(), discovery({ available: false }))).toBe(false);
    expect(canRepairParallGateway(connection(), address(), discovery({ orgId: "another-org" }))).toBe(false);
    expect(canRepairParallGateway(connection(), address(), discovery({ selectedAgentId: "another-agent" }))).toBe(false);
  });

  it("uses structured discovery identity for Slack commands instead of parsing an opaque managed ref", () => {
    const item = connection({ provider: "slack", accountRef: "T_TEST", credentialRef: "managed:must-not-appear" });
    const itemAddress = address({ externalIdentity: "slack://U_TEST" });
    const command = gatewayCommand(item, itemAddress, { slackAppID: "A_DISCOVERED" });
    expect(command).toContain("--app-id 'A_DISCOVERED'");
    expect(command).toContain("--team-id 'T_TEST'");
    expect(command).not.toContain("must-not-appear");
  });

  it("fails Slack command generation closed when public discovery has no App ID", () => {
    const item = connection({ provider: "slack", accountRef: "T_TEST", credentialRef: "managed:must-not-be-parsed" });
    expect(gatewayCommand(item, address({ externalIdentity: "slack://U_TEST" }))).toBe("");
  });

  it("recognizes native Feishu through discovery rather than the credential reference", () => {
    const item = connection({ provider: "lark", accountRef: "cli_test", credentialRef: "managed:opaque-lark" });
    const larkDiscovery: LarkDiscovery = { available: true, runtime: "native", appId: "cli_test", credentialStored: true, botReady: true, chats: [] };
    expect(isNativeFeishuConnection(item, larkDiscovery)).toBe(true);
    expect(isNativeFeishuConnection(item, { ...larkDiscovery, appId: "cli_other" })).toBe(false);
  });

  it("shows managed recovery and sends only the connection ID to backend preflight", async () => {
    const { fetchMock, onError } = renderPane(connection());
    expect((await screen.findAllByText("CodexLoom managed credential"))[0]).toBeVisible();
    const restart = await screen.findByRole("button", { name: "Restart gateway" });
    fireEvent.click(restart);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "/api/integrations/providers/parall/gateway",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ connectionId: "conn-parall" }) }),
    ));
    expect(screen.getByText(/wait for a later heartbeat before considering the Connection recovered/)).toBeVisible();
    expect(screen.queryByText(/Connection (is|has) recovered/i)).not.toBeInTheDocument();
    expect(onError).not.toHaveBeenCalled();
  });

  it("refreshes Parall discovery when selecting another Connection in the same organization", async () => {
    const first = connection({ id: "conn-first" });
    const second = connection({ id: "conn-second" });
    const firstAddress = address({ id: "addr-first", connectionId: first.id, externalIdentity: "prll://first-agent", displayName: "First identity" });
    const secondAddress = address({ id: "addr-second", connectionId: second.id, externalIdentity: "prll://second-agent", displayName: "Second identity" });
    const fetchMock = mockExternalAPI([first, second], [firstAddress, secondAddress], (connectionID) => {
      const selectedAgentId = connectionID === second.id ? "second-agent" : "first-agent";
      return discovery({
        selectedAgentId,
        agents: [{ id: selectedAgentId, name: selectedAgentId, status: "active", online: false, credentialStored: true }],
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<IntegrationsPane agents={[testAgent]} onError={vi.fn()} />);

    fireEvent.click(await screen.findByRole("button", { name: /Second identity/ }));
    expect(await screen.findByRole("heading", { name: "Second identity" })).toBeVisible();
    expect(await screen.findByRole("button", { name: "Restart gateway" })).toBeEnabled();
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/api/integrations/providers/parall/discovery?connectionId=conn-second"))).toBe(true);
  });

  it.each([
    ["keychain:existing.service", "Legacy Keychain credential"],
    ["env:EXISTING_TOKEN", "Legacy environment credential"],
  ])("preserves %s as a compatibility source without offering restart", async (credentialRef, label) => {
    const { fetchMock } = renderPane(connection({ credentialRef, status: "degraded" }));
    expect((await screen.findAllByText(label))[0]).toBeVisible();
    expect(screen.getByText(/not eligible for automatic or manual restart/)).toBeVisible();
    expect(screen.getByText("Migration required")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Restart gateway" })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input, init]) => String(input).includes("/api/integrations/providers/parall/gateway") && init?.method === "POST")).toBe(false);
  });

  it("does not offer repair for a connected managed connection", async () => {
    renderPane(connection({ status: "connected" }), address(), discovery({ agents: [{ id: "external-test", name: "External test", status: "active", online: true, credentialStored: true }] }));
    expect((await screen.findAllByText("CodexLoom managed credential"))[0]).toBeVisible();
    expect(screen.queryByText("managed:opaque-test-id")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Restart gateway" })).not.toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Add conversation" })).toBeVisible();
  });

  it("fails an unknown source closed instead of asking for a secret", async () => {
    renderPane(connection({ credentialRef: "file:/tmp/not-supported" }));
    expect((await screen.findAllByText("Unknown credential source"))[0]).toBeVisible();
    expect(screen.getByRole("button", { name: "Repair unavailable" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Connect identity" })).not.toBeInTheDocument();
  });

  it("keeps managed IDs out of the legacy manual input", async () => {
    const fetchMock = mockExternalAPI([], []);
    vi.stubGlobal("fetch", fetchMock);
    render(<IntegrationsPane agents={[testAgent]} onError={vi.fn()} />);
    fireEvent.click(await screen.findByTitle("Add integration"));
    fireEvent.click(screen.getByRole("button", { name: "Legacy / advanced compatibility" }));
    expect(screen.getByText(/Managed references are issued and validated by the Hub/)).toBeVisible();
    const input = screen.getByLabelText("Legacy credential reference");
    fireEvent.change(input, { target: { value: "managed:hand-written" } });
    expect(screen.getByRole("button", { name: "Create compatibility connection" })).toBeDisabled();
    fireEvent.change(input, { target: { value: "env:EXISTING_TOKEN" } });
    expect(screen.getByRole("button", { name: "Create compatibility connection" })).toBeEnabled();
  });

  it("describes new onboarding as managed and excludes ordinary backups", async () => {
    vi.stubGlobal("fetch", mockExternalAPI([], []));
    render(<IntegrationsPane agents={[testAgent]} onError={vi.fn()} />);
    fireEvent.click(await screen.findByTitle("Add integration"));
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(screen.getByText(/stored as a CodexLoom managed credential/)).toBeVisible();
    expect(screen.getByText(/managed credential material is not exposed here or included in ordinary backups/)).toBeVisible();
    expect(screen.queryByText(/operating system Keychain/)).not.toBeInTheDocument();
  });
});

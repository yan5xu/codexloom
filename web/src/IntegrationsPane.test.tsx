import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  IntegrationsPane,
} from "./IntegrationsPane";
import {
  canOfferParallRepairCLI,
  credentialSourceKind,
  gatewayCommand,
  isLegacyCredentialRef,
} from "./external-credentials";
import { resetGlobalEventsForTests } from "./global-events";
import type { Agent, AgentAddress, ParallDiscovery, PlatformConnection } from "./types";

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

function isProtectedIntegrationRequest(input: RequestInfo | URL) {
  const raw = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
  const path = new URL(raw, "http://localhost").pathname;
  return path.startsWith("/api/integrations/providers/")
    || path.includes("/credential-migration")
    || path.startsWith("/api/integrations/credential-migrations/");
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
  it("keeps managed repair browser-safe and routes the operator to explicit-token CLI", async () => {
    const { fetchMock, onError } = renderPane(connection());

    expect((await screen.findAllByText("CodexLoom managed credential"))[0]).toBeVisible();
    const repair = await screen.findByRole("button", { name: "Restart via CLI" });
    expect(screen.queryByRole("button", { name: "Restart gateway" })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => isProtectedIntegrationRequest(input))).toBe(false);
    fireEvent.click(repair);
    expect(await screen.findByRole("heading", { name: "Terminal repair required" })).toBeVisible();
    expect(screen.getByText("CODEX_LOOM_ADMIN_TOKEN")).toBeVisible();
    expect(screen.getByText(/only a later heartbeat proves recovery/i)).toBeVisible();
    expect(fetchMock.mock.calls.some(([input]) => isProtectedIntegrationRequest(input))).toBe(false);
    expect(onError).not.toHaveBeenCalled();
  });

  it("opens new onboarding as CLI-only guidance without secret fields or protected requests", async () => {
    const fetchMock = mockExternalAPI([], []);
    vi.stubGlobal("fetch", fetchMock);
    render(<IntegrationsPane agents={[testAgent]} onError={vi.fn()} />);

    fireEvent.click(await screen.findByTitle("Add integration"));
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("heading", { name: /terminal setup required/i })).toBeVisible();
    expect(screen.getByText(/CODEX_LOOM_ADMIN_TOKEN/)).toBeVisible();
    expect(screen.queryByLabelText(/secret|token/i)).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => isProtectedIntegrationRequest(input))).toBe(false);
  });

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

  it("offers managed repair CLI guidance only from eligible persisted state", () => {
    const item = connection({ credentialRef: "managed:opaque-id" });
    expect(canOfferParallRepairCLI(item, address())).toBe(true);
    expect(canOfferParallRepairCLI({ ...item, accountRef: "" }, address())).toBe(false);
    expect(canOfferParallRepairCLI(item, address({ externalIdentity: "" }))).toBe(false);
    expect(canOfferParallRepairCLI(item, address({ agentId: "" }))).toBe(false);
  });

  it.each(["keychain:existing.service", "env:EXISTING_TOKEN"])("keeps legacy %s outside manual repair", (credentialRef) => {
    expect(canOfferParallRepairCLI(connection({ credentialRef }), address())).toBe(false);
  });

  it("fails recovery closed for connected, disabled, archived, malformed, or incomplete inputs", () => {
    expect(canOfferParallRepairCLI(connection({ status: "connected" }), address())).toBe(false);
    expect(canOfferParallRepairCLI(connection({ status: "connecting" }), address())).toBe(false);
    expect(canOfferParallRepairCLI(connection({ enabled: false }), address())).toBe(false);
    expect(canOfferParallRepairCLI(connection({ archivedAt: now }), address())).toBe(false);
    expect(canOfferParallRepairCLI(connection({ credentialRef: "managed:" }), address())).toBe(false);
    expect(canOfferParallRepairCLI(connection(), address({ enabled: false }))).toBe(false);
    expect(canOfferParallRepairCLI(connection(), address({ archivedAt: now }))).toBe(false);
    expect(canOfferParallRepairCLI(connection(), address({ deletedAt: now }))).toBe(false);
    expect(canOfferParallRepairCLI(connection(), address({ connectionId: "another-connection" }))).toBe(false);
  });

  it("never emits a manual gateway command for a managed credential", () => {
    expect(gatewayCommand(
      connection({ provider: "lark", accountRef: "cli_test", credentialRef: "managed:opaque-lark" }),
      address({ externalIdentity: "lark://ou_test" }),
    )).toBe("");
    expect(gatewayCommand(
      connection({ provider: "slack", accountRef: "T_TEST", credentialRef: "managed:opaque-slack" }),
      address({ externalIdentity: "slack://U_TEST" }),
      { slackAppID: "A_DISCOVERED" },
    )).toBe("");
    expect(gatewayCommand(connection(), address())).toBe("");
  });

  it("preserves an exact runnable legacy reference in the generated command", () => {
    const lark = gatewayCommand(
      connection({ provider: "lark", accountRef: "cli_test", credentialRef: "env:FEISHU_APP_SECRET" }),
      address({ externalIdentity: "lark://ou_test" }),
    );
    expect(lark).toContain("bin/loom-feishu-gateway");
    expect(lark).toContain("--credential-ref 'env:FEISHU_APP_SECRET'");

    const slack = gatewayCommand(
      connection({ provider: "slack", accountRef: "T_TEST", credentialRef: "keychain:com.codexloom.slack.A_DISCOVERED" }),
      address({ externalIdentity: "slack://U_TEST" }),
      { slackAppID: "A_DISCOVERED" },
    );
    expect(slack).toContain("--app-id 'A_DISCOVERED'");
    expect(slack).toContain("--team-id 'T_TEST'");
    expect(slack).toContain("--credential-ref 'keychain:com.codexloom.slack.A_DISCOVERED'");

    const parallEnv = gatewayCommand(
      connection({ credentialRef: "env:PRLL_API_KEY" }),
      address(),
    );
    expect(parallEnv).toContain("bin/loom-parall-gateway");
    expect(parallEnv).toContain("--credential-ref 'env:PRLL_API_KEY'");

    const parallKeychain = gatewayCommand(
      connection({ credentialRef: "keychain:com.codexloom.parall.agent.org-test.external-test" }),
      address(),
    );
    expect(parallKeychain).toContain("--credential-ref 'keychain:com.codexloom.parall.agent.org-test.external-test'");
  });

  it("fails legacy gateway commands closed when their scheme or identity is not runnable", () => {
    expect(gatewayCommand(
      connection({ provider: "lark", accountRef: "cli_test", credentialRef: "keychain:wrong.service" }),
      address({ externalIdentity: "lark://ou_test" }),
    )).toBe("");
    expect(gatewayCommand(
      connection({ provider: "slack", accountRef: "T_TEST", credentialRef: "env:SLACK_BOT_TOKEN" }),
      address({ externalIdentity: "slack://U_TEST" }),
      { slackAppID: "A_DISCOVERED" },
    )).toBe("");
    expect(gatewayCommand(
      connection({ provider: "slack", accountRef: "T_TEST", credentialRef: "keychain:com.codexloom.slack.OTHER_APP" }),
      address({ externalIdentity: "slack://U_TEST" }),
      { slackAppID: "A_DISCOVERED" },
    )).toBe("");
    expect(gatewayCommand(
      connection({ credentialRef: "keychain:wrong.service" }),
      address(),
    )).toBe("");
    expect(gatewayCommand(
      connection({ provider: "custom", credentialRef: "env:CUSTOM_TOKEN" }),
      address({ externalIdentity: "custom://identity" }),
    )).toBe("");
    expect(gatewayCommand(
      connection({ credentialRef: "env:PRLL_API_KEY", enabled: false }),
      address(),
    )).toBe("");
    expect(gatewayCommand(
      connection({ credentialRef: "env:PRLL_API_KEY", archivedAt: now }),
      address(),
    )).toBe("");
    expect(gatewayCommand(
      connection({ credentialRef: "env:PRLL_API_KEY" }),
      address({ enabled: false }),
    )).toBe("");
    expect(gatewayCommand(
      connection({ credentialRef: "env:PRLL_API_KEY" }),
      address({ deletedAt: now }),
    )).toBe("");
  });

  it("opens managed repair instructions without sending a protected request", async () => {
    const { fetchMock, onError } = renderPane(connection());
    expect((await screen.findAllByText("CodexLoom managed credential"))[0]).toBeVisible();
    const restart = await screen.findByRole("button", { name: "Restart via CLI" });
    fireEvent.click(restart);
    expect(await screen.findByRole("heading", { name: "Terminal repair required" })).toBeVisible();
    expect(fetchMock.mock.calls.some(([input]) => isProtectedIntegrationRequest(input))).toBe(false);
    expect(screen.getByText(/only a later heartbeat proves recovery/)).toBeVisible();
    expect(screen.queryByText(/Connection (is|has) recovered/i)).not.toBeInTheDocument();
    expect(onError).not.toHaveBeenCalled();
  });

  it("shows managed status without exposing a manual gateway command", async () => {
    renderPane(connection());
    fireEvent.click(await screen.findByText("Advanced settings"));
    expect(await screen.findByText(/CodexLoom manages this gateway/)).toBeVisible();
    expect(screen.queryByText(/bin\/loom-parall-gateway/)).not.toBeInTheDocument();
    expect(screen.queryByTitle("Copy gateway command")).not.toBeInTheDocument();
    expect(screen.queryByText(/managed:opaque-test-id/)).not.toBeInTheDocument();
  });

  it("switches same-org Connections from persisted state without protected discovery", async () => {
    const first = connection({ id: "conn-first" });
    const second = connection({ id: "conn-second" });
    const firstAddress = address({ id: "addr-first", connectionId: first.id, externalIdentity: "prll://first-agent", displayName: "First identity" });
    const secondAddress = address({ id: "addr-second", connectionId: second.id, externalIdentity: "prll://second-agent", displayName: "Second identity" });
    const fetchMock = mockExternalAPI([first, second], [firstAddress, secondAddress]);
    vi.stubGlobal("fetch", fetchMock);
    render(<IntegrationsPane agents={[testAgent]} onError={vi.fn()} />);

    fireEvent.click(await screen.findByRole("button", { name: /Second identity/ }));
    expect(await screen.findByRole("heading", { name: "Second identity" })).toBeVisible();
    expect(await screen.findByRole("button", { name: "Restart via CLI" })).toBeEnabled();
    expect(fetchMock.mock.calls.some(([input]) => isProtectedIntegrationRequest(input))).toBe(false);
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

  it("shows a runnable Parall env command with its exact legacy reference", async () => {
    renderPane(connection({ credentialRef: "env:PRLL_API_KEY", status: "degraded" }));
    fireEvent.click(await screen.findByText("Advanced settings"));
    expect(await screen.findByText("Legacy compatibility command")).toBeVisible();
    expect(screen.getByText(/--credential-ref 'env:PRLL_API_KEY'/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "Restart gateway" })).not.toBeInTheDocument();
    expect(screen.getByTitle("Copy gateway command")).toBeVisible();
  });

  it("fails a mismatched legacy Keychain command closed and points to migration", async () => {
    renderPane(connection({ credentialRef: "keychain:wrong.service", status: "degraded" }));
    fireEvent.click(await screen.findByText("Advanced settings"));
    expect(await screen.findByText(/does not match a runnable compatibility command/)).toBeVisible();
    expect(screen.getByRole("button", { name: "Migration required" })).toBeVisible();
    expect(screen.queryByText(/bin\/loom-parall-gateway/)).not.toBeInTheDocument();
    expect(screen.queryByTitle("Copy gateway command")).not.toBeInTheDocument();
  });

  it("does not offer repair for a connected managed connection", async () => {
    renderPane(connection({ status: "connected" }), address(), discovery({ agents: [{ id: "external-test", name: "External test", status: "active", online: true, credentialStored: true }] }));
    expect((await screen.findAllByText("CodexLoom managed credential"))[0]).toBeVisible();
    expect(screen.queryByText("managed:opaque-test-id")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Restart gateway" })).not.toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Manage via CLI" })).toBeVisible();
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

  it("describes CLI onboarding and limits the ordinary-backup claim to managed material", async () => {
    vi.stubGlobal("fetch", mockExternalAPI([], []));
    render(<IntegrationsPane agents={[testAgent]} onError={vi.fn()} />);
    fireEvent.click(await screen.findByTitle("Add integration"));
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(screen.getByText(/credential onboarding and provider discovery require the operator CLI/)).toBeVisible();
    expect(screen.getByText(/Managed credential material and private rollback anchors are excluded from ordinary backups/)).toBeVisible();
    expect(screen.getByText(/does not claim that every other configuration file is secret-free/)).toBeVisible();
    expect(screen.queryByText(/operating system Keychain/)).not.toBeInTheDocument();
  });
});

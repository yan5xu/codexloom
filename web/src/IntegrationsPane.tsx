import { Bot, Cable, Check, ChevronDown, Copy, Link2, LoaderCircle, MessageSquare, Pencil, Plus, RefreshCw, Send, ShieldCheck, Terminal, Unplug, Users, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { canOfferParallRepairCLI, credentialSourceKind, credentialSourcePresentation, externalIdentityID, gatewayCommand, isLegacyCredentialRef } from "./external-credentials";
import { api, type AgentAddress, type PlatformConnection, type Agent, type ConversationCandidate, type ConversationMembership, type InboxEntry } from "./types";
import { subscribeGlobalEvents } from "./global-events";

type OperatorFlow = {
  provider: string;
  action: "onboarding" | "manage" | "repair" | "migration";
  connectionID?: string;
};

function operatorActionForConnection(connection: PlatformConnection): OperatorFlow["action"] {
  const source = credentialSourceKind(connection.credentialRef);
  if (source === "keychain" || source === "environment") return "migration";
  if (source === "managed") return connection.provider === "parall" && ["disconnected", "degraded"].includes(connection.status) ? "repair" : "manage";
  return "onboarding";
}

export function IntegrationsPane({ agents, onError }: { agents: Agent[]; onError: (message: string) => void }) {
  const [connections, setConnections] = useState<PlatformConnection[]>([]);
  const [addresses, setAddresses] = useState<AgentAddress[]>([]);
  const [memberships, setMemberships] = useState<ConversationMembership[]>([]);
  const [conversationCandidates, setConversationCandidates] = useState<ConversationCandidate[]>([]);
  const [inboxEntries, setInboxEntries] = useState<InboxEntry[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [advancedCreateOpen, setAdvancedCreateOpen] = useState(false);
  const [bindOpen, setBindOpen] = useState(false);
  const [provider, setProvider] = useState("lark");
  const [accountRef, setAccountRef] = useState("");
  const [credentialRef, setCredentialRef] = useState("");
  const [capabilities, setCapabilities] = useState(() => providerSpec("lark").capabilities.join(","));
  const [agent, setAgent] = useState("");
  const [identity, setIdentity] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [triggerPolicy, setTriggerPolicy] = useState<AgentAddress["triggerPolicy"]>("mention");
  const [replyPolicy, setReplyPolicy] = useState<AgentAddress["replyPolicy"]>("final_answer");
  const [trustDomain, setTrustDomain] = useState("local");
  const [allowActors, setAllowActors] = useState("");
  const [allowConversations, setAllowConversations] = useState("");
  const [blockActors, setBlockActors] = useState("");
  const [blockConversations, setBlockConversations] = useState("");
  const [editingAddressID, setEditingAddressID] = useState("");
  const [working, setWorking] = useState(false);
  const [operatorFlow, setOperatorFlow] = useState<OperatorFlow | null>(null);
  const stateRef = useRef<Record<string, unknown>>({});
  const selectedAddressIDsRef = useRef<string[]>([]);
  const inboxRefreshTimerRef = useRef<number | null>(null);
  const coreRefreshTimerRef = useRef<number | null>(null);
  const selected = connections.find((connection) => connection.id === selectedID) || null;
  const selectedAddresses = addresses.filter((address) => address.connectionId === selectedID && (selected?.archivedAt ? true : !address.archivedAt && !address.deletedAt));
  const selectedAddressIDs = selectedAddresses.map((address) => address.id).sort();
  const selectedAddressKey = selectedAddressIDs.join("\u0000");
  selectedAddressIDsRef.current = selectedAddressIDs;

  const refresh = useCallback(async () => {
    const [connectionData, addressData, membershipData, candidateData] = await Promise.all([
      api("GET", "/api/integrations/connections"),
      api("GET", "/api/integrations/addresses"),
      api("GET", "/api/integrations/conversations"),
      api("GET", "/api/integrations/conversation-candidates"),
    ]);
    const nextConnections = connectionData.connections || [];
    setConnections(nextConnections);
    setAddresses(addressData.addresses || []);
    setMemberships(membershipData.memberships || []);
    setConversationCandidates(candidateData.candidates || []);
    setSelectedID((current) => nextConnections.some((connection: PlatformConnection) => connection.id === current)
      ? current
      : nextConnections.find((connection: PlatformConnection) => !connection.archivedAt)?.id || nextConnections[0]?.id || "");
  }, []);

  const refreshSelectedInbox = useCallback(async (addressIDs: string[]) => {
    if (addressIDs.length === 0) {
      setInboxEntries([]);
      return;
    }
    const params = new URLSearchParams({ state: "pending_access" });
    for (const addressID of addressIDs) params.append("address", addressID);
    const data = await api("GET", `/api/inbox?${params}`);
    setInboxEntries(data.entries || []);
  }, []);

  const refreshAll = useCallback(async () => {
    await refresh();
    await refreshSelectedInbox(selectedAddressIDsRef.current);
  }, [refresh, refreshSelectedInbox]);

  const openOperatorFlow = (providerID: string, action: OperatorFlow["action"] = "onboarding", connectionID = "") => {
    setCreateOpen(false);
    setOperatorFlow({ provider: providerID, action, connectionID: connectionID || undefined });
    window.history.replaceState(null, "", `#external?setup=${encodeURIComponent(providerID)}`);
  };

  const closeOperatorFlow = () => {
    setOperatorFlow(null);
    window.history.replaceState(null, "", "#external");
  };

  const openLarkSetup = (_appID = "", mode: "connect" | "add-group" = "connect") => {
    openOperatorFlow("lark", mode === "add-group" ? "manage" : "onboarding", selected?.id || "");
  };

  const openSlackSetup = (connectionID = "", mode: "connect" | "add-channel" = "connect") => {
    openOperatorFlow("slack", mode === "add-channel" ? "manage" : "onboarding", connectionID);
  };

  const openParallSetup = (connectionID = "", mode: "connect" | "add-conversation" | "repair" = "connect") => {
    openOperatorFlow("parall", mode === "add-conversation" ? "manage" : mode === "repair" ? "repair" : "onboarding", connectionID);
  };

  useEffect(() => {
    refresh().catch((error: Error) => onError(error.message));
    const params = new URLSearchParams(window.location.hash.split("?")[1] || "");
    const setupProvider = params.get("setup") || "";
    if (["lark", "slack", "parall"].includes(setupProvider)) {
      setOperatorFlow({ provider: setupProvider, action: "onboarding" });
    }
    const unsubscribe = subscribeGlobalEvents((value) => {
      try {
        const data = value.data || {};
        if (value.type === "loom/integration-connection" && data.connection?.id) {
          setConnections((current) => upsertByID(current, data.connection));
        } else if (value.type === "loom/integration-address" && data.address?.id) {
          setAddresses((current) => upsertByID(current, data.address));
        } else if (value.type === "loom/conversation-membership" && data.membership?.id) {
          setMemberships((current) => upsertByID(current, data.membership));
        } else if (value.type === "loom/conversation-candidates" && data.addressId) {
          setConversationCandidates((current) => [
            ...current.filter((candidate) => candidate.addressId !== data.addressId),
            ...(data.candidates || []),
          ]);
        } else if (["loom/integration-consolidated", "loom/integration-rollback", "loom/integration-restored", "loom/reconcile"].includes(value.type)) {
          if (coreRefreshTimerRef.current !== null) window.clearTimeout(coreRefreshTimerRef.current);
          coreRefreshTimerRef.current = window.setTimeout(() => refresh().catch(() => {}), 150);
        } else if (["loom/inbox-message", "loom/inbox-item"].includes(value.type)) {
          const addressID = data.inboxItem?.addressId || data.item?.addressId || "";
          if (addressID && !selectedAddressIDsRef.current.includes(addressID)) return;
          if (inboxRefreshTimerRef.current !== null) window.clearTimeout(inboxRefreshTimerRef.current);
          inboxRefreshTimerRef.current = window.setTimeout(() => refreshSelectedInbox(selectedAddressIDsRef.current).catch(() => {}), 150);
        }
      } catch {
        // Ignore malformed global events.
      }
    });
    return () => {
      unsubscribe();
      if (coreRefreshTimerRef.current !== null) window.clearTimeout(coreRefreshTimerRef.current);
      if (inboxRefreshTimerRef.current !== null) window.clearTimeout(inboxRefreshTimerRef.current);
    };
  }, []);

  useEffect(() => {
    refreshSelectedInbox(selectedAddressIDs).catch((error: Error) => onError(error.message));
  }, [selectedAddressKey]);

  const activeConnections = connections.filter((connection) => !connection.archivedAt).sort((left, right) => {
    const label = (connection: PlatformConnection) => {
      const address = addresses.find((item) => item.connectionId === connection.id && !item.archivedAt && !item.deletedAt);
      const owner = agents.find((item) => item.id === address?.agentId)?.name || "~unassigned";
      return `${owner}\u0000${address?.displayName || address?.externalIdentity || connection.provider}`;
    };
    return label(left).localeCompare(label(right));
  });
  const archivedConnections = connections.filter((connection) => Boolean(connection.archivedAt));
  const selectedMemberships = memberships.filter((membership) => selectedAddresses.some((address) => address.id === membership.addressId) && (selected?.archivedAt ? true : !membership.archivedAt));
  const selectedCandidates = conversationCandidates.filter((candidate) => selectedAddresses.some((address) => address.id === candidate.addressId));
  const selectedInboxEntries = inboxEntries.filter((entry) => selectedAddresses.some((address) => address.id === entry.item.addressId));
  const selectedProvider = providerSpec(selected?.provider || provider);
  const createProvider = providerSpec(provider);
  const connectedCount = activeConnections.filter((connection) => connection.enabled && connection.status === "connected").length;
  const membershipKeys = new Set(memberships.filter((membership) => !membership.archivedAt).map((membership) => `${membership.addressId}\u0000${membership.conversationId}`));
  const unconfiguredCandidateCount = conversationCandidates.filter((candidate) => candidate.available && !membershipKeys.has(`${candidate.addressId}\u0000${candidate.conversationId}`)).length;
  const agentName = (id: string) => agents.find((agent) => agent.id === id)?.name || id;

  const run = async (task: () => Promise<void>) => {
    if (working) return;
    setWorking(true);
    try {
      await task();
      await refresh();
    } catch (error: any) {
      onError(error.message);
    } finally {
      setWorking(false);
    }
  };

  const createConnection = () => {
    if (!provider.trim()) {
      onError("provider is required");
      return;
    }
    if (!isLegacyCredentialRef(credentialRef)) {
      onError("Legacy credential reference must use env: or keychain:. Managed references are issued by the Hub and cannot be entered here.");
      return;
    }
    run(async () => {
      const data = await api("POST", "/api/integrations/connections", {
        provider: provider.trim(),
        accountRef: accountRef.trim(),
        credentialRef: credentialRef.trim(),
        capabilities: capabilities.split(",").map((value) => value.trim()).filter(Boolean),
      });
      setSelectedID(data.connection.id);
      setCreateOpen(false);
      setAdvancedCreateOpen(false);
      setAccountRef("");
      setCredentialRef("");
    });
  };

  const changeProvider = (value: string) => {
    const spec = providerSpec(value);
    setProvider(value);
    setCredentialRef("");
    setCapabilities(spec.capabilities.join(","));
  };

  const resetAddressForm = () => {
    setBindOpen(false);
    setEditingAddressID("");
    setIdentity("");
    setDisplayName("");
    setAllowActors("");
    setAllowConversations("");
    setBlockActors("");
    setBlockConversations("");
  };

  const startBind = () => {
    resetAddressForm();
    setAgent("");
    setTriggerPolicy("mention");
    setReplyPolicy("final_answer");
    setTrustDomain("local");
    setBindOpen(true);
  };

  const editAddress = (address: AgentAddress) => {
    setEditingAddressID(address.id);
    setAgent(agentName(address.agentId));
    setIdentity(address.externalIdentity);
    setDisplayName(address.displayName || "");
    setTriggerPolicy(address.triggerPolicy);
    setReplyPolicy(address.replyPolicy);
    setTrustDomain(address.trustDomain);
    setAllowActors((address.allowActors || []).join(", "));
    setAllowConversations((address.allowConversations || []).join(", "));
    setBlockActors((address.blockActors || []).join(", "));
    setBlockConversations((address.blockConversations || []).join(", "));
    setBindOpen(true);
  };

  const bindAddress = () => {
    if (!selected || (!editingAddressID && !agent) || !identity.trim() || !trustDomain.trim()) {
      onError("agent, identity and trust domain are required");
      return;
    }
    run(async () => {
      const payload = {
        connectionId: selected.id,
        externalIdentity: identity.trim(),
        displayName: displayName.trim(),
        triggerPolicy,
        replyPolicy,
        trustDomain: trustDomain.trim(),
        allowActors: parseList(allowActors),
        allowConversations: parseList(allowConversations),
        blockActors: parseList(blockActors),
        blockConversations: parseList(blockConversations),
      };
      if (editingAddressID) {
        await api("PATCH", `/api/integrations/addresses/${encodeURIComponent(editingAddressID)}`, payload);
      } else {
        await api("POST", `/api/agents/${encodeURIComponent(agent)}/addresses`, payload);
      }
      resetAddressForm();
    });
  };

  const toggleConnection = (connection: PlatformConnection) =>
    run(async () => {
      await api("PATCH", `/api/integrations/connections/${encodeURIComponent(connection.id)}`, {
        enabled: !connection.enabled,
      });
    });

  const toggleAddress = (address: AgentAddress) =>
    run(async () => {
      await api("PATCH", `/api/integrations/addresses/${encodeURIComponent(address.id)}`, {
        enabled: !address.enabled,
      });
    });

  stateRef.current = {
    connectionsCount: activeConnections.length,
    archivedConnectionsCount: archivedConnections.length,
    connectedCount,
    addressesCount: addresses.length,
    selectedConnectionID: selectedID,
    createProvider: provider,
    larkSetupOpen: operatorFlow?.provider === "lark",
    larkReady: Boolean(selected?.provider === "lark" && selected.enabled && selected.status === "connected"),
    larkChatsCount: selected?.provider === "lark" ? selectedMemberships.filter((membership) => membership.conversationType !== "dm").length : 0,
    slackSetupOpen: operatorFlow?.provider === "slack",
    slackReady: Boolean(selected?.provider === "slack" && selected.enabled && selected.status === "connected"),
    slackChannelsCount: selected?.provider === "slack" ? selectedMemberships.filter((membership) => membership.conversationType !== "dm").length : 0,
    parallSetupOpen: operatorFlow?.provider === "parall",
    parallReady: Boolean(selected?.provider === "parall" && selected.enabled && selected.status === "connected"),
    parallAgentsCount: selected?.provider === "parall" ? selectedAddresses.length : 0,
    parallChatsCount: selected?.provider === "parall" ? selectedMemberships.filter((membership) => membership.conversationType !== "dm").length : 0,
    conversationCandidatesCount: conversationCandidates.filter((candidate) => candidate.available).length,
    unconfiguredConversationCandidatesCount: unconfiguredCandidateCount,
  };
  useEffect(() => {
    const root = ((((window as any).codexLoom ||= (window as any).codexHub || {}) as Record<string, any>));
	(window as any).codexHub = root;
    root.integrations = {
      state: () => stateRef.current,
      select: async (id: string) => {
        stateRef.current = { ...stateRef.current, selectedConnectionID: id };
        setSelectedID(id);
        await new Promise((resolve) => setTimeout(resolve, 50));
        return stateRef.current;
      },
      refresh: async () => {
        await refreshAll();
        return stateRef.current;
      },
      openLarkSetup: async () => {
        openLarkSetup();
        await new Promise((resolve) => setTimeout(resolve, 50));
        return stateRef.current;
      },
      closeLarkSetup: async () => {
        closeOperatorFlow();
        await new Promise((resolve) => setTimeout(resolve, 0));
        return stateRef.current;
      },
      openSlackSetup: async () => {
        openSlackSetup();
        await new Promise((resolve) => setTimeout(resolve, 50));
        return stateRef.current;
      },
      closeSlackSetup: async () => {
        closeOperatorFlow();
        await new Promise((resolve) => setTimeout(resolve, 0));
        return stateRef.current;
      },
      openParallSetup: async () => {
        openParallSetup();
        await new Promise((resolve) => setTimeout(resolve, 50));
        return stateRef.current;
      },
      closeParallSetup: async () => {
        closeOperatorFlow();
        await new Promise((resolve) => setTimeout(resolve, 0));
        return stateRef.current;
      },
    };
    return () => { delete root.integrations; };
  }, []);

  return (
    <main className="flex w-full min-w-0 max-w-full flex-1 flex-col overflow-hidden bg-background">
      <header className="flex min-h-14 w-full max-w-full shrink-0 items-center gap-3 overflow-hidden border-b border-border bg-card/80 py-2 pl-14 pr-3 md:px-5">
        <ProviderIcon provider={selected?.provider || provider} className="size-4 shrink-0 text-primary" />
        <h1 className="min-w-0 truncate text-[15px] font-semibold">External</h1>
        <div className="hidden text-[11px] text-muted-foreground sm:block">{addresses.length} identities · {memberships.filter((membership) => !membership.archivedAt).length} conversation roles · {connectedCount} connected</div>
        <div className="ml-auto flex items-center gap-1">
          <button onClick={() => refreshAll().catch((error: Error) => onError(error.message))} title="Refresh" className="flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"><RefreshCw className="size-3.5" /></button>
          <button onClick={() => { setCreateOpen((value) => !value); setAdvancedCreateOpen(false); setOperatorFlow(null); }} title="Add integration" className="flex size-8 items-center justify-center rounded-md bg-primary text-primary-foreground hover:opacity-90"><Plus className="size-3.5" /></button>
        </div>
      </header>

      {operatorFlow && (
        <OperatorCLISetup
          flow={operatorFlow}
          connection={connections.find((item) => item.id === operatorFlow.connectionID)}
          onClose={closeOperatorFlow}
          onAdvanced={() => { closeOperatorFlow(); setCreateOpen(true); setAdvancedCreateOpen(true); }}
        />
      )}

      {!operatorFlow && createOpen && (
        <section className="shrink-0 border-b border-border bg-card px-4 py-3">
          <div className="grid gap-2 sm:grid-cols-[170px_1fr_auto]">
            <select value={provider} onChange={(event) => changeProvider(event.target.value)} className={controlClass}>{providerSpecs.map((spec) => <option key={spec.id} value={spec.id}>{spec.label}</option>)}</select>
            <div className="flex min-w-0 items-center text-[11px] text-muted-foreground">New onboarding verifies the secret once and stores a Hub-issued CodexLoom managed credential.</div>
            <div className="flex items-center gap-1"><button onClick={() => provider === "lark" ? openLarkSetup() : provider === "slack" ? openSlackSetup() : openParallSetup()} disabled={working} className="h-8 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50">Continue</button><button onClick={() => { setCreateOpen(false); setAdvancedCreateOpen(false); }} title="Close" className="flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted"><X className="size-3.5" /></button></div>
          </div>
          <button onClick={() => setAdvancedCreateOpen((value) => !value)} className="mt-2 text-[10.5px] text-muted-foreground underline decoration-border underline-offset-4 hover:text-foreground">Legacy / advanced compatibility</button>
          {advancedCreateOpen && <div className="mt-3 border-l-2 border-warning bg-warning/5 px-3 py-3">
            <div className="text-[11px] font-semibold">Existing compatibility credential only</div>
            <p className="mt-1 text-[10.5px] leading-4 text-muted-foreground">Bind an existing <span className="font-mono text-foreground">env:</span> or <span className="font-mono text-foreground">keychain:</span> reference without displaying its secret. Managed references are issued and validated by the Hub; they cannot be entered or constructed here.</p>
            <div className="mt-3 grid gap-2 md:grid-cols-[1fr_1.4fr_1.4fr_auto]">
              <input value={accountRef} onChange={(event) => setAccountRef(event.target.value)} aria-label="Compatibility account reference" placeholder={createProvider.accountPlaceholder} className={controlClass} />
              <input value={credentialRef} onChange={(event) => setCredentialRef(event.target.value)} aria-label="Legacy credential reference" placeholder="env:VARIABLE or keychain:service" autoComplete="off" className={controlClass} />
              <input value={capabilities} onChange={(event) => setCapabilities(event.target.value)} aria-label="Compatibility capabilities" placeholder="capabilities (comma separated)" className={controlClass} />
              <button onClick={createConnection} disabled={working || !isLegacyCredentialRef(credentialRef)} className="h-8 rounded-md border border-border px-3 text-[11px] font-medium hover:bg-muted disabled:opacity-45">Create compatibility connection</button>
            </div>
          </div>}
        </section>
      )}

      {!operatorFlow && <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden lg:grid-cols-[320px_1fr]">
        <section className={`${selected ? "hidden lg:block" : "block"} min-h-0 overflow-y-auto border-r border-border`}>
          <div className="border-b border-border px-4 py-2 text-[9px] font-semibold uppercase text-muted-foreground">Agents and external identities</div>
          {activeConnections.map((connection) => {
            const boundAddresses = addresses.filter((address) => address.connectionId === connection.id && !address.archivedAt && !address.deletedAt);
            const identityName = boundAddresses[0]?.displayName || boundAddresses[0]?.externalIdentity;
            const ownerName = agents.find((agent) => agent.id === boundAddresses[0]?.agentId)?.name || "Unassigned";
            const conversationCount = memberships.filter((membership) => boundAddresses.some((address) => address.id === membership.addressId) && !membership.archivedAt).length;
            return <button key={connection.id} onClick={() => setSelectedID(connection.id)} className={`block w-full border-b border-border px-4 py-3 text-left ${selectedID === connection.id ? "bg-selection text-selection-foreground" : "hover:bg-muted/45"}`}>
              <div className="mb-1 truncate font-mono text-[8.5px] uppercase text-muted-foreground">{ownerName}</div>
              <div className="flex min-w-0 items-center gap-2"><ConnectionDot connection={connection} /><ProviderIcon provider={connection.provider} className="size-3.5 shrink-0" /><span className="min-w-0 flex-1 truncate text-[13px] font-semibold">{identityName || `Set up ${providerSpec(connection.provider).label}`}</span><span className="font-mono text-[9px] uppercase text-muted-foreground">{connection.status}</span></div>
              <div className="mt-1 text-[10px] text-muted-foreground">{providerSpec(connection.provider).label} · {conversationCount} conversation role{conversationCount === 1 ? "" : "s"}</div>
            </button>;
          })}
          {activeConnections.length === 0 && <Empty label="No external identities" />}
          {archivedConnections.length > 0 && <details className="group border-b border-border bg-muted/20">
            <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-2.5 text-[10px] font-semibold uppercase text-muted-foreground"><ChevronDown className="size-3 transition-transform group-open:rotate-180" />Archived <span className="font-mono">{archivedConnections.length}</span></summary>
            <div className="border-t border-border/60">
              {archivedConnections.map((connection) => {
                const boundAddresses = addresses.filter((address) => address.connectionId === connection.id);
                return <button key={connection.id} onClick={() => setSelectedID(connection.id)} className={`block w-full border-b border-border/60 px-4 py-2.5 text-left last:border-b-0 ${selectedID === connection.id ? "bg-selection text-selection-foreground" : "opacity-70 hover:bg-muted/45"}`}>
                  <div className="flex min-w-0 items-center gap-2"><ProviderIcon provider={connection.provider} className="size-3.5 shrink-0" /><span className="min-w-0 flex-1 truncate text-[12px] font-medium">{boundAddresses[0]?.displayName || providerSpec(connection.provider).label}</span><span className="font-mono text-[8px] uppercase">archived</span></div>
                  <div className="mt-1 truncate font-mono text-[9px] text-muted-foreground">superseded by {connection.supersededBy || "-"}</div>
                </button>;
              })}
            </div>
          </details>}
        </section>

        <section className={`${selected ? "block" : "hidden lg:block"} min-h-0 overflow-y-auto`}>
          {selected ? (
            <div className="mx-auto max-w-4xl p-4 md:p-6">
              <button onClick={() => setSelectedID("")} className="mb-4 text-[12px] text-muted-foreground lg:hidden">← External identities</button>
              <div className="flex min-w-0 items-start gap-3 border-b border-border pb-4">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted"><ProviderIcon provider={selected.provider} className="size-4 text-primary" /></div>
                <div className="min-w-0"><h2 className="truncate text-lg font-semibold">{selectedAddresses[0]?.displayName || selectedProvider.label}</h2><div className="truncate text-[10px] text-muted-foreground">{selectedProvider.label}<span className="font-mono"> · {selected.accountRef || selected.id}</span></div></div>
                {selected.archivedAt ? <span className="ml-auto rounded-[3px] border border-border px-2 py-1 font-mono text-[9px] uppercase text-muted-foreground">Archived</span> : <button onClick={() => toggleConnection(selected)} disabled={working} title={selected.enabled ? "Disable connection" : "Enable connection"} className={`ml-auto flex h-8 items-center gap-1.5 rounded-md px-2.5 text-[11px] font-medium ${selected.enabled ? "border border-border text-muted-foreground" : "bg-primary text-primary-foreground"}`}>
                  {selected.enabled ? <Unplug className="size-3.5" /> : <Check className="size-3.5" />}{selected.enabled ? "Disable" : "Enable"}
                </button>}
              </div>
              {selected.archivedAt && <div className="mt-4 border-l-2 border-border bg-muted/35 px-3 py-2 text-[11px] leading-4 text-muted-foreground">This historical transport is read-only. Messages still reference its stable IDs; current delivery uses <span className="font-mono text-foreground">{selected.supersededBy || "the replacement connection"}</span>.</div>}
              <CredentialSourceSummary connection={selected} />
              {!selected.archivedAt && selected.provider === "lark" && (
                <LarkConnectionSummary
                  connection={selected}
                  addresses={selectedAddresses}
                  memberships={selectedMemberships}
                  inboxEntries={selectedInboxEntries}
                  agents={agents}
                  onError={onError}
                  onChanged={refresh}
                  onSetup={() => openOperatorFlow("lark", operatorActionForConnection(selected), selected.id)}
                />
              )}
              {!selected.archivedAt && selected.provider === "slack" && (
                <SlackConnectionSummary
                  connection={selected}
                  addresses={selectedAddresses}
                  memberships={selectedMemberships}
                  inboxEntries={selectedInboxEntries}
                  agents={agents}
                  onError={onError}
                  onChanged={refresh}
                  onSetup={() => openOperatorFlow("slack", operatorActionForConnection(selected), selected.id)}
                />
              )}
              {!selected.archivedAt && selected.provider === "parall" && (
                <ParallConnectionSummary
                  connection={selected}
                  addresses={selectedAddresses}
                  memberships={selectedMemberships}
                  candidates={selectedCandidates}
                  inboxEntries={selectedInboxEntries}
                  agents={agents}
                  onError={onError}
                  onChanged={refresh}
                  working={working}
                  onReconnect={() => openParallSetup(selected.id, "repair")}
                  onSetup={() => openOperatorFlow("parall", operatorActionForConnection(selected), selected.id)}
                />
              )}
              {selected.lastError && <div className="mt-4 border-l-2 border-destructive bg-destructive/5 px-3 py-2 text-[12px] text-destructive">{selected.lastError}</div>}
              {selected.provider === "lark" || selected.provider === "slack" || selected.provider === "parall" ? (
                <details className="group mt-4 rounded-[3px] bg-muted/30 px-3">
                  <summary className="flex cursor-pointer list-none items-center gap-2 py-2.5 text-[11px] font-semibold uppercase text-muted-foreground"><ChevronDown className="size-3.5 transition-transform group-open:rotate-180" />Advanced settings</summary>
                  <div className="border-t border-border/60 pb-3">
                    <dl className="grid gap-x-6 gap-y-3 py-4 text-[11px] sm:grid-cols-2 xl:grid-cols-3">
                      <Meta label="Status" value={selected.status} /><Meta label="Account" value={selected.accountRef || "-"} /><CredentialMeta credentialRef={selected.credentialRef} /><Meta label="Heartbeat" value={formatDate(selected.lastHeartbeatAt)} /><Meta label="Last event" value={formatDate(selected.lastEventAt)} /><Meta label="Cursor" value={selected.cursor || "-"} />
                    </dl>
                    {!selected.archivedAt && <GatewaySetup connection={selected} addresses={selectedAddresses} />}
                  </div>
                </details>
              ) : (
                <>
                  <dl className="grid gap-x-6 gap-y-3 border-b border-border py-4 text-[11px] sm:grid-cols-2 xl:grid-cols-3">
                    <Meta label="Status" value={selected.status} /><Meta label="Account" value={selected.accountRef || "-"} /><CredentialMeta credentialRef={selected.credentialRef} /><Meta label="Heartbeat" value={formatDate(selected.lastHeartbeatAt)} /><Meta label="Last event" value={formatDate(selected.lastEventAt)} /><Meta label="Cursor" value={selected.cursor || "-"} />
                  </dl>
                  {!selected.archivedAt && <GatewaySetup connection={selected} addresses={selectedAddresses} />}
                </>
              )}
              <details className="group mt-6 border-t border-border">
                <summary className="flex cursor-pointer list-none items-center gap-2 py-3 text-[11px] font-semibold uppercase text-muted-foreground"><ChevronDown className="size-3.5 transition-transform group-open:rotate-180" />Technical identity mapping</summary>
                <div className="border-t border-border/60 pb-3">
              <div className="mt-3 flex items-center justify-between"><h3 className="text-[12px] font-semibold uppercase text-muted-foreground">Agent Addresses</h3>{!selected.archivedAt && <button onClick={bindOpen ? resetAddressForm : startBind} className="flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-[11px] font-medium hover:bg-muted">{bindOpen ? <X className="size-3.5" /> : <Link2 className="size-3.5" />}{bindOpen ? "Close" : "Bind"}</button>}</div>
              {!selected.archivedAt && bindOpen && (
                <div className="mt-3 grid gap-2 rounded-[3px] bg-muted/25 p-3 sm:grid-cols-2 xl:grid-cols-3">
                  <select value={agent} disabled={Boolean(editingAddressID)} onChange={(event) => setAgent(event.target.value)} className={controlClass}><option value="">Agent</option>{agents.map((agent) => <option key={agent.id} value={agent.name}>{agent.name}</option>)}</select>
                  <input value={identity} onChange={(event) => setIdentity(event.target.value)} placeholder={selectedProvider.identityPlaceholder} className={controlClass} />
                  <input value={displayName} onChange={(event) => setDisplayName(event.target.value)} placeholder="display name" className={controlClass} />
                  <select value={triggerPolicy} onChange={(event) => setTriggerPolicy(event.target.value as AgentAddress["triggerPolicy"])} className={controlClass}><option value="direct">direct</option><option value="mention">mention</option><option value="explicit_dispatch">explicit dispatch</option><option value="all">all</option><option value="allowlist">allowlist</option></select>
                  <select value={replyPolicy} onChange={(event) => setReplyPolicy(event.target.value as AgentAddress["replyPolicy"])} className={controlClass}><option value="final_answer">final answer</option><option value="explicit">explicit</option><option value="none">none</option></select>
                  <input value={trustDomain} onChange={(event) => setTrustDomain(event.target.value)} placeholder="trust domain" className={controlClass} />
                  <input value={allowActors} onChange={(event) => setAllowActors(event.target.value)} placeholder="allow actors (comma separated)" className={controlClass} />
                  <input value={allowConversations} onChange={(event) => setAllowConversations(event.target.value)} placeholder="allow conversations" className={controlClass} />
                  <input value={blockActors} onChange={(event) => setBlockActors(event.target.value)} placeholder="block actors" className={controlClass} />
                  <input value={blockConversations} onChange={(event) => setBlockConversations(event.target.value)} placeholder="block conversations" className={controlClass} />
                  <button onClick={bindAddress} disabled={working} className="h-8 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50">{editingAddressID ? "Save address" : "Bind address"}</button>
                </div>
              )}
              <div className="mt-3 divide-y divide-border/60 rounded-[3px] bg-muted/20 px-3">
                {selectedAddresses.map((address) => (
                  <div key={address.id} className="flex min-w-0 items-center gap-3 py-3">
                    <span className={`size-2 shrink-0 rounded-full ${address.enabled ? "bg-success" : "bg-muted-foreground/40"}`} />
                    <div className="min-w-0 flex-1"><div className="truncate text-[12.5px] font-medium">{address.displayName || address.externalIdentity}</div><div className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">{agentName(address.agentId)} · {address.externalIdentity}</div></div>
                    <div className="hidden max-w-60 text-right text-[10px] text-muted-foreground sm:block"><div>{address.triggerPolicy}</div><div className="truncate">{address.replyPolicy} · {address.trustDomain}</div><div className="truncate">{policySummary(address)}</div></div>
                    {address.archivedAt ? <span className="font-mono text-[8px] uppercase text-muted-foreground">→ {address.supersededBy || "archived"}</span> : <><button onClick={() => editAddress(address)} disabled={working} title="Edit address" className="flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"><Pencil className="size-3.5" /></button>
                    <button onClick={() => toggleAddress(address)} disabled={working} title={address.enabled ? "Disable address" : "Enable address"} className="flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground">{address.enabled ? <Unplug className="size-3.5" /> : <Check className="size-3.5" />}</button></>}
                  </div>
                ))}
                {selectedAddresses.length === 0 && <div className="py-8 text-center text-[12px] text-muted-foreground">No addresses</div>}
              </div>
                </div>
              </details>
            </div>
          ) : <Empty label="Select an external identity" />}
        </section>
      </div>}
    </main>
  );
}

function OperatorCLISetup({ flow, connection, onClose, onAdvanced }: { flow: OperatorFlow; connection?: PlatformConnection; onClose: () => void; onAdvanced: () => void }) {
  const spec = providerSpec(flow.provider);
  const isMigration = flow.action === "migration";
  const isRepair = flow.action === "repair";
  const isManage = flow.action === "manage";
  const title = isRepair ? "Terminal repair required" : isMigration ? "Terminal migration required" : isManage ? "Terminal management required" : "Terminal setup required";
  const detail = isRepair
    ? "This managed Connection is eligible for an operator repair check. The CLI revalidates the active Address, managed binding, provider identity and status, and socket readiness before it starts anything. A successful request means restart was initiated; only a later heartbeat proves recovery."
    : isMigration
      ? "This legacy source remains visible for compatibility, but browser repair is disabled. Migrate the existing Connection from a terminal; its Connection, Address, Membership, Inbox and Outbox history stay attached to the same canonical IDs."
      : isManage
        ? `Provider discovery and ${flow.provider === "slack" ? "channel" : flow.provider === "lark" ? "group" : "conversation"} setup require the operator CLI in this release.`
        : `${spec.label} credential onboarding and provider discovery require the operator CLI in this release.`;
  const migrationCommand = isMigration && connection ? `loom integration credential migrate ${connection.id} --dry-run` : "";
  return (
    <section className="min-h-0 flex-1 overflow-y-auto bg-card" data-operator-cli-only={flow.action}>
      <div className="mx-auto max-w-3xl px-4 py-6 md:px-6 md:py-10">
        <div className="flex items-start gap-3 border-b border-border pb-5">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-md bg-muted"><Terminal className="size-4 text-primary" /></div>
          <div className="min-w-0 flex-1"><div className="text-[10px] font-semibold uppercase text-muted-foreground">{spec.label} · operator-only</div><h2 className="mt-1 text-[17px] font-semibold">{title}</h2></div>
          <button onClick={onClose} title="Close" className="flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted"><X className="size-4" /></button>
        </div>
        <div className="mt-5 border-l-2 border-warning bg-warning/5 px-4 py-3">
          <p className="text-[12px] leading-5 text-foreground">{detail}</p>
          <p className="mt-2 text-[11px] leading-5 text-muted-foreground">Run the accepted build's CodexLoom operator CLI in a terminal with <code className="font-mono text-foreground">CODEX_LOOM_ADMIN_TOKEN</code> explicitly configured. The browser never reads, stores, forwards, or renders the token value, and it does not call the protected onboarding, discovery, readiness, migration, or repair endpoints.</p>
          <p className="mt-2 text-[11px] leading-5 text-muted-foreground">New onboarding stores credential material in the Owner-only managed store. Managed credential material and private rollback anchors are excluded from ordinary backups; this does not claim that every other configuration file is secret-free.</p>
        </div>
        {migrationCommand && <div className="mt-4"><div className="text-[9.5px] font-semibold uppercase text-muted-foreground">Safe dry-run starting point</div><div className="mt-1 flex min-w-0 items-stretch border border-border bg-muted/25"><code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap px-3 py-2 font-mono text-[10.5px] text-foreground/80">{migrationCommand}</code><CopyCommand value={migrationCommand} /></div></div>}
        <div className="mt-5 grid gap-3 text-[11px] text-muted-foreground sm:grid-cols-3">
          <div className="rounded-[3px] bg-muted/30 p-3"><div className="font-semibold text-foreground">No browser secret path</div><p className="mt-1 leading-4">No credential or admin token input is rendered in this page.</p></div>
          <div className="rounded-[3px] bg-muted/30 p-3"><div className="font-semibold text-foreground">Fail closed</div><p className="mt-1 leading-4">A 403 means operator authorization is required, not that provider state is unknown.</p></div>
          <div className="rounded-[3px] bg-muted/30 p-3"><div className="font-semibold text-foreground">Canonical status</div><p className="mt-1 leading-4">Return here after the CLI finishes; persisted status and heartbeat remain the recovery facts.</p></div>
        </div>
        <div className="mt-6 flex flex-wrap gap-2"><button onClick={onClose} className="h-8 rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground">Return to External</button><button onClick={onAdvanced} className="h-8 rounded-md border border-border px-3 text-[11px] font-medium text-muted-foreground hover:bg-muted">Legacy / advanced compatibility</button></div>
      </div>
    </section>
  );
}

function LarkConnectionSummary({ connection, addresses, memberships, inboxEntries, agents, onSetup, onChanged, onError }: { connection: PlatformConnection; addresses: AgentAddress[]; memberships: ConversationMembership[]; inboxEntries: InboxEntry[]; agents: Agent[]; onSetup: () => void; onChanged: () => Promise<void>; onError: (message: string) => void }) {
  const address = addresses[0];
  const agentName = agents.find((agent) => agent.id === address?.agentId)?.name || "an Agent";
  const online = connection.enabled && connection.status === "connected";
  const credentialSource = credentialSourceKind(connection.credentialRef);
  const legacySource = credentialSource === "keychain" || credentialSource === "environment";
  const managedSource = credentialSource === "managed";
  const groupMemberships = memberships.filter((membership) => membership.conversationType !== "dm");
  const dmMemberships = memberships.filter((membership) => membership.conversationType === "dm");
  const heading = !connection.enabled ? "Feishu connection is disabled" : online ? "Feishu is connected" : legacySource ? "Feishu migration required" : managedSource ? "Feishu needs operator attention" : credentialSource === "missing" ? "Complete Feishu setup in terminal" : "Feishu recovery unavailable";
  const description = !connection.enabled
    ? "Enable this Connection before changing its provider setup."
    : online
      ? <>Persisted heartbeat state reports this Connection connected. Messages are routed to <span className="font-medium text-foreground">{agentName}</span>.</>
      : legacySource
        ? "This compatibility source remains visible without exposing its secret. Use the explicit-token CLI to migrate the existing Connection; browser repair and secret re-entry are disabled."
        : managedSource
          ? "The browser shows persisted Connection and heartbeat state only. Provider discovery and readiness checks require the explicit-token CLI; a 403 is an operator authorization boundary."
          : credentialSource === "missing"
            ? "Credential-backed onboarding is CLI-only in this release. The browser does not accept the App Secret."
            : "This credential source cannot be verified safely in the browser. Provider actions remain unavailable.";
  return (
    <section className="py-5">
	      <div className="grid grid-cols-[36px_minmax(0,1fr)] items-start gap-3 sm:flex sm:flex-wrap">
        <div className={`flex size-9 items-center justify-center rounded-full ${online ? "bg-success/10 text-success" : "bg-warning/10 text-warning"}`}>{online ? <Check className="size-4" /> : <Bot className="size-4" />}</div>
        <div className="min-w-0 flex-1">
          <h3 className="text-[14px] font-semibold">{heading}</h3>
          <p className="mt-0.5 text-[11.5px] leading-5 text-muted-foreground">{description}</p>
        </div>
	        {connection.enabled && credentialSource !== "unknown" ? <button onClick={onSetup} className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground"><Terminal className="size-3.5" />{legacySource ? "Migrate via CLI" : online ? "Manage via CLI" : "Open CLI instructions"}</button> : <button disabled className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md border border-border px-3 text-[11px] font-medium text-muted-foreground opacity-60"><ShieldCheck className="size-3.5" />Recovery unavailable</button>}
      </div>
      <div className="mt-4 space-y-2">
        <DirectMessages address={address} memberships={dmMemberships} inboxEntries={inboxEntries} agentName={agentName} onChanged={onChanged} onError={onError} />
        {groupMemberships.map((membership) => (
          <ConversationMembershipRow key={membership.id} membership={membership} provider="lark" connection={connection} address={address} agentName={agentName} onChanged={onChanged} onError={onError} />
        ))}
        {groupMemberships.length === 0 && <div className="py-3 pl-7 text-[10.5px] text-muted-foreground">No groups added. Group messages are blocked.</div>}
      </div>
    </section>
  );
}

function SlackConnectionSummary({ connection, addresses, memberships, inboxEntries, agents, onSetup, onChanged, onError }: { connection: PlatformConnection; addresses: AgentAddress[]; memberships: ConversationMembership[]; inboxEntries: InboxEntry[]; agents: Agent[]; onSetup: () => void; onChanged: () => Promise<void>; onError: (message: string) => void }) {
  const address = addresses[0];
  const agentName = agents.find((agent) => agent.id === address?.agentId)?.name || "an Agent";
  const online = connection.enabled && connection.status === "connected";
  const credentialSource = credentialSourceKind(connection.credentialRef);
  const legacySource = credentialSource === "keychain" || credentialSource === "environment";
  const managedSource = credentialSource === "managed";
  const channelMemberships = memberships.filter((membership) => membership.conversationType !== "dm");
  const dmMemberships = memberships.filter((membership) => membership.conversationType === "dm");
  const heading = !connection.enabled ? "Slack connection is disabled" : online ? "Slack is connected" : legacySource ? "Slack migration required" : managedSource ? "Slack needs operator attention" : credentialSource === "missing" ? "Complete Slack setup in terminal" : "Slack recovery unavailable";
  const description = !connection.enabled
    ? "Enable this Connection before changing its provider setup."
    : online
      ? <>Persisted heartbeat state reports this Connection connected. Messages are routed to <span className="font-medium text-foreground">{agentName}</span>.</>
      : legacySource
        ? "This compatibility source remains visible without exposing its tokens. Use the explicit-token CLI to migrate the existing Connection; browser repair and token re-entry are disabled."
        : managedSource
          ? "The browser shows persisted Connection and heartbeat state only. Provider discovery and Socket Mode readiness require the explicit-token CLI; a 403 is an operator authorization boundary."
          : credentialSource === "missing"
            ? "Credential-backed onboarding is CLI-only in this release. The browser does not accept Slack tokens."
            : "This credential source cannot be verified safely in the browser. Provider actions remain unavailable.";
  return <section className="py-5">
	    <div className="grid grid-cols-[36px_minmax(0,1fr)] items-start gap-3 sm:flex sm:flex-wrap">
      <div className={`flex size-9 items-center justify-center rounded-full ${online ? "bg-success/10 text-success" : "bg-warning/10 text-warning"}`}>{online ? <Check className="size-4" /> : <MessageSquare className="size-4" />}</div>
	    <div className="min-w-0 flex-1"><h3 className="text-[14px] font-semibold">{heading}</h3><p className="mt-0.5 text-[11.5px] leading-5 text-muted-foreground">{description}</p></div>
	      {connection.enabled && credentialSource !== "unknown" ? <button onClick={onSetup} className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground"><Terminal className="size-3.5" />{legacySource ? "Migrate via CLI" : online ? "Manage via CLI" : "Open CLI instructions"}</button> : <button disabled className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md border border-border px-3 text-[11px] font-medium text-muted-foreground opacity-60"><ShieldCheck className="size-3.5" />Recovery unavailable</button>}
    </div>
    <div className="mt-4 space-y-2">
      <DirectMessages address={address} memberships={dmMemberships} inboxEntries={inboxEntries} agentName={agentName} onChanged={onChanged} onError={onError} />
      {channelMemberships.map((membership) => <ConversationMembershipRow key={membership.id} membership={membership} provider="slack" connection={connection} address={address} agentName={agentName} onChanged={onChanged} onError={onError} />)}
      {channelMemberships.length === 0 && <div className="py-3 pl-7 text-[10.5px] text-muted-foreground">No channels added. Channel messages are blocked.</div>}
    </div>
  </section>;
}

function ParallConnectionSummary({ connection, addresses, memberships, candidates, inboxEntries, agents, working, onReconnect, onSetup, onChanged, onError }: { connection: PlatformConnection; addresses: AgentAddress[]; memberships: ConversationMembership[]; candidates: ConversationCandidate[]; inboxEntries: InboxEntry[]; agents: Agent[]; working: boolean; onReconnect: () => void; onSetup: () => void; onChanged: () => Promise<void>; onError: (message: string) => void }) {
  const address = addresses.find((item) => item.enabled && !item.archivedAt && !item.deletedAt) || addresses[0];
  const agentName = agents.find((agent) => agent.id === address?.agentId)?.name || "an Agent";
  const online = connection.enabled && connection.status === "connected";
  const conversationMemberships = memberships.filter((membership) => membership.conversationType !== "dm");
  const dmMemberships = memberships.filter((membership) => membership.conversationType === "dm");
  const configuredConversationIDs = new Set(conversationMemberships.map((membership) => membership.conversationId));
  const unconfiguredCandidates = candidates.filter((candidate) => candidate.available && candidate.conversationType !== "dm" && !configuredConversationIDs.has(candidate.conversationId));
  const source = credentialSourceKind(connection.credentialRef);
  const legacySource = source === "keychain" || source === "environment";
  const canRestart = canOfferParallRepairCLI(connection, address);
  const recoveryUnavailable = !online && connection.enabled && source !== "missing" && !canRestart;
  const heading = !connection.enabled
    ? "Parall connection is disabled"
    : online
      ? "Parall is connected"
      : legacySource
        ? "Parall migration required"
        : canRestart
          ? "Parall gateway needs attention"
          : source === "missing"
            ? "Connect the Parall identity"
            : "Parall recovery unavailable";
  const description = !connection.enabled
    ? "Enable this connection before checking or recovering its gateway."
    : online
      ? <>Persisted heartbeat state reports this Connection connected. Dispatches for <span className="font-medium text-foreground">{address?.displayName || "the external identity"}</span> are routed to <span className="font-medium text-foreground">{agentName}</span>.</>
      : legacySource
        ? "This legacy credential source remains visible for compatibility, but it is not eligible for automatic or manual restart. Use the explicit-token CLI to migrate this Connection; secret re-entry is not offered here."
        : canRestart
          ? "This persisted Connection and active Address qualify for the managed repair CLI. The CLI revalidates credential binding, provider identity/status, and WebSocket readiness; only a later heartbeat proves recovery."
          : source === "missing"
            ? "Complete onboarding from the explicit-token CLI. This browser does not accept a Parall key."
            : "The persisted Connection or Address is incomplete, disabled, archived, or unsupported. Browser repair stays fail closed.";
  return <section className="py-5">
	    <div className="grid grid-cols-[36px_minmax(0,1fr)] items-start gap-3 sm:flex sm:flex-wrap">
      <div className={`flex size-9 items-center justify-center rounded-full ${online ? "bg-success/10 text-success" : "bg-warning/10 text-warning"}`}>{online ? <Check className="size-4" /> : <Cable className="size-4" />}</div>
      <div className="min-w-0 flex-1"><h3 className="text-[14px] font-semibold">{heading}</h3><p className="mt-0.5 text-[11.5px] leading-5 text-muted-foreground">{description}</p></div>
	      {online ? <button onClick={onSetup} disabled={working} className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground disabled:opacity-45"><Terminal className="size-3.5" />Manage via CLI</button> : canRestart ? <button onClick={onReconnect} disabled={working} className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground disabled:opacity-45"><Terminal className="size-3.5" />Restart via CLI</button> : source === "missing" && connection.enabled ? <button onClick={onSetup} disabled={working} className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground disabled:opacity-45"><Terminal className="size-3.5" />Setup via CLI</button> : legacySource && connection.enabled ? <button onClick={onSetup} disabled={working} className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md border border-warning/40 bg-warning/5 px-3 text-[11px] font-medium text-warning disabled:opacity-45" data-migration-required><Terminal className="size-3.5" />Migration required</button> : recoveryUnavailable ? <button disabled className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md border border-border px-3 text-[11px] font-medium text-muted-foreground opacity-60"><ShieldCheck className="size-3.5" />Repair unavailable</button> : null}
    </div>
    <div className="mt-4 space-y-2">
      <DirectMessages address={address} memberships={dmMemberships} inboxEntries={inboxEntries} agentName={agentName} onChanged={onChanged} onError={onError} />
      {unconfiguredCandidates.length > 0 && <div className="flex items-center gap-2 px-1 pt-2 text-[10px] font-semibold uppercase text-warning"><span className="size-1.5 rounded-full bg-warning" />{unconfiguredCandidates.length} joined {unconfiguredCandidates.length === 1 ? "conversation needs" : "conversations need"} a role</div>}
      {unconfiguredCandidates.map((candidate) => <ConversationCandidateRow key={candidate.id} candidate={candidate} address={address} agentName={agentName} onChanged={onChanged} onError={onError} />)}
      {conversationMemberships.map((membership) => <ConversationMembershipRow key={membership.id} membership={membership} provider="parall" connection={connection} address={address} agentName={agentName} onChanged={onChanged} onError={onError} />)}
      {conversationMemberships.length === 0 && unconfiguredCandidates.length === 0 && <div className="py-3 pl-7 text-[10.5px] text-muted-foreground">No joined group conversations discovered yet. Invite this Parall identity to a group; it will appear here automatically.</div>}
    </div>
  </section>;
}

function ConversationCandidateRow({ candidate, address, agentName, onChanged, onError }: { candidate: ConversationCandidate; address?: AgentAddress; agentName: string; onChanged: () => Promise<void>; onError: (message: string) => void }) {
  const [expanded, setExpanded] = useState(false);
  const [purpose, setPurpose] = useState(candidate.description || "");
  const [role, setRole] = useState("");
  const [guidance, setGuidance] = useState("");
  const [enableAfterSaving, setEnableAfterSaving] = useState(false);
  const [saving, setSaving] = useState(false);
  const name = candidate.displayName || candidate.conversationId;
  const save = async () => {
    if (!address || saving) return;
    if (!purpose.trim() || !role.trim()) {
      onError("Describe this conversation's purpose and the Agent's role before enabling it.");
      return;
    }
    setSaving(true);
    try {
      await api("PUT", `/api/integrations/addresses/${encodeURIComponent(address.id)}/conversations/${encodeURIComponent(candidate.conversationId)}`, {
        conversationType: candidate.conversationType,
        displayName: name,
        purpose: purpose.trim(),
        role: role.trim(),
        guidance: guidance.trim(),
        triggerPolicy: "explicit_dispatch",
        replyPolicy: "final_answer",
        enabled: enableAfterSaving,
      });
      await onChanged();
      setExpanded(false);
    } catch (error: any) {
      onError(error.message);
    } finally {
      setSaving(false);
    }
  };
  return <div className="rounded-[3px] border border-warning/30 bg-warning/5 px-3 py-3">
    <div className="flex min-w-0 items-center gap-3">
      <Users className="size-4 shrink-0 text-warning" />
      <button onClick={() => setExpanded((value) => !value)} className="min-w-0 flex-1 text-left"><span className="block truncate text-[12px] font-medium">{name}</span><span className="block truncate text-[10px] text-muted-foreground">{candidate.description || "Joined on Parall; no Loom role configured"}</span></button>
      <span className="hidden font-mono text-[8px] uppercase text-warning sm:inline">joined · not configured</span>
      <button onClick={() => setExpanded((value) => !value)} className="h-8 shrink-0 rounded-md border border-warning/35 px-2.5 text-[10.5px] font-medium text-warning hover:bg-warning/10">Configure</button>
    </div>
    {expanded && <div className="ml-7 mt-3 border-l-2 border-warning/30 pl-4">
      <div className="grid gap-3 lg:grid-cols-2">
        <label className="block text-[10.5px] font-medium text-muted-foreground">What is this conversation for?<textarea value={purpose} onChange={(event) => setPurpose(event.target.value)} placeholder="The purpose of this conversation" className={`${textAreaClass} mt-1 h-16`} /></label>
        <label className="block text-[10.5px] font-medium text-muted-foreground">What should the Agent do here?<textarea value={role} onChange={(event) => setRole(event.target.value)} placeholder={`${agentName} answers questions within its domain.`} className={`${textAreaClass} mt-1 h-16`} /></label>
      </div>
      <label className="mt-3 block text-[10.5px] font-medium text-muted-foreground">Anything it should avoid or remember?<textarea value={guidance} onChange={(event) => setGuidance(event.target.value)} placeholder="Optional boundaries and communication style" className={`${textAreaClass} mt-1 h-16`} /></label>
      <div className="mt-3 flex flex-wrap items-center justify-between gap-3">
        <label className="flex items-center gap-2 text-[10.5px] text-muted-foreground"><input type="checkbox" checked={enableAfterSaving} onChange={(event) => setEnableAfterSaving(event.target.checked)} className="size-3.5 accent-primary" />Enable after saving</label>
        <div className="flex gap-2"><button onClick={() => setExpanded(false)} disabled={saving} className="h-8 px-3 text-[11px] text-muted-foreground hover:bg-muted disabled:opacity-50">Cancel</button><button onClick={save} disabled={saving || !address || !purpose.trim() || !role.trim()} className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground disabled:opacity-45">{saving && <LoaderCircle className="size-3.5 animate-spin" />}{enableAfterSaving ? "Save and enable" : "Save as paused"}</button></div>
      </div>
    </div>}
  </div>;
}

type DirectContact = {
  conversationId: string;
  actorId: string;
  name: string;
  membership?: ConversationMembership;
  pending: InboxEntry[];
};

function DirectMessages({ address, memberships, inboxEntries, agentName, onChanged, onError }: { address?: AgentAddress; memberships: ConversationMembership[]; inboxEntries: InboxEntry[]; agentName: string; onChanged: () => Promise<void>; onError: (message: string) => void }) {
  const effectivePolicy = address?.dmPolicy || "open";
  const [expanded, setExpanded] = useState(false);
  const [policy, setPolicy] = useState<NonNullable<AgentAddress["dmPolicy"]>>(effectivePolicy);
  const [selectedID, setSelectedID] = useState("");
  const [purpose, setPurpose] = useState("");
  const [role, setRole] = useState("");
  const [guidance, setGuidance] = useState("");
  const [triggerPolicy, setTriggerPolicy] = useState<ConversationMembership["triggerPolicy"]>("direct");
  const [replyPolicy, setReplyPolicy] = useState<ConversationMembership["replyPolicy"]>(address?.replyPolicy || "final_answer");
  const [enabled, setEnabled] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => setPolicy(effectivePolicy), [effectivePolicy]);
  const contacts = useMemo(() => {
    const values = new Map<string, DirectContact>();
    for (const membership of memberships) {
      values.set(membership.conversationId, {
        conversationId: membership.conversationId,
        actorId: membership.actorId || "",
        name: membership.displayName || membership.actorId || membership.conversationId,
        membership,
        pending: [],
      });
    }
    for (const entry of inboxEntries) {
      if (!isDirectConversationType(entry.message.conversation.conversationType)) continue;
      const conversationId = entry.message.conversation.conversationId;
      const current = values.get(conversationId) || {
        conversationId,
        actorId: entry.message.sender.externalId,
        name: entry.message.sender.displayName || entry.message.sender.externalId || conversationId,
        pending: [],
      };
      if (!current.actorId) current.actorId = entry.message.sender.externalId;
      if (!current.name || current.name === current.conversationId) current.name = entry.message.sender.displayName || current.actorId || current.conversationId;
      if (entry.item.state === "pending_access") current.pending.push(entry);
      values.set(conversationId, current);
    }
    return Array.from(values.values()).sort((left, right) => left.name.localeCompare(right.name));
  }, [memberships, inboxEntries]);
  const selected = contacts.find((contact) => contact.conversationId === selectedID);

  const selectContact = (contact: DirectContact) => {
    setSelectedID(contact.conversationId);
    setPurpose(contact.membership?.purpose || "");
    setRole(contact.membership?.role || "");
    setGuidance(contact.membership?.guidance || "");
    setTriggerPolicy(contact.membership?.triggerPolicy || "direct");
    setReplyPolicy(contact.membership?.replyPolicy || address?.replyPolicy || "final_answer");
    setEnabled(contact.membership?.enabled ?? true);
  };
  const savePolicy = async () => {
    if (!address || saving || policy === effectivePolicy) return;
    setSaving(true);
    try {
      await api("PATCH", `/api/integrations/addresses/${encodeURIComponent(address.id)}`, { dmPolicy: policy });
      await onChanged();
    } catch (error: any) {
      onError(error.message);
    } finally {
      setSaving(false);
    }
  };
  const saveContact = async () => {
    if (!address || !selected || saving) return;
    setSaving(true);
    try {
      await api("PUT", `/api/integrations/addresses/${encodeURIComponent(address.id)}/conversations/${encodeURIComponent(selected.conversationId)}`, {
        conversationType: "dm",
        actorId: selected.actorId,
        displayName: selected.name,
        purpose: purpose.trim(),
        role: role.trim(),
        guidance: guidance.trim(),
        triggerPolicy,
        replyPolicy,
        trustDomain: address.trustDomain,
        enabled,
        expectedVersion: selected.membership?.version || 0,
      });
      for (const entry of selected.pending) {
        await api("POST", `/api/inbox/${encodeURIComponent(entry.item.id)}/retry`, {});
      }
      await onChanged();
      setSelectedID("");
    } catch (error: any) {
      onError(error.message);
    } finally {
      setSaving(false);
    }
  };
  const policySummary = effectivePolicy === "managed" ? `${memberships.filter((item) => item.enabled).length} configured people` : effectivePolicy === "closed" ? "No one can contact this Agent" : "Anyone can contact this Agent";

  return <div className="rounded-[3px] bg-muted/30 px-3 py-3">
    <div className="flex min-w-0 items-center gap-3">
      <MessageSquare className="size-4 shrink-0 text-muted-foreground" />
      <button onClick={() => setExpanded((value) => !value)} className="min-w-0 flex-1 text-left">
        <div className="text-[12px] font-medium">Direct messages</div>
        <div className="truncate text-[10px] text-muted-foreground">{policySummary}</div>
      </button>
      {contacts.some((contact) => contact.pending.length > 0) && <span className="rounded border border-warning/40 bg-warning/10 px-1.5 py-0.5 font-mono text-[8px] uppercase text-warning">{contacts.reduce((count, contact) => count + contact.pending.length, 0)} requests</span>}
      <span className={`hidden font-mono text-[9px] uppercase sm:inline ${effectivePolicy === "closed" ? "text-muted-foreground" : effectivePolicy === "managed" ? "text-success" : "text-warning"}`}>{effectivePolicy}</span>
      <button onClick={() => setExpanded((value) => !value)} title="Configure direct messages" aria-label="Configure direct messages" className={`flex size-8 shrink-0 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-muted hover:text-foreground ${expanded ? "bg-muted text-foreground" : ""}`}><Pencil className="size-3.5" /></button>
    </div>
    {expanded && <div className="ml-7 mt-3 border-l-2 border-border pl-4">
      <div className="grid gap-2 sm:grid-cols-[1fr_auto]">
        <label className="block"><span className="mb-1 block text-[10px] uppercase text-muted-foreground">Who may send direct messages?</span><select value={policy} onChange={(event) => setPolicy(event.target.value as NonNullable<AgentAddress["dmPolicy"]>)} className={`${controlClass} w-full`}><option value="managed">Only configured people</option><option value="open">Anyone</option><option value="closed">No one</option></select></label>
        <button onClick={savePolicy} disabled={!address || saving || policy === effectivePolicy} className="mt-4 h-8 rounded-md border border-border px-3 text-[11px] font-medium hover:bg-muted disabled:opacity-40">Apply policy</button>
      </div>
      {policy === "open" && <div className="mt-2 border-l-2 border-warning bg-warning/5 px-3 py-2 text-[10.5px] leading-4 text-muted-foreground">Unconfigured people can trigger {agentName} without person-specific role boundaries.</div>}
      <div className="mt-4 text-[10px] font-semibold uppercase text-muted-foreground">People</div>
      <div className="mt-1 divide-y divide-border border-y border-border">
        {contacts.map((contact) => <button key={contact.conversationId} onClick={() => selectContact(contact)} className={`flex w-full min-w-0 items-center gap-2 px-2 py-2 text-left ${selectedID === contact.conversationId ? "bg-selection text-selection-foreground" : "hover:bg-muted/45"}`}><span className={`size-1.5 shrink-0 rounded-full ${contact.membership?.enabled ? "bg-success" : contact.pending.length ? "bg-warning" : "bg-muted-foreground/35"}`} /><span className="min-w-0 flex-1"><span className="block truncate text-[11.5px] font-medium">{contact.name}</span><span className="block truncate font-mono text-[9px] text-muted-foreground">{contact.actorId || contact.conversationId}</span></span>{contact.pending.length > 0 && <span className="font-mono text-[8px] uppercase text-warning">request</span>}{contact.membership && <span className="font-mono text-[8px] uppercase text-muted-foreground">v{contact.membership.version}</span>}<Pencil className="size-3 shrink-0 text-muted-foreground" /></button>)}
        {contacts.length === 0 && <div className="px-3 py-4 text-center text-[10.5px] leading-4 text-muted-foreground">No DM contacts have been seen. In managed mode, a new sender appears here as an access request and does not trigger the Agent.</div>}
      </div>
      {selected && <div className="mt-4">
        <div className="flex items-start justify-between gap-3"><div><div className="text-[11.5px] font-semibold">Role with {selected.name}</div><div className="mt-0.5 font-mono text-[9px] text-muted-foreground">{selected.conversationId}{selected.membership ? ` · v${selected.membership.version}` : " · new"}</div></div><label className="flex shrink-0 items-center gap-2 text-[10.5px] text-muted-foreground"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} className="size-3.5 accent-primary" />Enabled</label></div>
        <div className="mt-3 grid gap-3 lg:grid-cols-2"><div><label className="block text-[10.5px] font-medium text-muted-foreground">What is this relationship for?</label><textarea value={purpose} onChange={(event) => setPurpose(event.target.value)} placeholder="Purpose of this private relationship" className={`${textAreaClass} mt-1 h-16`} /></div><div><label className="block text-[10.5px] font-medium text-muted-foreground">What should the Agent do for this person?</label><textarea value={role} onChange={(event) => setRole(event.target.value)} placeholder={`${agentName}'s role with this person`} className={`${textAreaClass} mt-1 h-16`} /></div></div>
        <label className="mt-3 block text-[10.5px] font-medium text-muted-foreground">Anything it should avoid or remember?</label><textarea value={guidance} onChange={(event) => setGuidance(event.target.value)} placeholder="Private boundaries and communication style" className={`${textAreaClass} mt-1 h-16`} />
        <div className="mt-3 grid gap-2 sm:grid-cols-2"><label className="block"><span className="mb-1 block text-[10px] uppercase text-muted-foreground">When should it respond?</span><select value={triggerPolicy} onChange={(event) => setTriggerPolicy(event.target.value as ConversationMembership["triggerPolicy"])} className={`${controlClass} w-full`}><option value="direct">Every DM from this person</option><option value="explicit_dispatch">Explicit dispatch only</option></select></label><label className="block"><span className="mb-1 block text-[10px] uppercase text-muted-foreground">How should it reply?</span><select value={replyPolicy} onChange={(event) => setReplyPolicy(event.target.value as ConversationMembership["replyPolicy"])} className={`${controlClass} w-full`}><option value="final_answer">Send final answer</option><option value="explicit">Only explicit replies</option><option value="none">Do not reply</option></select></label></div>
        <div className="mt-3 flex justify-end gap-2"><button onClick={() => setSelectedID("")} disabled={saving} className="h-8 px-3 text-[11px] text-muted-foreground hover:bg-muted disabled:opacity-50">Cancel</button><button onClick={saveContact} disabled={!address || saving || !selected.actorId} className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground disabled:opacity-50">{saving && <LoaderCircle className="size-3.5 animate-spin" />}{selected.pending.length ? "Approve and deliver" : selected.membership ? "Save changes" : "Configure person"}</button></div>
      </div>}
    </div>}
  </div>;
}

function isDirectConversationType(value?: string) {
  return ["dm", "p2p", "direct"].includes((value || "").toLowerCase());
}

function ConversationMembershipRow({ membership, conversation, provider, connection, address, agentName, onChanged, onError }: { membership: ConversationMembership; conversation?: { name: string; description?: string; external?: boolean }; provider: "lark" | "slack" | "parall"; connection: PlatformConnection; address?: AgentAddress; agentName: string; onChanged: () => Promise<void>; onError: (message: string) => void }) {
  const [testing, setTesting] = useState(false);
  const [sent, setSent] = useState(false);
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [purpose, setPurpose] = useState(membership.purpose || "");
  const [role, setRole] = useState(membership.role || "");
  const [guidance, setGuidance] = useState(membership.guidance || "");
  const [triggerPolicy, setTriggerPolicy] = useState(membership.triggerPolicy);
  const [replyPolicy, setReplyPolicy] = useState(membership.replyPolicy);
  const [outboundPolicy, setOutboundPolicy] = useState(membership.outboundPolicy || "reply_only");
  const [enabled, setEnabled] = useState(membership.enabled);
  const migratedName = membership.displayName && membership.displayName !== membership.conversationId ? membership.displayName : "";
  const name = conversation?.name || migratedName || membership.conversationId;
  const noun = provider === "slack" ? "channel" : provider === "parall" ? "conversation" : "group";
  const platformName = provider === "slack" ? "Slack" : provider === "parall" ? "Parall" : "Feishu";
  const summary = membership.role || membership.purpose || conversation?.description || `${provider === "slack" ? "Channel" : provider === "parall" ? "Conversation" : "Group"} role not described yet`;
  useEffect(() => {
    if (editing) return;
    setPurpose(membership.purpose || "");
    setRole(membership.role || "");
    setGuidance(membership.guidance || "");
    setTriggerPolicy(membership.triggerPolicy);
    setReplyPolicy(membership.replyPolicy);
    setOutboundPolicy(membership.outboundPolicy || "reply_only");
    setEnabled(membership.enabled);
  }, [membership, editing]);
  const save = async () => {
    if (saving) return;
    setSaving(true);
    try {
      await api("PATCH", `/api/integrations/conversations/${encodeURIComponent(membership.id)}`, {
        purpose: purpose.trim(),
        role: role.trim(),
        guidance: guidance.trim(),
        triggerPolicy,
        replyPolicy,
        outboundPolicy,
        enabled,
        expectedVersion: membership.version,
      });
      await onChanged();
      setEditing(false);
    } catch (error: any) {
      onError(error.message);
    } finally {
      setSaving(false);
    }
  };
  const test = async () => {
    if (!address || testing) return;
    setTesting(true);
    try {
      const created = await api("POST", "/api/integrations/send", {
        agent: agentName,
        membershipId: membership.id,
        content: { text: `CodexLoom connection check: ${agentName} is ready in this ${noun}.` },
        responseExpectation: "none",
        idempotencyKey: `web:${provider}-test:${crypto.randomUUID()}`,
      });
      const outboxID = created.outboxItem.id;
      let delivered = false;
      for (let attempt = 0; attempt < 24; attempt++) {
        await new Promise((resolve) => setTimeout(resolve, 250));
        const data = await api("GET", `/api/outbox?agent=${encodeURIComponent(agentName)}`);
        const item = (data.items || []).find((candidate: { id: string }) => candidate.id === outboxID);
        if (item?.state === "failed") throw new Error(item.lastError || `${platformName} rejected the test message`);
        if (item?.state === "sent") {
          delivered = true;
          break;
        }
      }
      if (!delivered) throw new Error(`Test message is still queued. Check the ${platformName} gateway status.`);
      setSent(true);
      window.setTimeout(() => setSent(false), 2500);
    } catch (error: any) {
      onError(error.message);
    } finally {
      setTesting(false);
    }
  };
  return <div className="rounded-[3px] bg-muted/30 px-3 py-3">
    <div className="flex min-w-0 items-center gap-3">
      <Users className="size-4 shrink-0 text-muted-foreground" />
      <button onClick={() => setEditing((value) => !value)} className="min-w-0 flex-1 text-left">
        <div className="truncate text-[12px] font-medium">{name}</div>
        <div className="truncate text-[10px] text-muted-foreground">{summary}</div>
      </button>
      {conversation?.external && <span className="hidden rounded border border-border px-1 py-0.5 font-mono text-[8px] uppercase text-muted-foreground sm:inline">external</span>}
      <span className={`hidden font-mono text-[9px] uppercase sm:inline ${membership.enabled ? "text-success" : "text-muted-foreground"}`}>{membership.enabled ? "enabled" : "paused"}</span>
      <button onClick={() => setEditing((value) => !value)} title="Edit group configuration" aria-label={`Edit ${name} configuration`} className={`flex size-8 shrink-0 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-muted hover:text-foreground ${editing ? "bg-muted text-foreground" : ""}`}><Pencil className="size-3.5" /></button>
      <button onClick={test} disabled={!membership.enabled || testing || !address || (membership.outboundPolicy || "reply_only") !== "proactive"} title={(membership.outboundPolicy || "reply_only") === "proactive" ? `Send a test message to this ${noun}` : `Allow proactive messages before sending a test`} className="flex size-8 shrink-0 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-40">{testing ? <LoaderCircle className="size-3.5 animate-spin" /> : sent ? <Check className="size-3.5 text-success" /> : <Send className="size-3.5" />}</button>
    </div>
    {editing && <div className="ml-7 mt-3 border-l-2 border-border pl-4">
      <div className="flex items-start justify-between gap-3">
        <div><div className="text-[11.5px] font-semibold">Role in {name}</div><div className="mt-0.5 font-mono text-[9px] text-muted-foreground">{membership.conversationId} · v{membership.version}</div></div>
        <label className="flex shrink-0 items-center gap-2 text-[10.5px] text-muted-foreground"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} className="size-3.5 accent-primary" />Enabled</label>
      </div>
      <div className="mt-3 grid gap-3 lg:grid-cols-2">
        <div>
          <label className="block text-[10.5px] font-medium text-muted-foreground">What is this {noun} for?</label>
          <textarea value={purpose} onChange={(event) => setPurpose(event.target.value)} placeholder={conversation?.description || `The purpose of this ${noun}`} className={`${textAreaClass} mt-1 h-16`} />
        </div>
        <div>
          <label className="block text-[10.5px] font-medium text-muted-foreground">What should the Agent do here?</label>
          <textarea value={role} onChange={(event) => setRole(event.target.value)} placeholder={`${agentName} answers questions in its domain.`} className={`${textAreaClass} mt-1 h-16`} />
        </div>
      </div>
      <label className="mt-3 block text-[10.5px] font-medium text-muted-foreground">Anything it should avoid or remember?</label>
      <textarea value={guidance} onChange={(event) => setGuidance(event.target.value)} placeholder="Boundaries and communication style" className={`${textAreaClass} mt-1 h-16`} />
      <div className="mt-3 grid gap-2 sm:grid-cols-3">
        <label className="block"><span className="mb-1 block text-[10px] uppercase text-muted-foreground">When should it respond?</span><select value={triggerPolicy} onChange={(event) => setTriggerPolicy(event.target.value as ConversationMembership["triggerPolicy"])} className={`${controlClass} w-full`}><option value="mention">When mentioned</option><option value="all">Every message</option><option value="explicit_dispatch">Explicit dispatch</option><option value="direct">Direct messages</option><option value="allowlist">Allowlist only</option></select></label>
        <label className="block"><span className="mb-1 block text-[10px] uppercase text-muted-foreground">How should it reply?</span><select value={replyPolicy} onChange={(event) => setReplyPolicy(event.target.value as ConversationMembership["replyPolicy"])} className={`${controlClass} w-full`}><option value="final_answer">Send final answer</option><option value="explicit">Only explicit replies</option><option value="none">Do not reply</option></select></label>
        <label className="block"><span className="mb-1 block text-[10px] uppercase text-muted-foreground">Can it initiate?</span><select value={outboundPolicy} onChange={(event) => setOutboundPolicy(event.target.value as NonNullable<ConversationMembership["outboundPolicy"]>)} className={`${controlClass} w-full`}><option value="reply_only">Replies only</option><option value="proactive">Proactive + replies</option><option value="none">No outbound messages</option></select></label>
      </div>
      <div className="mt-3 flex justify-end gap-2">
        <button onClick={() => setEditing(false)} disabled={saving} className="h-8 px-3 text-[11px] text-muted-foreground hover:bg-muted disabled:opacity-50">Cancel</button>
        <button onClick={save} disabled={saving} className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground disabled:opacity-50">{saving && <LoaderCircle className="size-3.5 animate-spin" />}{saving ? "Saving" : "Save changes"}</button>
      </div>
    </div>}
  </div>;
}

function ConnectionDot({ connection }: { connection: PlatformConnection }) {
  const color = !connection.enabled ? "bg-muted-foreground/40" : connection.status === "connected" ? "bg-success" : connection.status === "degraded" ? "bg-destructive" : connection.status === "connecting" ? "bg-warning" : "bg-muted-foreground/45";
  return <span className={`size-2 shrink-0 rounded-full ${color}`} />;
}

function CredentialSourceSummary({ connection }: { connection: PlatformConnection }) {
  const source = credentialSourcePresentation(connection.credentialRef);
  return <div className={`mt-4 border-l-2 px-3 py-2 ${source.tone}`} data-credential-source={source.kind}>
    <div className="text-[10.5px] font-semibold">{source.label}</div>
    <div className="mt-0.5 text-[10px] leading-4 opacity-80">{source.detail}</div>
  </div>;
}

function CredentialMeta({ credentialRef }: { credentialRef?: string }) {
  const source = credentialSourcePresentation(credentialRef);
  return <>
    <Meta label="Credential source" value={source.label} />
    <Meta label="Credential reference" value={source.kind === "managed" ? "Hub-issued opaque reference" : credentialRef || "-"} />
  </>;
}

function Meta({ label, value }: { label: string; value: string }) {
  return <div className="min-w-0"><dt className="uppercase text-muted-foreground">{label}</dt><dd className="mt-0.5 break-all font-mono text-foreground">{value}</dd></div>;
}

function Empty({ label }: { label: string }) {
  return <div className="flex h-full min-h-40 items-center justify-center text-[12px] text-muted-foreground">{label}</div>;
}

type ProviderSpec = {
  id: string;
  label: string;
  accountPlaceholder: string;
  identityPlaceholder: string;
  capabilities: string[];
  requirements: string[];
  manifest?: string;
};

const providerSpecs: ProviderSpec[] = [
  {
    id: "lark",
    label: "Feishu / Lark",
    accountPlaceholder: "App ID (cli_...)",
    identityPlaceholder: "Bot Open ID (ou_...)",
    capabilities: ["receive_events", "threads", "mentions", "attachments", "reactions", "proactive_send"],
    requirements: ["native Go gateway", "CodexLoom managed credential", "message and reaction scopes", "im:chat.members:read for member names"],
  },
  {
    id: "slack",
    label: "Slack",
    accountPlaceholder: "Workspace ID (T...)",
    identityPlaceholder: "Bot User ID (U...)",
    capabilities: ["receive_events", "threads", "mentions", "attachments", "reactions", "proactive_send"],
    requirements: ["managed Socket Mode gateway", "CodexLoom managed credential", "Bot invited to configured channels"],
    manifest: "gateway/slack-app-manifest.yaml",
  },
  {
    id: "parall",
    label: "Parall",
    accountPlaceholder: "Organization ID (org_...)",
    identityPlaceholder: "Parall identity (prll://...)",
    capabilities: ["receive_events", "explicit_dispatch", "threads", "attachments", "reading", "ack", "proactive_send"],
    requirements: ["managed WebSocket gateway", "CodexLoom managed credentials", "external Agent membership in configured conversations"],
  },
];

function providerSpec(provider: string) {
  return providerSpecs.find((spec) => spec.id === provider) || {
    ...providerSpecs[0],
    id: provider,
    label: provider || "Integration",
  };
}

function ProviderIcon({ provider, className }: { provider: string; className?: string }) {
  if (provider === "slack") return <MessageSquare className={className} />;
  if (provider === "lark") return <Bot className={className} />;
  return <Cable className={className} />;
}

function GatewaySetup({ connection, addresses, slackAppID = "" }: { connection: PlatformConnection; addresses: AgentAddress[]; slackAppID?: string }) {
  const spec = providerSpec(connection.provider);
  const address = addresses[0];
  const credentialSource = credentialSourceKind(connection.credentialRef);
  const command = address ? gatewayCommand(connection, address, { slackAppID }) : "";
  const online = connection.enabled && connection.status === "connected";
  const commandStatus = credentialSource === "managed"
    ? "CodexLoom manages this gateway. The browser shows persisted status only; protected readiness and repair run from the explicit-token CLI. Manual gateway commands are intentionally unavailable."
    : credentialSource === "keychain" || credentialSource === "environment"
      ? "This legacy credential source does not match a runnable compatibility command. Migrate this Connection to a managed credential."
      : !address
        ? "Bind an active Agent Address before checking gateway availability."
        : "Manual gateway commands are unavailable for this credential source. Complete managed onboarding or migrate the Connection.";
  return (
    <section className="mt-4 py-3">
      <div className="flex min-w-0 items-center gap-2">
        <Terminal className="size-3.5 shrink-0 text-muted-foreground" />
        <h3 className="text-[12px] font-semibold uppercase text-muted-foreground">Gateway</h3>
        <span className={`ml-auto rounded-md px-2 py-0.5 font-mono text-[9px] font-semibold uppercase ${online ? "bg-success/10 text-success" : connection.status === "degraded" ? "bg-destructive/10 text-destructive" : "bg-warning/10 text-warning"}`}>
          {online ? "online" : connection.status}
        </span>
      </div>
      <div className="mt-3 flex flex-wrap gap-1.5">
        {spec.requirements.map((value) => <span key={value} className="rounded-md border border-border px-2 py-1 font-mono text-[9.5px] text-muted-foreground">{value}</span>)}
      </div>
      {spec.manifest && <div className="mt-2 truncate font-mono text-[10px] text-muted-foreground">manifest · {spec.manifest}</div>}
      {command ? (
        <>
          <div className="mt-3 text-[9.5px] font-semibold uppercase text-muted-foreground">Legacy compatibility command</div>
          <div className="mt-1 flex min-w-0 items-stretch border border-border bg-muted/25" data-gateway-command="legacy">
            <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap px-3 py-2 font-mono text-[10.5px] text-foreground/80">{command}</code>
            <CopyCommand value={command} />
          </div>
        </>
      ) : <div className="mt-3 border-l-2 border-warning bg-warning/5 px-3 py-2 text-[11px] text-muted-foreground" data-gateway-command-status={credentialSource}>{commandStatus}</div>}
      {command && addresses.length > 1 && <div className="mt-2 font-mono text-[9.5px] text-warning">This command uses the first of {addresses.length} addresses.</div>}
    </section>
  );
}

function CopyCommand({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    await navigator.clipboard.writeText(value);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  };
  return <button onClick={copy} title="Copy gateway command" className="flex w-9 shrink-0 items-center justify-center border-l border-border text-muted-foreground hover:bg-muted hover:text-foreground">{copied ? <Check className="size-3.5 text-success" /> : <Copy className="size-3.5" />}</button>;
}

function formatDate(value?: string) {
  if (!value) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

const controlClass = "h-8 min-w-0 rounded-md border border-border bg-background px-2.5 text-[12px] outline-none focus:border-ring";
const textAreaClass = "w-full resize-none rounded-md border border-border bg-background px-2.5 py-2 text-[11px] leading-4 outline-none focus:border-ring";

function upsertByID<T extends { id: string }>(values: T[], next: T) {
  const index = values.findIndex((value) => value.id === next.id);
  if (index < 0) return [...values, next];
  const updated = values.slice();
  updated[index] = next;
  return updated;
}

function parseList(value: string) {
  return Array.from(new Set(value.split(",").map((item) => item.trim()).filter(Boolean)));
}

function policySummary(address: AgentAddress) {
  const values = [
    address.allowActors?.length ? `allow actors ${address.allowActors.length}` : "",
    address.allowConversations?.length ? `allow chats ${address.allowConversations.length}` : "",
    address.blockActors?.length ? `block actors ${address.blockActors.length}` : "",
    address.blockConversations?.length ? `block chats ${address.blockConversations.length}` : "",
  ].filter(Boolean);
  return values.join(" · ") || "no filters";
}

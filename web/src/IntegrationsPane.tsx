import { Bot, Cable, Check, ChevronDown, CircleCheck, Copy, Link2, LoaderCircle, MessageSquare, Pencil, Plus, RefreshCw, Search, Send, ShieldCheck, Terminal, Unplug, Users, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, type AgentAddress, type PlatformConnection, type Agent, type ConversationCandidate, type ConversationMembership, type InboxEntry, type LarkDiscovery, type SlackDiscovery, type ParallDiscovery } from "./types";

export function IntegrationsPane({ agents, onError }: { agents: Agent[]; onError: (message: string) => void }) {
  const [connections, setConnections] = useState<PlatformConnection[]>([]);
  const [addresses, setAddresses] = useState<AgentAddress[]>([]);
  const [memberships, setMemberships] = useState<ConversationMembership[]>([]);
  const [conversationCandidates, setConversationCandidates] = useState<ConversationCandidate[]>([]);
  const [inboxEntries, setInboxEntries] = useState<InboxEntry[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
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
  const [larkSetupOpen, setLarkSetupOpen] = useState(false);
  const [larkSetupMode, setLarkSetupMode] = useState<"connect" | "add-group">("connect");
  const [larkDiscovery, setLarkDiscovery] = useState<LarkDiscovery | null>(null);
  const [larkDiscoveryLoading, setLarkDiscoveryLoading] = useState(false);
  const [slackSetupOpen, setSlackSetupOpen] = useState(false);
  const [slackSetupMode, setSlackSetupMode] = useState<"connect" | "add-channel">("connect");
  const [slackDiscovery, setSlackDiscovery] = useState<SlackDiscovery | null>(null);
  const [slackDiscoveryLoading, setSlackDiscoveryLoading] = useState(false);
  const [parallSetupOpen, setParallSetupOpen] = useState(false);
  const [parallSetupMode, setParallSetupMode] = useState<"connect" | "add-conversation">("connect");
  const [parallDiscovery, setParallDiscovery] = useState<ParallDiscovery | null>(null);
  const [parallDiscoveryLoading, setParallDiscoveryLoading] = useState(false);
  const stateRef = useRef<Record<string, unknown>>({});
  const selectedAddressIDsRef = useRef<string[]>([]);
  const inboxRefreshTimerRef = useRef<number | null>(null);
  const coreRefreshTimerRef = useRef<number | null>(null);
  const selected = connections.find((connection) => connection.id === selectedID) || null;
  const selectedAddresses = addresses.filter((address) => address.connectionId === selectedID && (selected?.archivedAt ? true : !address.archivedAt));
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

  const discoverLark = async (appID = "") => {
    setLarkDiscoveryLoading(true);
    try {
      const suffix = appID.trim() ? `?appId=${encodeURIComponent(appID.trim())}` : "";
      const data = await api("GET", `/api/integrations/providers/lark/discovery${suffix}`);
      const discovery = normalizeLarkDiscovery(data.discovery as LarkDiscovery);
      setLarkDiscovery(discovery);
      return discovery;
    } catch (error: any) {
      const discovery = { available: false, runtime: "native", credentialStored: false, botReady: false, chats: [], error: error.message } as LarkDiscovery;
      setLarkDiscovery(discovery);
      return discovery;
    } finally {
      setLarkDiscoveryLoading(false);
    }
  };

  const saveLarkCredentials = async (appID: string, appSecret: string) => {
    setLarkDiscoveryLoading(true);
    try {
      const data = await api("POST", "/api/integrations/providers/lark/credentials", { appId: appID.trim(), appSecret });
      const discovery = normalizeLarkDiscovery(data.discovery as LarkDiscovery);
      setLarkDiscovery(discovery);
      return discovery;
    } finally {
      setLarkDiscoveryLoading(false);
    }
  };

  const openLarkSetup = (appID = "", mode: "connect" | "add-group" = "connect") => {
    setCreateOpen(false);
    setLarkSetupMode(mode);
    setLarkSetupOpen(true);
    window.history.replaceState(null, "", "#integrations?setup=lark");
    if (appID.trim()) {
      discoverLark(appID);
    } else {
      setLarkDiscovery({ available: true, runtime: "native", appId: "", credentialStored: false, botReady: false, chats: [] });
    }
  };

  const closeLarkSetup = () => {
    setLarkSetupOpen(false);
    window.history.replaceState(null, "", "#integrations");
  };

  const discoverSlack = async (connectionID = "", appID = "") => {
    setSlackDiscoveryLoading(true);
    try {
      const params = new URLSearchParams();
      if (connectionID.trim()) params.set("connectionId", connectionID.trim());
      if (appID.trim()) params.set("appId", appID.trim());
      const data = await api("GET", `/api/integrations/providers/slack/discovery${params.size ? `?${params}` : ""}`);
      const discovery = normalizeSlackDiscovery(data.discovery as SlackDiscovery);
      setSlackDiscovery(discovery);
      return discovery;
    } catch (error: any) {
      const discovery = { available: false, runtime: "managed-socket-mode", credentialStored: false, botReady: false, socketReady: false, channels: [], error: error.message } as SlackDiscovery;
      setSlackDiscovery(discovery);
      return discovery;
    } finally {
      setSlackDiscoveryLoading(false);
    }
  };

  const saveSlackCredentials = async (botToken: string, appToken: string) => {
    setSlackDiscoveryLoading(true);
    try {
      const data = await api("POST", "/api/integrations/providers/slack/credentials", { botToken, appToken });
      const discovery = normalizeSlackDiscovery(data.discovery as SlackDiscovery);
      setSlackDiscovery(discovery);
      return discovery;
    } finally {
      setSlackDiscoveryLoading(false);
    }
  };

  const openSlackSetup = (connectionID = "", mode: "connect" | "add-channel" = "connect") => {
    setCreateOpen(false);
    setSlackSetupMode(mode);
    setSlackSetupOpen(true);
    window.history.replaceState(null, "", "#integrations?setup=slack");
    if (connectionID.trim()) {
      discoverSlack(connectionID);
    } else {
      setSlackDiscovery({ available: true, runtime: "managed-socket-mode", credentialStored: false, botReady: false, socketReady: false, channels: [] });
    }
  };

  const closeSlackSetup = () => {
    setSlackSetupOpen(false);
    window.history.replaceState(null, "", "#integrations");
  };

  const discoverParall = async (connectionID = "", orgID = "", agentID = "") => {
    setParallDiscoveryLoading(true);
    try {
      const params = new URLSearchParams();
      if (connectionID.trim()) params.set("connectionId", connectionID.trim());
      if (orgID.trim()) params.set("orgId", orgID.trim());
      if (agentID.trim()) params.set("agentId", agentID.trim());
      const data = await api("GET", `/api/integrations/providers/parall/discovery${params.size ? `?${params}` : ""}`);
      const discovery = normalizeParallDiscovery(data.discovery as ParallDiscovery);
      setParallDiscovery(discovery);
      return discovery;
    } catch (error: any) {
      const discovery = { available: false, runtime: "managed-websocket", ownerCredentialStored: false, ownerReady: false, agentCredentialStored: false, externalReady: false, socketReady: false, agents: [], chats: [], error: error.message } as ParallDiscovery;
      setParallDiscovery(discovery);
      return discovery;
    } finally {
      setParallDiscoveryLoading(false);
    }
  };

  const saveParallCredentials = async (apiUrl: string, orgId: string, ownerApiKey: string) => {
    setParallDiscoveryLoading(true);
    try {
      const data = await api("POST", "/api/integrations/providers/parall/credentials", { apiUrl: apiUrl.trim(), orgId: orgId.trim(), ownerApiKey });
      const discovery = normalizeParallDiscovery(data.discovery as ParallDiscovery);
      setParallDiscovery(discovery);
      return discovery;
    } finally {
      setParallDiscoveryLoading(false);
    }
  };

  const openParallSetup = (connectionID = "", mode: "connect" | "add-conversation" = "connect") => {
    setCreateOpen(false);
    setParallSetupMode(mode);
    setParallSetupOpen(true);
    window.history.replaceState(null, "", "#integrations?setup=parall");
    if (connectionID.trim()) discoverParall(connectionID);
    else setParallDiscovery({ available: true, runtime: "managed-websocket", apiUrl: "https://api.parall.com", ownerCredentialStored: false, ownerReady: false, agentCredentialStored: false, externalReady: false, socketReady: false, agents: [], chats: [] });
  };

  const closeParallSetup = () => {
    setParallSetupOpen(false);
    window.history.replaceState(null, "", "#integrations");
  };

  useEffect(() => {
    refresh().catch((error: Error) => onError(error.message));
    const params = new URLSearchParams(window.location.hash.split("?")[1] || "");
    if (params.get("setup") === "lark") {
      setLarkSetupOpen(true);
      discoverLark();
    } else if (params.get("setup") === "slack") {
      setSlackSetupOpen(true);
      discoverSlack();
    } else if (params.get("setup") === "parall") {
      setParallSetupOpen(true);
      discoverParall();
    }
    const es = new EventSource("/api/events");
    es.onmessage = (event) => {
      try {
        const value = JSON.parse(event.data);
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
    };
    return () => {
      es.close();
      if (coreRefreshTimerRef.current !== null) window.clearTimeout(coreRefreshTimerRef.current);
      if (inboxRefreshTimerRef.current !== null) window.clearTimeout(inboxRefreshTimerRef.current);
    };
  }, []);

  useEffect(() => {
    refreshSelectedInbox(selectedAddressIDs).catch((error: Error) => onError(error.message));
  }, [selectedAddressKey]);

  useEffect(() => {
    if (!larkSetupOpen && selected?.provider === "lark" && !selected.archivedAt && selected.accountRef && larkDiscovery?.appId !== selected.accountRef) {
      discoverLark(selected.accountRef);
    }
  }, [selected?.id, larkSetupOpen]);

  useEffect(() => {
    if (!slackSetupOpen && selected?.provider === "slack" && !selected.archivedAt && slackDiscovery?.teamId !== selected.accountRef) {
      discoverSlack(selected.id);
    }
  }, [selected?.id, slackSetupOpen]);

  useEffect(() => {
    if (!parallSetupOpen && selected?.provider === "parall" && !selected.archivedAt && parallDiscovery?.orgId !== selected.accountRef) {
      discoverParall(selected.id);
    }
  }, [selected?.id, parallSetupOpen]);

  const activeConnections = connections.filter((connection) => !connection.archivedAt);
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

  const restartParallGateway = (connectionID: string) => run(async () => {
    await api("POST", "/api/integrations/providers/parall/gateway", { connectionId: connectionID });
    await discoverParall(connectionID);
  });

  const createConnection = () => {
    if (!provider.trim()) {
      onError("provider is required");
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
      setAccountRef("");
      setCredentialRef("");
    });
  };

  const changeProvider = (value: string) => {
    const spec = providerSpec(value);
    setProvider(value);
    setCredentialRef(spec.defaultCredentialRef);
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
    larkSetupOpen,
    larkReady: Boolean(larkDiscovery?.botReady),
    larkChatsCount: larkDiscovery?.chats.length || 0,
    slackSetupOpen,
    slackReady: Boolean(slackDiscovery?.botReady && slackDiscovery?.socketReady),
    slackChannelsCount: slackDiscovery?.channels.length || 0,
    parallSetupOpen,
    parallReady: Boolean(parallDiscovery?.externalReady && parallDiscovery?.socketReady) || Boolean(selected?.provider === "parall" && selected.enabled && selected.status === "connected"),
    parallAgentsCount: parallDiscovery?.agents.length || 0,
    parallChatsCount: parallDiscovery?.chats.length || 0,
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
        closeLarkSetup();
        await new Promise((resolve) => setTimeout(resolve, 0));
        return stateRef.current;
      },
      openSlackSetup: async () => {
        openSlackSetup();
        await new Promise((resolve) => setTimeout(resolve, 50));
        return stateRef.current;
      },
      closeSlackSetup: async () => {
        closeSlackSetup();
        await new Promise((resolve) => setTimeout(resolve, 0));
        return stateRef.current;
      },
      openParallSetup: async () => {
        openParallSetup();
        await new Promise((resolve) => setTimeout(resolve, 50));
        return stateRef.current;
      },
      closeParallSetup: async () => {
        closeParallSetup();
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
        <h1 className="min-w-0 truncate font-serif text-xl tracking-tight">Integrations</h1>
        <div className="hidden text-[11px] text-muted-foreground sm:block">{connectedCount}/{connections.length} connected · {addresses.length} addresses</div>
        <div className="ml-auto flex items-center gap-1">
          <button onClick={() => refreshAll().catch((error: Error) => onError(error.message))} title="Refresh" className="flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"><RefreshCw className="size-3.5" /></button>
          <button onClick={() => { setCreateOpen((value) => !value); setLarkSetupOpen(false); setSlackSetupOpen(false); setParallSetupOpen(false); }} title="Add integration" className="flex size-8 items-center justify-center rounded-md bg-primary text-primary-foreground hover:opacity-90"><Plus className="size-3.5" /></button>
        </div>
      </header>

      {larkSetupOpen && (
        <LarkSetup
          agents={agents}
          addresses={addresses}
          connections={connections}
          discovery={larkDiscovery}
          loading={larkDiscoveryLoading}
          working={working}
          requireGroup={larkSetupMode === "add-group"}
          onRefreshDiscovery={discoverLark}
          onSaveCredentials={saveLarkCredentials}
          onClose={closeLarkSetup}
          onAdvanced={() => { setLarkSetupOpen(false); setCreateOpen(true); }}
          onComplete={async (connectionID) => {
            await refresh();
            setSelectedID(connectionID);
            closeLarkSetup();
          }}
          onRun={run}
          onError={onError}
        />
      )}

      {slackSetupOpen && (
        <SlackSetup
          agents={agents}
          addresses={addresses}
          connections={connections}
          discovery={slackDiscovery}
          loading={slackDiscoveryLoading}
          working={working}
          requireChannel={slackSetupMode === "add-channel"}
          onRefreshDiscovery={discoverSlack}
          onSaveCredentials={saveSlackCredentials}
          onClose={closeSlackSetup}
          onAdvanced={() => { setSlackSetupOpen(false); setCreateOpen(true); }}
          onComplete={async (connectionID) => {
            await refresh();
            setSelectedID(connectionID);
            closeSlackSetup();
          }}
          onRun={run}
          onError={onError}
        />
      )}

      {parallSetupOpen && (
        <ParallSetup
          agents={agents}
          addresses={addresses}
          connections={connections}
          discovery={parallDiscovery}
          loading={parallDiscoveryLoading}
          working={working}
          requireConversation={parallSetupMode === "add-conversation"}
          onRefreshDiscovery={discoverParall}
          onSaveCredentials={saveParallCredentials}
          onClose={closeParallSetup}
          onAdvanced={() => { setParallSetupOpen(false); setCreateOpen(true); }}
          onComplete={async (connectionID) => {
            await refresh();
            setSelectedID(connectionID);
            closeParallSetup();
          }}
          onRun={run}
          onError={onError}
        />
      )}

      {!larkSetupOpen && !slackSetupOpen && !parallSetupOpen && createOpen && (
        <section className="grid shrink-0 gap-2 border-b border-border bg-card px-4 py-3 sm:grid-cols-[170px_1fr_auto]">
          <select value={provider} onChange={(event) => changeProvider(event.target.value)} className={controlClass}>{providerSpecs.map((spec) => <option key={spec.id} value={spec.id}>{spec.label}</option>)}</select>
          {provider === "lark" || provider === "slack" || provider === "parall" ? <div className="flex min-w-0 items-center text-[11px] text-muted-foreground">Verify credentials, choose an Agent, then add the conversations where it may work.</div> : <div className="grid min-w-0 gap-2 sm:grid-cols-2"><input value={accountRef} onChange={(event) => setAccountRef(event.target.value)} placeholder={createProvider.accountPlaceholder} className={controlClass} /><input value={credentialRef} onChange={(event) => setCredentialRef(event.target.value)} placeholder={createProvider.credentialPlaceholder} className={controlClass} /></div>}
          <div className="flex items-center gap-1"><button onClick={() => provider === "lark" ? openLarkSetup() : provider === "slack" ? openSlackSetup() : provider === "parall" ? openParallSetup() : createConnection()} disabled={working} className="h-8 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50">Continue</button><button onClick={() => setCreateOpen(false)} title="Close" className="flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted"><X className="size-3.5" /></button></div>
        </section>
      )}

      {!larkSetupOpen && !slackSetupOpen && !parallSetupOpen && <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden lg:grid-cols-[320px_1fr]">
        <section className={`${selected ? "hidden lg:block" : "block"} min-h-0 overflow-y-auto border-r border-border`}>
          {activeConnections.map((connection) => {
            const boundAddresses = addresses.filter((address) => address.connectionId === connection.id && !address.archivedAt);
            const identityName = boundAddresses[0]?.displayName;
            return <button key={connection.id} onClick={() => setSelectedID(connection.id)} className={`block w-full border-b border-border px-4 py-3 text-left ${selectedID === connection.id ? "bg-selection text-selection-foreground" : "hover:bg-muted/45"}`}>
              <div className="flex min-w-0 items-center gap-2"><ConnectionDot connection={connection} /><ProviderIcon provider={connection.provider} className="size-3.5 shrink-0" /><span className="min-w-0 flex-1 truncate text-[13px] font-semibold">{identityName || providerSpec(connection.provider).label}</span><span className="font-mono text-[9px] uppercase text-muted-foreground">{connection.status}</span></div>
              <div className="mt-1 truncate text-[10px] text-muted-foreground"><span>{providerSpec(connection.provider).label}</span>{connection.accountRef && <span className="font-mono"> · {connection.accountRef}</span>}</div>
              <div className="mt-1 text-[10px] text-muted-foreground">{boundAddresses.length} addresses</div>
            </button>;
          })}
          {activeConnections.length === 0 && <Empty label="No integrations" />}
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
              <button onClick={() => setSelectedID("")} className="mb-4 text-[12px] text-muted-foreground lg:hidden">← Integrations</button>
              <div className="flex min-w-0 items-start gap-3 border-b border-border pb-4">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted"><ProviderIcon provider={selected.provider} className="size-4 text-primary" /></div>
                <div className="min-w-0"><h2 className="truncate text-lg font-semibold">{selectedAddresses[0]?.displayName || selectedProvider.label}</h2><div className="truncate text-[10px] text-muted-foreground">{selectedProvider.label}<span className="font-mono"> · {selected.accountRef || selected.id}</span></div></div>
                {selected.archivedAt ? <span className="ml-auto rounded-[3px] border border-border px-2 py-1 font-mono text-[9px] uppercase text-muted-foreground">Archived</span> : <button onClick={() => toggleConnection(selected)} disabled={working} title={selected.enabled ? "Disable connection" : "Enable connection"} className={`ml-auto flex h-8 items-center gap-1.5 rounded-md px-2.5 text-[11px] font-medium ${selected.enabled ? "border border-border text-muted-foreground" : "bg-primary text-primary-foreground"}`}>
                  {selected.enabled ? <Unplug className="size-3.5" /> : <Check className="size-3.5" />}{selected.enabled ? "Disable" : "Enable"}
                </button>}
              </div>
              {selected.archivedAt && <div className="mt-4 border-l-2 border-border bg-muted/35 px-3 py-2 text-[11px] leading-4 text-muted-foreground">This historical transport is read-only. Messages still reference its stable IDs; current delivery uses <span className="font-mono text-foreground">{selected.supersededBy || "the replacement connection"}</span>.</div>}
              {!selected.archivedAt && selected.provider === "lark" && (
                <LarkConnectionSummary
                  connection={selected}
                  addresses={selectedAddresses}
                  memberships={selectedMemberships}
                  inboxEntries={selectedInboxEntries}
                  agents={agents}
                  discovery={larkDiscovery?.appId === selected.accountRef ? larkDiscovery : null}
                  onError={onError}
                  onChanged={refresh}
                  onSetup={() => openLarkSetup(
                    selected.accountRef,
                    larkDiscovery && larkDiscovery.appId === selected.accountRef && larkDiscovery.credentialStored && larkDiscovery.botReady && isNativeFeishuConnection(selected) ? "add-group" : "connect",
                  )}
                />
              )}
              {!selected.archivedAt && selected.provider === "slack" && (
                <SlackConnectionSummary
                  connection={selected}
                  addresses={selectedAddresses}
                  memberships={selectedMemberships}
                  inboxEntries={selectedInboxEntries}
                  agents={agents}
                  discovery={slackDiscovery?.teamId === selected.accountRef ? slackDiscovery : null}
                  onError={onError}
                  onChanged={refresh}
                  onSetup={() => openSlackSetup(
                    selected.id,
                    slackDiscovery && slackDiscovery.teamId === selected.accountRef && slackDiscovery.credentialStored && slackDiscovery.botReady ? "add-channel" : "connect",
                  )}
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
                  discovery={parallDiscovery?.orgId === selected.accountRef ? parallDiscovery : null}
                  onError={onError}
                  onChanged={refresh}
                  working={working}
                  onReconnect={() => restartParallGateway(selected.id)}
                  onSetup={() => openParallSetup(
                    selected.id,
                    parallDiscovery && parallDiscovery.orgId === selected.accountRef && parallDiscovery.ownerReady && parallDiscovery.externalReady ? "add-conversation" : "connect",
                  )}
                />
              )}
              {selected.lastError && <div className="mt-4 border-l-2 border-destructive bg-destructive/5 px-3 py-2 text-[12px] text-destructive">{selected.lastError}</div>}
              {selected.provider === "lark" || selected.provider === "slack" || selected.provider === "parall" ? (
                <details className="group mt-4 rounded-[3px] bg-muted/30 px-3">
                  <summary className="flex cursor-pointer list-none items-center gap-2 py-2.5 text-[11px] font-semibold uppercase text-muted-foreground"><ChevronDown className="size-3.5 transition-transform group-open:rotate-180" />Advanced settings</summary>
                  <div className="border-t border-border/60 pb-3">
                    <dl className="grid gap-x-6 gap-y-3 py-4 text-[11px] sm:grid-cols-2 xl:grid-cols-3">
                      <Meta label="Status" value={selected.status} /><Meta label="Account" value={selected.accountRef || "-"} /><Meta label="Credential" value={selected.credentialRef || "-"} /><Meta label="Heartbeat" value={formatDate(selected.lastHeartbeatAt)} /><Meta label="Last event" value={formatDate(selected.lastEventAt)} /><Meta label="Cursor" value={selected.cursor || "-"} />
                    </dl>
                    {!selected.archivedAt && <GatewaySetup connection={selected} addresses={selectedAddresses} />}
                  </div>
                </details>
              ) : (
                <>
                  <dl className="grid gap-x-6 gap-y-3 border-b border-border py-4 text-[11px] sm:grid-cols-2 xl:grid-cols-3">
                    <Meta label="Status" value={selected.status} /><Meta label="Account" value={selected.accountRef || "-"} /><Meta label="Credential" value={selected.credentialRef || "-"} /><Meta label="Heartbeat" value={formatDate(selected.lastHeartbeatAt)} /><Meta label="Last event" value={formatDate(selected.lastEventAt)} /><Meta label="Cursor" value={selected.cursor || "-"} />
                  </dl>
                  {!selected.archivedAt && <GatewaySetup connection={selected} addresses={selectedAddresses} />}
                </>
              )}
              <div className="mt-6 flex items-center justify-between"><h3 className="text-[12px] font-semibold uppercase text-muted-foreground">Agent Addresses</h3>{!selected.archivedAt && <button onClick={bindOpen ? resetAddressForm : startBind} className="flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-[11px] font-medium hover:bg-muted">{bindOpen ? <X className="size-3.5" /> : <Link2 className="size-3.5" />}{bindOpen ? "Close" : "Bind"}</button>}</div>
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
          ) : <Empty label="Select an integration" />}
        </section>
      </div>}
    </main>
  );
}

function LarkSetup({
  agents,
  addresses,
  connections,
  discovery,
  loading,
  working,
  requireGroup,
  onRefreshDiscovery,
  onSaveCredentials,
  onClose,
  onAdvanced,
  onComplete,
  onRun,
  onError,
}: {
  agents: Agent[];
  addresses: AgentAddress[];
  connections: PlatformConnection[];
  discovery: LarkDiscovery | null;
  loading: boolean;
  working: boolean;
  requireGroup: boolean;
  onRefreshDiscovery: (appID?: string) => Promise<LarkDiscovery>;
  onSaveCredentials: (appID: string, appSecret: string) => Promise<LarkDiscovery>;
  onClose: () => void;
  onAdvanced: () => void;
  onComplete: (connectionID: string) => Promise<void>;
  onRun: (task: () => Promise<void>) => void;
  onError: (message: string) => void;
}) {
  const connection = connections.find((item) => item.provider === "lark" && item.accountRef === discovery?.appId);
  const existingAddress = addresses.find((item) => item.connectionId === connection?.id);
  const existingAgent = agents.find((item) => item.id === existingAddress?.agentId)?.name || "";
  const suggestedAgent = agents.find((item) => item.name === "lark-external")?.name || agents.find((item) => item.status === "idle")?.name || agents[0]?.name || "";
  const [agent, setAgent] = useState("");
  const [appID, setAppID] = useState("");
  const [appSecret, setAppSecret] = useState("");
  const [chatID, setChatID] = useState("");
  const [query, setQuery] = useState("");
  const [purpose, setPurpose] = useState("");
  const [role, setRole] = useState("");
  const [guidance, setGuidance] = useState("");

  useEffect(() => {
    if (!agent) setAgent(existingAgent || suggestedAgent);
  }, [existingAgent, suggestedAgent]);
  useEffect(() => {
    if (!appID && discovery?.appId) setAppID(discovery.appId);
  }, [discovery?.appId]);

  const chats = (discovery?.chats || []).filter((chat) => {
    const haystack = `${chat.name} ${chat.description || ""}`.toLowerCase();
    return haystack.includes(query.trim().toLowerCase());
  });
  const selectedChat = discovery?.chats.find((chat) => chat.id === chatID);
  const ready = Boolean(discovery?.available && discovery.credentialStored && discovery.botReady && discovery.appId);
  const nativeConnection = isNativeFeishuConnection(connection);
  const verifyCredentials = async () => {
    if (!appID.trim() || !appSecret.trim()) {
      onError("Enter the Feishu App ID and App Secret");
      return;
    }
    try {
      await onSaveCredentials(appID, appSecret);
      setAppSecret("");
    } catch (error: any) {
      onError(error.message);
    }
  };
  const submit = () => {
    if (!agent) {
      onError("Choose the Agent that will represent this Feishu app");
      return;
    }
    if (requireGroup && !chatID) {
      onError("Choose a Feishu group before continuing");
      return;
    }
    onRun(async () => {
      const data = await api("POST", "/api/integrations/providers/lark/setup", {
        agent,
        appId: discovery?.appId || appID.trim(),
        chatId: chatID,
        purpose: purpose.trim(),
        role: role.trim(),
        guidance: guidance.trim(),
      });
      await onComplete(data.connection.id);
    });
  };

  return (
    <section className="min-h-0 flex-1 overflow-y-auto bg-card">
      <div className="mx-auto grid max-w-5xl gap-5 px-4 py-5 lg:grid-cols-[260px_1fr] lg:px-6 lg:py-8">
        <div className="min-w-0 border-b border-border pb-4 lg:border-b-0 lg:border-r lg:pb-0 lg:pr-5">
          <div className="flex items-center gap-2">
            <div className="flex size-9 items-center justify-center rounded-md bg-muted"><Bot className="size-4" /></div>
            <div className="min-w-0"><h2 className="text-[15px] font-semibold">Connect Feishu</h2><p className="text-[11px] text-muted-foreground">Native connection, managed by CodexLoom.</p></div>
            <button onClick={onClose} title="Close" className="ml-auto flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted"><X className="size-4" /></button>
          </div>
          <div className="mt-4 space-y-2 text-[11px]">
            <SetupCheck complete={nativeConnection} label={nativeConnection ? "Native gateway active" : "Ready to replace legacy gateway"} />
            <SetupCheck complete={Boolean(discovery?.credentialStored)} label="Credential stored securely" />
            <SetupCheck complete={Boolean(discovery?.botReady)} label={discovery?.botName ? `${discovery.botName} verified` : "Bot verified"} />
            <SetupCheck complete={Boolean(discovery?.botReady)} label={`${discovery?.chats.length || 0} visible groups found`} />
          </div>
          {discovery?.appId && <div className="mt-3 truncate font-mono text-[9.5px] text-muted-foreground">{discovery.appId}</div>}
          <p className="mt-4 text-[10.5px] leading-4 text-muted-foreground">The App Secret stays in the operating system Keychain and is never written to Loom data files.</p>
          <button onClick={onAdvanced} className="mt-3 text-[11px] text-muted-foreground underline decoration-border underline-offset-4 hover:text-foreground">Advanced configuration</button>
        </div>

        <div className="min-w-0">
          {loading ? (
            <div className="flex min-h-44 items-center justify-center gap-2 text-[12px] text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />Checking Feishu on this Mac…</div>
          ) : !ready ? (
            <div className="mx-auto max-w-md py-2">
              <h3 className="text-[14px] font-semibold">Connect your Feishu app</h3>
              <p className="mt-1 text-[11.5px] leading-5 text-muted-foreground">Copy these two values from Feishu Developer Console → Credentials & Basic Info. CodexLoom verifies them before saving.</p>
              <label className="mt-4 block text-[10.5px] font-medium text-muted-foreground">App ID</label>
              <input value={appID} disabled={Boolean(connection?.accountRef)} onChange={(event) => setAppID(event.target.value)} placeholder="cli_..." autoComplete="off" className={`${controlClass} mt-1 w-full font-mono`} />
              <label className="mt-3 block text-[10.5px] font-medium text-muted-foreground">App Secret</label>
              <input value={appSecret} onChange={(event) => setAppSecret(event.target.value)} placeholder="Paste App Secret" type="password" autoComplete="new-password" className={`${controlClass} mt-1 w-full font-mono`} />
              {discovery?.error && <div className="mt-3 border-l-2 border-warning bg-warning/5 px-3 py-2 text-[10.5px] leading-4 text-muted-foreground">{discovery.error}</div>}
              <button onClick={verifyCredentials} disabled={!appID.trim() || !appSecret.trim()} className="mt-4 flex h-9 w-full items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-[12px] font-medium text-primary-foreground disabled:opacity-45"><ShieldCheck className="size-3.5" />Verify and continue</button>
              {discovery?.credentialStored && <button onClick={() => onRefreshDiscovery(appID)} className="mx-auto mt-3 flex h-8 items-center gap-1.5 px-3 text-[11px] text-muted-foreground hover:text-foreground"><RefreshCw className="size-3.5" />Check again</button>}
            </div>
          ) : (
            <div className="grid gap-4 xl:grid-cols-2">
              <div className="min-w-0">
                <label className="text-[11px] font-semibold">Which Agent speaks through Feishu?</label>
                <select value={agent} disabled={Boolean(existingAgent)} onChange={(event) => setAgent(event.target.value)} className={`${controlClass} mt-1.5 w-full`}>
                  <option value="">Choose an Agent</option>
                  {agents.map((item) => <option key={item.id} value={item.name}>{item.name}</option>)}
                </select>
                <p className="mt-1.5 text-[10.5px] leading-4 text-muted-foreground">{existingAgent ? `${existingAgent} already owns this Feishu identity.` : "This is the long-lived Agent that receives messages and writes replies."}</p>

                <div className="mt-4 flex items-center justify-between gap-3">
                  <label className="text-[11px] font-semibold">Where may it work?</label>
                  <div className="flex items-center gap-1">
                    <div className="relative"><Search className="pointer-events-none absolute left-2 top-2 size-3 text-muted-foreground" /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Find a group" className="h-7 w-32 rounded-md border border-border bg-background pl-7 pr-2 text-[10.5px] outline-none focus:border-ring" /></div>
                    <button onClick={() => onRefreshDiscovery(discovery?.appId || appID)} title="Refresh Feishu groups" className="flex size-7 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-muted hover:text-foreground"><RefreshCw className="size-3" /></button>
                  </div>
                </div>
                <div className="mt-1.5 max-h-44 overflow-y-auto border-y border-border">
                  {!requireGroup && <button onClick={() => setChatID("")} className={`flex w-full items-center gap-2 border-b border-border px-2 py-2 text-left ${chatID === "" ? "bg-selection text-selection-foreground" : "hover:bg-muted/45"}`}>
                    <MessageSquare className="size-3.5 shrink-0" /><span className="text-[11.5px] font-medium">Direct messages only</span><Check className={`ml-auto size-3.5 ${chatID === "" ? "opacity-100" : "opacity-0"}`} />
                  </button>}
                  {chats.map((chat) => (
                    <button key={chat.id} onClick={() => { setChatID(chat.id); if (!purpose) setPurpose(chat.description || ""); }} className={`flex w-full min-w-0 items-center gap-2 border-b border-border px-2 py-2 text-left last:border-b-0 ${chatID === chat.id ? "bg-selection text-selection-foreground" : "hover:bg-muted/45"}`}>
                      <Users className="size-3.5 shrink-0" /><span className="min-w-0 flex-1"><span className="block truncate text-[11.5px] font-medium">{chat.name}</span>{chat.description && <span className="block truncate text-[9.5px] text-muted-foreground">{chat.description}</span>}</span>{chat.external && <span className="rounded border border-border px-1 py-0.5 font-mono text-[8px] uppercase text-muted-foreground">external</span>}<Check className={`size-3.5 shrink-0 ${chatID === chat.id ? "opacity-100" : "opacity-0"}`} />
                    </button>
                  ))}
                  {chats.length === 0 && <div className="px-3 py-4 text-center"><div className="text-[11px] font-medium">No groups visible</div><div className="mt-1 text-[10px] leading-4 text-muted-foreground">Add this bot to a Feishu group, then refresh the list.</div></div>}
                </div>
              </div>

              <div className="min-w-0">
                {selectedChat ? (
                  <>
                    <div className="flex items-center gap-2"><ShieldCheck className="size-4 text-success" /><div><h3 className="text-[12px] font-semibold">Role in {selectedChat.name}</h3><p className="text-[10px] text-muted-foreground">These instructions are added whenever this group contacts the Agent.</p></div></div>
                    <label className="mt-3 block text-[10.5px] font-medium text-muted-foreground">What is this group for?</label>
                    <textarea value={purpose} onChange={(event) => setPurpose(event.target.value)} placeholder={selectedChat.description || "The purpose of this group"} className={`${textAreaClass} mt-1 h-14`} />
                    <label className="mt-2 block text-[10.5px] font-medium text-muted-foreground">What should the Agent do here?</label>
                    <textarea value={role} onChange={(event) => setRole(event.target.value)} placeholder={`${agent || "The Agent"} answers questions in its domain and coordinates internal work.`} className={`${textAreaClass} mt-1 h-14`} />
                    <label className="mt-2 block text-[10.5px] font-medium text-muted-foreground">Anything it should avoid or remember?</label>
                    <textarea value={guidance} onChange={(event) => setGuidance(event.target.value)} placeholder="Optional boundaries and communication style" className={`${textAreaClass} mt-1 h-14`} />
                  </>
                ) : (
                  <div className="flex h-full min-h-44 flex-col items-center justify-center border-y border-border text-center">{requireGroup ? <Users className="size-5 text-muted-foreground" /> : <MessageSquare className="size-5 text-muted-foreground" />}<h3 className="mt-2 text-[12px] font-semibold">{requireGroup ? "Choose a group" : "Private conversations"}</h3><p className="mt-1 max-w-xs text-[10.5px] leading-4 text-muted-foreground">{requireGroup ? "Select a visible group to define the Agent's role and communication boundaries there." : "The Agent can answer direct messages. Group messages remain blocked until you add a group and define its role."}</p></div>
                )}
                <button onClick={submit} disabled={working || !agent || (requireGroup && !selectedChat)} className="mt-3 flex h-9 w-full items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-[12px] font-medium text-primary-foreground disabled:opacity-45">{working ? <LoaderCircle className="size-3.5 animate-spin" /> : <CircleCheck className="size-3.5" />}{requireGroup && !selectedChat ? "Choose a group" : existingAddress ? selectedChat ? "Add this group" : nativeConnection ? "Keep current setup" : "Migrate to native gateway" : selectedChat ? "Connect Agent and group" : "Connect for direct messages"}</button>
              </div>
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

function SlackSetup({
  agents,
  addresses,
  connections,
  discovery,
  loading,
  working,
  requireChannel,
  onRefreshDiscovery,
  onSaveCredentials,
  onClose,
  onAdvanced,
  onComplete,
  onRun,
  onError,
}: {
  agents: Agent[];
  addresses: AgentAddress[];
  connections: PlatformConnection[];
  discovery: SlackDiscovery | null;
  loading: boolean;
  working: boolean;
  requireChannel: boolean;
  onRefreshDiscovery: (connectionID?: string, appID?: string) => Promise<SlackDiscovery>;
  onSaveCredentials: (botToken: string, appToken: string) => Promise<SlackDiscovery>;
  onClose: () => void;
  onAdvanced: () => void;
  onComplete: (connectionID: string) => Promise<void>;
  onRun: (task: () => Promise<void>) => void;
  onError: (message: string) => void;
}) {
  const connection = connections.find((item) => item.provider === "slack" && item.accountRef === discovery?.teamId);
  const existingAddress = addresses.find((item) => item.connectionId === connection?.id);
  const existingAgent = agents.find((item) => item.id === existingAddress?.agentId)?.name || "";
  const suggestedAgent = agents.find((item) => item.name === "slack-external")?.name || agents.find((item) => item.status === "idle")?.name || agents[0]?.name || "";
  const [agent, setAgent] = useState("");
  const [botToken, setBotToken] = useState("");
  const [appToken, setAppToken] = useState("");
  const [channelID, setChannelID] = useState("");
  const [query, setQuery] = useState("");
  const [purpose, setPurpose] = useState("");
  const [role, setRole] = useState("");
  const [guidance, setGuidance] = useState("");

  useEffect(() => {
    if (!agent) setAgent(existingAgent || suggestedAgent);
  }, [existingAgent, suggestedAgent]);

  const channels = (discovery?.channels || []).filter((channel) => {
    const haystack = `${channel.name} ${channel.description || ""}`.toLowerCase();
    return haystack.includes(query.trim().toLowerCase());
  });
  const selectedChannel = discovery?.channels.find((channel) => channel.id === channelID);
  const joinedChannels = discovery?.channels.filter((channel) => channel.member) || [];
  const ready = Boolean(discovery?.available && discovery.credentialStored && discovery.botReady && discovery.socketReady && discovery.appId && discovery.teamId);
  const verifyCredentials = async () => {
    if (!botToken.trim() || !appToken.trim()) {
      onError("Enter the Slack Bot token and App token");
      return;
    }
    try {
      await onSaveCredentials(botToken, appToken);
      setBotToken("");
      setAppToken("");
    } catch (error: any) {
      onError(error.message);
    }
  };
  const submit = () => {
    if (!agent) {
      onError("Choose the Agent that will represent this Slack app");
      return;
    }
    if (requireChannel && !channelID) {
      onError("Choose a Slack channel before continuing");
      return;
    }
    onRun(async () => {
      const data = await api("POST", "/api/integrations/providers/slack/setup", {
        agent,
        appId: discovery?.appId,
        teamId: discovery?.teamId,
        channelId: channelID,
        purpose: purpose.trim(),
        role: role.trim(),
        guidance: guidance.trim(),
      });
      await onComplete(data.connection.id);
    });
  };

  return (
    <section className="min-h-0 flex-1 overflow-y-auto bg-card">
      <div className="mx-auto grid max-w-5xl gap-5 px-4 py-5 lg:grid-cols-[260px_1fr] lg:px-6 lg:py-8">
        <div className="min-w-0 border-b border-border pb-4 lg:border-b-0 lg:border-r lg:pb-0 lg:pr-5">
          <div className="flex items-center gap-2">
            <div className="flex size-9 items-center justify-center rounded-md bg-muted"><MessageSquare className="size-4" /></div>
            <div className="min-w-0"><h2 className="text-[15px] font-semibold">Connect Slack</h2><p className="text-[11px] text-muted-foreground">Socket Mode, managed by CodexLoom.</p></div>
            <button onClick={onClose} title="Close" className="ml-auto flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted"><X className="size-4" /></button>
          </div>
          <div className="mt-4 space-y-2 text-[11px]">
            <SetupCheck complete={discovery?.runtime === "managed-socket-mode"} label="Managed gateway included" />
            <SetupCheck complete={Boolean(discovery?.credentialStored)} label="Tokens stored securely" />
            <SetupCheck complete={Boolean(discovery?.botReady)} label={discovery?.botName ? `${discovery.botName} verified` : "Bot verified"} />
            <SetupCheck complete={Boolean(discovery?.socketReady)} label="Socket Mode verified" />
            <SetupCheck complete={joinedChannels.length > 0} label={`${joinedChannels.length} joined channels found`} />
          </div>
          {discovery?.teamName && <div className="mt-3 text-[11px] font-medium">{discovery.teamName}</div>}
          {discovery?.appId && <div className="mt-0.5 truncate font-mono text-[9.5px] text-muted-foreground">{discovery.appId} · {discovery.teamId}</div>}
          <p className="mt-4 text-[10.5px] leading-4 text-muted-foreground">Both tokens stay in the operating system Keychain. Loom stores only the App and Workspace identifiers.</p>
          <button onClick={onAdvanced} className="mt-3 text-[11px] text-muted-foreground underline decoration-border underline-offset-4 hover:text-foreground">Advanced configuration</button>
        </div>

        <div className="min-w-0">
          {loading ? (
            <div className="flex min-h-44 items-center justify-center gap-2 text-[12px] text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />Checking Slack on this Mac…</div>
          ) : !ready ? (
            <div className="mx-auto max-w-md py-2">
              <h3 className="text-[14px] font-semibold">Connect your Slack app</h3>
              <p className="mt-1 text-[11.5px] leading-5 text-muted-foreground">Paste the Bot token from OAuth & Permissions and the App token from Basic Information. The App token must include <span className="font-mono text-foreground">connections:write</span>.</p>
              <label className="mt-4 block text-[10.5px] font-medium text-muted-foreground">Bot token</label>
              <input value={botToken} onChange={(event) => setBotToken(event.target.value)} placeholder="xoxb-…" type="password" autoComplete="new-password" className={`${controlClass} mt-1 w-full font-mono`} />
              <label className="mt-3 block text-[10.5px] font-medium text-muted-foreground">App token</label>
              <input value={appToken} onChange={(event) => setAppToken(event.target.value)} placeholder="xapp-…" type="password" autoComplete="new-password" className={`${controlClass} mt-1 w-full font-mono`} />
              {discovery?.error && <div className="mt-3 border-l-2 border-warning bg-warning/5 px-3 py-2 text-[10.5px] leading-4 text-muted-foreground">{discovery.error}</div>}
              <button onClick={verifyCredentials} disabled={!botToken.trim() || !appToken.trim()} className="mt-4 flex h-9 w-full items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-[12px] font-medium text-primary-foreground disabled:opacity-45"><ShieldCheck className="size-3.5" />Verify and continue</button>
            </div>
          ) : (
            <div className="grid gap-4 xl:grid-cols-2">
              <div className="min-w-0">
                <label className="text-[11px] font-semibold">Which Agent speaks through Slack?</label>
                <select value={agent} disabled={Boolean(existingAgent)} onChange={(event) => setAgent(event.target.value)} className={`${controlClass} mt-1.5 w-full`}><option value="">Choose an Agent</option>{agents.map((item) => <option key={item.id} value={item.name}>{item.name}</option>)}</select>
                <p className="mt-1.5 text-[10.5px] leading-4 text-muted-foreground">{existingAgent ? `${existingAgent} already owns this Slack identity.` : "This long-lived Agent receives Slack messages and writes replies."}</p>

                {discovery?.missingScopes?.length ? <div className="mt-4 rounded-[3px] bg-warning/8 px-3 py-3"><div className="text-[11px] font-semibold">Allow Loom to discover channels</div><p className="mt-1 text-[10.5px] leading-4 text-muted-foreground">Add these Bot Token Scopes, reinstall the app, then check again:</p><div className="mt-2 flex flex-wrap gap-1">{discovery.missingScopes.map((scope) => <span key={scope} className="rounded border border-warning/30 px-1.5 py-0.5 font-mono text-[9px] text-warning">{scope}</span>)}</div><div className="mt-2 flex items-center gap-2"><a href={`https://api.slack.com/apps/${encodeURIComponent(discovery.appId || "")}/oauth`} target="_blank" rel="noreferrer" className="text-[10.5px] font-medium text-primary underline underline-offset-2">Open Slack permissions</a><button onClick={() => onRefreshDiscovery(connection?.id || "", discovery.appId || "")} className="flex h-7 items-center gap-1 rounded-md border border-border px-2 text-[10.5px] hover:bg-muted"><RefreshCw className="size-3" />Check again</button></div></div> : <>
                  <div className="mt-4 flex items-center justify-between gap-3"><label className="text-[11px] font-semibold">Where may it work?</label><div className="flex items-center gap-1"><div className="relative"><Search className="pointer-events-none absolute left-2 top-2 size-3 text-muted-foreground" /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Find a channel" className="h-7 w-32 rounded-md border border-border bg-background pl-7 pr-2 text-[10.5px] outline-none focus:border-ring" /></div><button onClick={() => onRefreshDiscovery(connection?.id || "", discovery?.appId || "")} title="Refresh Slack channels" className="flex size-7 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-muted hover:text-foreground"><RefreshCw className="size-3" /></button></div></div>
                  <div className="mt-1.5 max-h-48 overflow-y-auto rounded-[3px] bg-muted/25 p-1">
                    {!requireChannel && <button onClick={() => setChannelID("")} className={`flex w-full items-center gap-2 rounded-sm px-2 py-2 text-left ${channelID === "" ? "bg-selection text-selection-foreground" : "hover:bg-muted/60"}`}><MessageSquare className="size-3.5 shrink-0" /><span className="text-[11.5px] font-medium">Direct messages only</span><Check className={`ml-auto size-3.5 ${channelID === "" ? "opacity-100" : "opacity-0"}`} /></button>}
                    {channels.map((channel) => <button key={channel.id} disabled={!channel.member} onClick={() => { setChannelID(channel.id); if (!purpose) setPurpose(channel.description || ""); }} className={`flex w-full min-w-0 items-center gap-2 rounded-sm px-2 py-2 text-left disabled:opacity-50 ${channelID === channel.id ? "bg-selection text-selection-foreground" : "hover:bg-muted/60"}`}><Users className="size-3.5 shrink-0" /><span className="min-w-0 flex-1"><span className="block truncate text-[11.5px] font-medium">#{channel.name}</span>{channel.description && <span className="block truncate text-[9.5px] text-muted-foreground">{channel.description}</span>}</span>{channel.private && <span className="font-mono text-[8px] uppercase text-muted-foreground">private</span>}{!channel.member && <span className="font-mono text-[8px] uppercase text-warning">invite bot</span>}<Check className={`size-3.5 shrink-0 ${channelID === channel.id ? "opacity-100" : "opacity-0"}`} /></button>)}
                    {channels.length === 0 && <div className="px-3 py-4 text-center"><div className="text-[11px] font-medium">No channels visible</div><div className="mt-1 text-[10px] leading-4 text-muted-foreground">Invite the bot to a channel, then refresh.</div></div>}
                  </div>
                  {joinedChannels.length === 0 && channels.length > 0 && <div className="mt-2 border-l-2 border-warning px-2.5 py-1 text-[10.5px] leading-4 text-muted-foreground">In Slack, open a channel and enter <span className="font-mono text-foreground">/invite @{discovery?.botName || "CodexLoom"}</span>, then refresh this list.</div>}
                </>}
              </div>

              <div className="min-w-0">
                {selectedChannel ? <><div className="flex items-center gap-2"><ShieldCheck className="size-4 text-success" /><div><h3 className="text-[12px] font-semibold">Role in #{selectedChannel.name}</h3><p className="text-[10px] text-muted-foreground">These instructions accompany messages from this channel.</p></div></div><label className="mt-3 block text-[10.5px] font-medium text-muted-foreground">What is this channel for?</label><textarea value={purpose} onChange={(event) => setPurpose(event.target.value)} placeholder={selectedChannel.description || "The purpose of this channel"} className={`${textAreaClass} mt-1 h-14`} /><label className="mt-2 block text-[10.5px] font-medium text-muted-foreground">What should the Agent do here?</label><textarea value={role} onChange={(event) => setRole(event.target.value)} placeholder={`${agent || "The Agent"} answers questions in its domain.`} className={`${textAreaClass} mt-1 h-14`} /><label className="mt-2 block text-[10.5px] font-medium text-muted-foreground">Anything it should avoid or remember?</label><textarea value={guidance} onChange={(event) => setGuidance(event.target.value)} placeholder="Optional boundaries and communication style" className={`${textAreaClass} mt-1 h-14`} /></> : <div className="flex h-full min-h-44 flex-col items-center justify-center rounded-[3px] bg-muted/20 text-center">{requireChannel ? <Users className="size-5 text-muted-foreground" /> : <MessageSquare className="size-5 text-muted-foreground" />}<h3 className="mt-2 text-[12px] font-semibold">{requireChannel ? "Choose a channel" : "Private conversations"}</h3><p className="mt-1 max-w-xs text-[10.5px] leading-4 text-muted-foreground">{requireChannel ? "Select a joined channel to define the Agent's role and boundaries there." : "The Agent can receive direct messages. Channel messages remain blocked until a channel role is added."}</p></div>}
                <button onClick={submit} disabled={working || !agent || (requireChannel && !selectedChannel)} className="mt-3 flex h-9 w-full items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-[12px] font-medium text-primary-foreground disabled:opacity-45">{working ? <LoaderCircle className="size-3.5 animate-spin" /> : <CircleCheck className="size-3.5" />}{requireChannel && !selectedChannel ? "Choose a channel" : existingAddress ? selectedChannel ? "Add this channel" : "Keep current setup" : selectedChannel ? "Connect Agent and channel" : "Connect for direct messages"}</button>
              </div>
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

function ParallSetup({
  agents,
  addresses,
  connections,
  discovery,
  loading,
  working,
  requireConversation,
  onRefreshDiscovery,
  onSaveCredentials,
  onClose,
  onAdvanced,
  onComplete,
  onRun,
  onError,
}: {
  agents: Agent[];
  addresses: AgentAddress[];
  connections: PlatformConnection[];
  discovery: ParallDiscovery | null;
  loading: boolean;
  working: boolean;
  requireConversation: boolean;
  onRefreshDiscovery: (connectionID?: string, orgID?: string, agentID?: string) => Promise<ParallDiscovery>;
  onSaveCredentials: (apiURL: string, orgID: string, ownerAPIKey: string) => Promise<ParallDiscovery>;
  onClose: () => void;
  onAdvanced: () => void;
  onComplete: (connectionID: string) => Promise<void>;
  onRun: (task: () => Promise<void>) => void;
  onError: (message: string) => void;
}) {
  const connection = connections.find((item) => item.provider === "parall" && item.accountRef === discovery?.orgId);
  const existingAddress = addresses.find((item) => item.connectionId === connection?.id);
  const existingAgent = agents.find((item) => item.id === existingAddress?.agentId)?.name || "";
  const existingExternalID = existingAddress ? stripIdentity(existingAddress.externalIdentity) : discovery?.selectedAgentId || "";
  const suggestedAgent = agents.find((item) => item.name === "parall-lead")?.name || agents.find((item) => item.status === "idle")?.name || agents[0]?.name || "";
  const [apiURL, setAPIURL] = useState("https://api.parall.com");
  const [orgID, setOrgID] = useState("");
  const [ownerAPIKey, setOwnerAPIKey] = useState("");
  const [agent, setAgent] = useState("");
  const [identityMode, setIdentityMode] = useState<"create" | "existing">("create");
  const [externalAgentID, setExternalAgentID] = useState("");
  const [externalDisplayName, setExternalDisplayName] = useState("");
  const [conversationID, setConversationID] = useState("");
  const [query, setQuery] = useState("");
  const [purpose, setPurpose] = useState("");
  const [role, setRole] = useState("");
  const [guidance, setGuidance] = useState("");

  useEffect(() => {
    if (discovery?.apiUrl) setAPIURL(discovery.apiUrl);
    if (discovery?.orgId) setOrgID(discovery.orgId);
    if (!agent) setAgent(existingAgent || suggestedAgent);
    if (existingExternalID) {
      setIdentityMode("existing");
      setExternalAgentID(existingExternalID);
      const external = discovery?.agents.find((item) => item.id === existingExternalID);
      if (external?.name) setExternalDisplayName(external.name);
    }
  }, [discovery?.apiUrl, discovery?.orgId, discovery?.selectedAgentId, existingAgent, existingExternalID, suggestedAgent]);

  const conversations = (discovery?.chats || []).filter((conversation) => conversation.type !== "direct" && `${conversation.name} ${conversation.description || ""}`.toLowerCase().includes(query.trim().toLowerCase()));
  const selectedConversation = discovery?.chats.find((item) => item.id === conversationID);
  const selectedExternal = discovery?.agents.find((item) => item.id === externalAgentID);
  const ownerReady = Boolean(discovery?.available && discovery.ownerCredentialStored && discovery.ownerReady && discovery.orgId);
  const verifyOwner = async () => {
    if (!orgID.trim() || !ownerAPIKey.trim()) {
      onError("Enter the Parall organization ID and Owner API key");
      return;
    }
    try {
      await onSaveCredentials(apiURL, orgID, ownerAPIKey);
      setOwnerAPIKey("");
    } catch (error: any) {
      onError(error.message);
    }
  };
  const selectExternal = async (id: string) => {
    setExternalAgentID(id);
    const item = discovery?.agents.find((candidate) => candidate.id === id);
    setExternalDisplayName(item?.name || "");
    if (id) await onRefreshDiscovery(connection?.id || "", discovery?.orgId || orgID, id);
  };
  const submit = () => {
    if (!agent) {
      onError("Choose the Loom Agent that will use this Parall identity");
      return;
    }
    if (identityMode === "existing" && !externalAgentID) {
      onError("Choose a Parall external Agent");
      return;
    }
    if (!externalDisplayName.trim()) {
      onError("Enter the external Agent display name");
      return;
    }
    if (requireConversation && !conversationID) {
      onError("Choose a Parall conversation before continuing");
      return;
    }
    onRun(async () => {
      const data = await api("POST", "/api/integrations/providers/parall/setup", {
        agent,
        orgId: discovery?.orgId || orgID,
        externalAgentId: identityMode === "existing" ? externalAgentID : "",
        externalDisplayName: externalDisplayName.trim(),
        chatId: conversationID,
        purpose: purpose.trim(),
        role: role.trim(),
        guidance: guidance.trim(),
      });
      await onComplete(data.connection.id);
    });
  };

  return <section className="min-h-0 flex-1 overflow-y-auto bg-card">
    <div className="mx-auto grid max-w-5xl gap-5 px-4 py-5 lg:grid-cols-[260px_1fr] lg:px-6 lg:py-8">
      <div className="min-w-0 border-b border-border pb-4 lg:border-b-0 lg:border-r lg:pb-0 lg:pr-5">
        <div className="flex items-center gap-2"><div className="flex size-9 items-center justify-center rounded-md bg-muted"><Cable className="size-4" /></div><div className="min-w-0"><h2 className="text-[15px] font-semibold">Connect Parall</h2><p className="text-[11px] text-muted-foreground">External identity and WebSocket, managed by Loom.</p></div><button onClick={onClose} title="Close" className="ml-auto flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted"><X className="size-4" /></button></div>
        <div className="mt-4 space-y-2 text-[11px]">
          <SetupCheck complete={discovery?.runtime === "managed-websocket"} label="Managed gateway included" />
          <SetupCheck complete={Boolean(discovery?.ownerReady)} label={discovery?.ownerName ? `${discovery.ownerName} verified as Owner` : "Organization Owner verified"} />
          <SetupCheck complete={Boolean(discovery?.externalReady)} label={selectedExternal?.name ? `${selectedExternal.name} identity active` : "External Agent identity ready"} />
          <SetupCheck complete={Boolean(discovery?.agentCredentialStored)} label="Agent key stored securely" />
          <SetupCheck complete={Boolean(discovery?.socketReady)} label="WebSocket access verified" />
        </div>
        {discovery?.orgName && <div className="mt-3 text-[11px] font-medium">{discovery.orgName}</div>}
        {discovery?.orgId && <div className="mt-0.5 truncate font-mono text-[9.5px] text-muted-foreground">{discovery.orgId}{existingExternalID ? ` · ${existingExternalID}` : ""}</div>}
        <p className="mt-4 text-[10.5px] leading-4 text-muted-foreground">Loom uses the Owner key only for identity and membership administration. Each external Agent runs with its own key. Both stay in the operating system Keychain.</p>
        <button onClick={onAdvanced} className="mt-3 text-[11px] text-muted-foreground underline decoration-border underline-offset-4 hover:text-foreground">Advanced configuration</button>
      </div>

      <div className="min-w-0">
        {loading ? <div className="flex min-h-44 items-center justify-center gap-2 text-[12px] text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />Checking Parall…</div> : !ownerReady ? <div className="mx-auto max-w-md py-2">
          <h3 className="text-[14px] font-semibold">Connect your Parall organization</h3>
          <p className="mt-1 text-[11.5px] leading-5 text-muted-foreground">Use an Owner API key so Loom can create external Agent identities, preserve their one-time keys, rename them, and add them to conversations.</p>
          <label className="mt-4 block text-[10.5px] font-medium text-muted-foreground">Parall API URL</label><input value={apiURL} onChange={(event) => setAPIURL(event.target.value)} placeholder="https://api.parall.com" className={`${controlClass} mt-1 w-full font-mono`} />
          <label className="mt-3 block text-[10.5px] font-medium text-muted-foreground">Organization ID</label><input value={orgID} onChange={(event) => setOrgID(event.target.value)} placeholder="org_…" className={`${controlClass} mt-1 w-full font-mono`} />
          <label className="mt-3 block text-[10.5px] font-medium text-muted-foreground">Owner API key</label><input value={ownerAPIKey} onChange={(event) => setOwnerAPIKey(event.target.value)} placeholder="Parall Owner key" type="password" autoComplete="new-password" className={`${controlClass} mt-1 w-full font-mono`} />
          {discovery?.error && <div className="mt-3 border-l-2 border-warning bg-warning/5 px-3 py-2 text-[10.5px] leading-4 text-muted-foreground">{discovery.error}</div>}
          <button onClick={verifyOwner} disabled={!orgID.trim() || !ownerAPIKey.trim()} className="mt-4 flex h-9 w-full items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-[12px] font-medium text-primary-foreground disabled:opacity-45"><ShieldCheck className="size-3.5" />Verify Owner and continue</button>
        </div> : <div className="grid gap-4 xl:grid-cols-2">
          <div className="min-w-0">
            <label className="text-[11px] font-semibold">Which Loom Agent speaks through Parall?</label>
            <select value={agent} disabled={Boolean(existingAgent)} onChange={(event) => setAgent(event.target.value)} className={`${controlClass} mt-1.5 w-full`}><option value="">Choose an Agent</option>{agents.map((item) => <option key={item.id} value={item.name}>{item.name}</option>)}</select>
            <p className="mt-1.5 text-[10.5px] leading-4 text-muted-foreground">{existingAgent ? `${existingAgent} already owns this Parall identity.` : "This long-lived Loom Agent receives dispatches and writes replies."}</p>

            <div className="mt-4 flex items-center justify-between"><label className="text-[11px] font-semibold">External identity</label>{!existingAddress && <div className="flex rounded-[3px] bg-muted p-0.5"><button onClick={() => { setIdentityMode("create"); setExternalAgentID(""); setExternalDisplayName(agent || ""); }} className={`h-6 rounded-sm px-2 text-[10px] ${identityMode === "create" ? "bg-background font-medium shadow-sm" : "text-muted-foreground"}`}>Create new</button><button onClick={() => setIdentityMode("existing")} className={`h-6 rounded-sm px-2 text-[10px] ${identityMode === "existing" ? "bg-background font-medium shadow-sm" : "text-muted-foreground"}`}>Use existing</button></div>}</div>
            {identityMode === "existing" ? <select value={externalAgentID} disabled={Boolean(existingAddress)} onChange={(event) => void selectExternal(event.target.value)} className={`${controlClass} mt-1.5 w-full`}><option value="">Choose a Parall Agent</option>{discovery?.agents.map((item) => <option key={item.id} value={item.id}>{item.name} · {item.status}{item.online ? " · online" : ""}</option>)}</select> : null}
            <label className="mt-2 block text-[10.5px] font-medium text-muted-foreground">External display name</label><input value={externalDisplayName} onChange={(event) => setExternalDisplayName(event.target.value)} placeholder={agent || "Parall Agent name"} className={`${controlClass} mt-1 w-full`} />
            {identityMode === "existing" && selectedExternal && <div className="mt-2 flex flex-wrap gap-1.5 text-[9.5px]"><span className={`rounded border px-1.5 py-0.5 ${selectedExternal.status === "active" ? "border-success/30 text-success" : "border-warning/30 text-warning"}`}>{selectedExternal.status}</span><span className="rounded border border-border px-1.5 py-0.5 text-muted-foreground">{selectedExternal.credentialStored ? "key stored" : "Loom will create a key"}</span><span className="rounded border border-border px-1.5 py-0.5 font-mono text-muted-foreground">{selectedExternal.id}</span></div>}

            <div className="mt-4 flex items-center justify-between gap-3"><label className="text-[11px] font-semibold">Where may it work?</label><div className="flex items-center gap-1"><div className="relative"><Search className="pointer-events-none absolute left-2 top-2 size-3 text-muted-foreground" /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Find conversation" className="h-7 w-36 rounded-md border border-border bg-background pl-7 pr-2 text-[10.5px] outline-none focus:border-ring" /></div><button onClick={() => onRefreshDiscovery(connection?.id || "", discovery?.orgId || "", externalAgentID)} title="Refresh Parall conversations" className="flex size-7 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-muted hover:text-foreground"><RefreshCw className="size-3" /></button></div></div>
            <div className="mt-1.5 max-h-48 overflow-y-auto rounded-[3px] bg-muted/25 p-1">
              {!requireConversation && <button onClick={() => setConversationID("")} className={`flex w-full items-center gap-2 rounded-sm px-2 py-2 text-left ${conversationID === "" ? "bg-selection text-selection-foreground" : "hover:bg-muted/60"}`}><MessageSquare className="size-3.5 shrink-0" /><span className="text-[11.5px] font-medium">Identity only</span><Check className={`ml-auto size-3.5 ${conversationID === "" ? "opacity-100" : "opacity-0"}`} /></button>}
              {conversations.map((conversation) => <button key={conversation.id} onClick={() => { setConversationID(conversation.id); if (!purpose) setPurpose(conversation.description || ""); }} className={`flex w-full min-w-0 items-center gap-2 rounded-sm px-2 py-2 text-left ${conversationID === conversation.id ? "bg-selection text-selection-foreground" : "hover:bg-muted/60"}`}><Users className="size-3.5 shrink-0" /><span className="min-w-0 flex-1"><span className="block truncate text-[11.5px] font-medium">{conversation.name}</span>{conversation.description && <span className="block truncate text-[9.5px] text-muted-foreground">{conversation.description}</span>}</span><span className="font-mono text-[8px] uppercase text-muted-foreground">{conversation.type}</span>{conversation.member && <span className="font-mono text-[8px] uppercase text-success">joined</span>}<Check className={`size-3.5 shrink-0 ${conversationID === conversation.id ? "opacity-100" : "opacity-0"}`} /></button>)}
              {conversations.length === 0 && <div className="px-3 py-4 text-center text-[10.5px] text-muted-foreground">No active conversations found.</div>}
            </div>
            <p className="mt-2 text-[10px] leading-4 text-muted-foreground">If the external Agent is not a member, Loom adds it when you finish setup. Direct messages are approved and configured per person after first contact.</p>
          </div>

          <div className="min-w-0">
            {selectedConversation ? <><div className="flex items-center gap-2"><ShieldCheck className="size-4 text-success" /><div><h3 className="text-[12px] font-semibold">Role in {selectedConversation.name}</h3><p className="text-[10px] text-muted-foreground">These instructions accompany dispatches from this conversation.</p></div></div><label className="mt-3 block text-[10.5px] font-medium text-muted-foreground">What is this conversation for?</label><textarea value={purpose} onChange={(event) => setPurpose(event.target.value)} placeholder={selectedConversation.description || "The purpose of this conversation"} className={`${textAreaClass} mt-1 h-14`} /><label className="mt-2 block text-[10.5px] font-medium text-muted-foreground">What should the Agent do here?</label><textarea value={role} onChange={(event) => setRole(event.target.value)} placeholder={`${agent || "The Agent"} answers questions in its domain.`} className={`${textAreaClass} mt-1 h-14`} /><label className="mt-2 block text-[10.5px] font-medium text-muted-foreground">Anything it should avoid or remember?</label><textarea value={guidance} onChange={(event) => setGuidance(event.target.value)} placeholder="Optional boundaries and communication style" className={`${textAreaClass} mt-1 h-14`} /></> : <div className="flex h-full min-h-44 flex-col items-center justify-center rounded-[3px] bg-muted/20 text-center">{requireConversation ? <Users className="size-5 text-muted-foreground" /> : <Cable className="size-5 text-muted-foreground" />}<h3 className="mt-2 text-[12px] font-semibold">{requireConversation ? "Choose a conversation" : "External identity"}</h3><p className="mt-1 max-w-xs text-[10.5px] leading-4 text-muted-foreground">{requireConversation ? "Select a conversation to define the Agent's role and boundaries there." : "Create or adopt the external identity now. Conversations can be added whenever the role is clear."}</p></div>}
            <button onClick={submit} disabled={working || !agent || !externalDisplayName.trim() || (identityMode === "existing" && !externalAgentID) || (requireConversation && !selectedConversation)} className="mt-3 flex h-9 w-full items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-[12px] font-medium text-primary-foreground disabled:opacity-45">{working ? <LoaderCircle className="size-3.5 animate-spin" /> : <CircleCheck className="size-3.5" />}{requireConversation && !selectedConversation ? "Choose a conversation" : existingAddress ? selectedConversation ? "Add this conversation" : "Keep current setup" : selectedConversation ? "Connect Agent and conversation" : "Create external identity"}</button>
          </div>
        </div>}
      </div>
    </div>
  </section>;
}

function SetupCheck({ complete, label }: { complete: boolean; label: string }) {
  return <div className="flex items-center gap-2"><span className={`flex size-4 items-center justify-center rounded-full border ${complete ? "border-success/40 bg-success/10 text-success" : "border-border text-muted-foreground"}`}>{complete && <Check className="size-2.5" />}</span><span className={complete ? "text-foreground" : "text-muted-foreground"}>{label}</span></div>;
}

function LarkConnectionSummary({ connection, addresses, memberships, inboxEntries, agents, discovery, onSetup, onChanged, onError }: { connection: PlatformConnection; addresses: AgentAddress[]; memberships: ConversationMembership[]; inboxEntries: InboxEntry[]; agents: Agent[]; discovery: LarkDiscovery | null; onSetup: () => void; onChanged: () => Promise<void>; onError: (message: string) => void }) {
  const address = addresses[0];
  const agentName = agents.find((agent) => agent.id === address?.agentId)?.name || "an Agent";
  const online = connection.enabled && connection.status === "connected";
  const credentialReady = Boolean(discovery?.credentialStored && discovery.botReady);
  const nativeConfigured = isNativeFeishuConnection(connection);
  const nativeReady = credentialReady && nativeConfigured;
  const needsMigration = credentialReady && !nativeConfigured;
  const groupMemberships = memberships.filter((membership) => membership.conversationType !== "dm");
  const dmMemberships = memberships.filter((membership) => membership.conversationType === "dm");
  return (
    <section className="py-5">
	      <div className="grid grid-cols-[36px_minmax(0,1fr)] items-start gap-3 sm:flex sm:flex-wrap">
        <div className={`flex size-9 items-center justify-center rounded-full ${nativeReady && online ? "bg-success/10 text-success" : "bg-warning/10 text-warning"}`}>{nativeReady && online ? <Check className="size-4" /> : <Bot className="size-4" />}</div>
        <div className="min-w-0 flex-1">
          <h3 className="text-[14px] font-semibold">{needsMigration ? "Move to native Feishu gateway" : !credentialReady ? "Complete native Feishu setup" : online ? "Feishu is ready" : "Feishu needs attention"}</h3>
          <p className="mt-0.5 text-[11.5px] leading-5 text-muted-foreground">{needsMigration ? "The App Secret is already stored. Migrate this connection without changing its Agent, groups, or direct-message policy." : !credentialReady ? "Enter the App Secret once to move this connection to CodexLoom's built-in gateway." : <>Messages are routed to <span className="font-medium text-foreground">{agentName}</span>. Direct-message access follows the policy below; only listed groups may contact it.</>}</p>
        </div>
	        <button onClick={onSetup} className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground">{nativeReady ? <Plus className="size-3.5" /> : <ShieldCheck className="size-3.5" />}{needsMigration ? "Migrate now" : nativeReady ? "Add group" : "Enter App Secret"}</button>
      </div>
      <div className="mt-4 space-y-2">
        <DirectMessages address={address} memberships={dmMemberships} inboxEntries={inboxEntries} agentName={agentName} onChanged={onChanged} onError={onError} />
        {groupMemberships.map((membership) => (
          <ConversationMembershipRow key={membership.id} membership={membership} conversation={discovery?.chats.find((item) => item.id === membership.conversationId)} provider="lark" connection={connection} address={address} agentName={agentName} onChanged={onChanged} onError={onError} />
        ))}
        {groupMemberships.length === 0 && <div className="py-3 pl-7 text-[10.5px] text-muted-foreground">No groups added. Group messages are blocked.</div>}
      </div>
    </section>
  );
}

function isNativeFeishuConnection(connection?: PlatformConnection) {
  return Boolean(connection?.credentialRef?.startsWith("keychain:com.codexloom.feishu."));
}

function SlackConnectionSummary({ connection, addresses, memberships, inboxEntries, agents, discovery, onSetup, onChanged, onError }: { connection: PlatformConnection; addresses: AgentAddress[]; memberships: ConversationMembership[]; inboxEntries: InboxEntry[]; agents: Agent[]; discovery: SlackDiscovery | null; onSetup: () => void; onChanged: () => Promise<void>; onError: (message: string) => void }) {
  const address = addresses[0];
  const agentName = agents.find((agent) => agent.id === address?.agentId)?.name || "an Agent";
  const online = connection.enabled && connection.status === "connected";
  const managedReady = Boolean(discovery?.credentialStored && discovery.botReady && discovery.socketReady);
  const channelMemberships = memberships.filter((membership) => membership.conversationType !== "dm");
  const dmMemberships = memberships.filter((membership) => membership.conversationType === "dm");
  return <section className="py-5">
	    <div className="grid grid-cols-[36px_minmax(0,1fr)] items-start gap-3 sm:flex sm:flex-wrap">
      <div className={`flex size-9 items-center justify-center rounded-full ${managedReady && online ? "bg-success/10 text-success" : "bg-warning/10 text-warning"}`}>{managedReady && online ? <Check className="size-4" /> : <MessageSquare className="size-4" />}</div>
      <div className="min-w-0 flex-1"><h3 className="text-[14px] font-semibold">{!managedReady ? "Complete managed Slack setup" : online ? "Slack is ready" : "Slack needs attention"}</h3><p className="mt-0.5 text-[11.5px] leading-5 text-muted-foreground">{!managedReady ? "Verify the Slack tokens once so Loom can discover conversations and manage the Socket Mode gateway." : <>Messages are routed to <span className="font-medium text-foreground">{agentName}</span>. Direct-message access follows the policy below; only listed channels may contact it.</>}</p></div>
	      <button onClick={onSetup} className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground">{managedReady ? <Plus className="size-3.5" /> : <ShieldCheck className="size-3.5" />}{managedReady ? "Add channel" : "Verify tokens"}</button>
    </div>
    {discovery?.missingScopes?.length ? <div className="ml-12 mt-3 rounded-[3px] bg-warning/8 px-3 py-2 text-[10.5px] text-muted-foreground">Channel discovery needs {discovery.missingScopes.join(", ")}. Existing message delivery remains online.</div> : null}
    <div className="mt-4 space-y-2">
      <DirectMessages address={address} memberships={dmMemberships} inboxEntries={inboxEntries} agentName={agentName} onChanged={onChanged} onError={onError} />
      {channelMemberships.map((membership) => <ConversationMembershipRow key={membership.id} membership={membership} conversation={discovery?.channels.find((item) => item.id === membership.conversationId)} provider="slack" connection={connection} address={address} agentName={agentName} onChanged={onChanged} onError={onError} />)}
      {channelMemberships.length === 0 && <div className="py-3 pl-7 text-[10.5px] text-muted-foreground">No channels added. Channel messages are blocked.</div>}
    </div>
  </section>;
}

function ParallConnectionSummary({ connection, addresses, memberships, candidates, inboxEntries, agents, discovery, working, onReconnect, onSetup, onChanged, onError }: { connection: PlatformConnection; addresses: AgentAddress[]; memberships: ConversationMembership[]; candidates: ConversationCandidate[]; inboxEntries: InboxEntry[]; agents: Agent[]; discovery: ParallDiscovery | null; working: boolean; onReconnect: () => void; onSetup: () => void; onChanged: () => Promise<void>; onError: (message: string) => void }) {
  const address = addresses[0];
  const agentName = agents.find((agent) => agent.id === address?.agentId)?.name || "an Agent";
  const online = connection.enabled && connection.status === "connected";
  const identityReady = Boolean(discovery?.agentCredentialStored && discovery.externalReady && discovery.socketReady);
  const conversationMemberships = memberships.filter((membership) => membership.conversationType !== "dm");
  const dmMemberships = memberships.filter((membership) => membership.conversationType === "dm");
  const configuredConversationIDs = new Set(conversationMemberships.map((membership) => membership.conversationId));
  const unconfiguredCandidates = candidates.filter((candidate) => candidate.available && candidate.conversationType !== "dm" && !configuredConversationIDs.has(candidate.conversationId));
  const external = discovery?.agents.find((item) => item.id === discovery.selectedAgentId);
  const canRestart = !online && Boolean(address) && connection.credentialRef?.startsWith("keychain:");
  return <section className="py-5">
	    <div className="grid grid-cols-[36px_minmax(0,1fr)] items-start gap-3 sm:flex sm:flex-wrap">
      <div className={`flex size-9 items-center justify-center rounded-full ${online ? "bg-success/10 text-success" : "bg-warning/10 text-warning"}`}>{online ? <Check className="size-4" /> : <Cable className="size-4" />}</div>
      <div className="min-w-0 flex-1"><h3 className="text-[14px] font-semibold">{online ? "Parall is connected" : identityReady ? "Parall needs attention" : "Connect the Parall identity"}</h3><p className="mt-0.5 text-[11.5px] leading-5 text-muted-foreground">{online ? <>Dispatches for <span className="font-medium text-foreground">{external?.name || address?.displayName || "the external identity"}</span> are routed to <span className="font-medium text-foreground">{agentName}</span>. Joined conversations appear below and remain blocked until their role is configured.</> : "Connect an external Agent identity so Loom can receive dispatches and discover the conversations it has joined."}</p></div>
	      {(discovery?.ownerReady || !online) && <button onClick={canRestart ? onReconnect : onSetup} disabled={working} className="col-start-2 flex h-8 items-center gap-1.5 justify-self-start rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground disabled:opacity-45">{canRestart ? <RefreshCw className={`size-3.5 ${working ? "animate-spin" : ""}`} /> : discovery?.ownerReady ? <Plus className="size-3.5" /> : <ShieldCheck className="size-3.5" />}{canRestart ? "Restart gateway" : discovery?.ownerReady ? "Add conversation" : "Connect identity"}</button>}
    </div>
    {discovery?.error && <div className="ml-12 mt-3 rounded-[3px] bg-warning/8 px-3 py-2 text-[10.5px] text-muted-foreground">{discovery.error}</div>}
    <div className="mt-4 space-y-2">
      <DirectMessages address={address} memberships={dmMemberships} inboxEntries={inboxEntries} agentName={agentName} onChanged={onChanged} onError={onError} />
      {unconfiguredCandidates.length > 0 && <div className="flex items-center gap-2 px-1 pt-2 text-[10px] font-semibold uppercase text-warning"><span className="size-1.5 rounded-full bg-warning" />{unconfiguredCandidates.length} joined {unconfiguredCandidates.length === 1 ? "conversation needs" : "conversations need"} a role</div>}
      {unconfiguredCandidates.map((candidate) => <ConversationCandidateRow key={candidate.id} candidate={candidate} address={address} agentName={agentName} onChanged={onChanged} onError={onError} />)}
      {conversationMemberships.map((membership) => <ConversationMembershipRow key={membership.id} membership={membership} conversation={discovery?.chats.find((item) => item.id === membership.conversationId)} provider="parall" connection={connection} address={address} agentName={agentName} onChanged={onChanged} onError={onError} />)}
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
  credentialPlaceholder: string;
  defaultCredentialRef: string;
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
    credentialPlaceholder: "keychain:com.codexloom.feishu.<app-id>",
    defaultCredentialRef: "",
    identityPlaceholder: "Bot Open ID (ou_...)",
    capabilities: ["receive_events", "threads", "mentions", "attachments", "reactions", "proactive_send"],
    requirements: ["native Go gateway", "App Secret in system Keychain", "message and reaction scopes", "im:chat.members:read for member names"],
  },
  {
    id: "slack",
    label: "Slack",
    accountPlaceholder: "Workspace ID (T...)",
    credentialPlaceholder: "keychain:com.codexloom.slack.<app-id>",
    defaultCredentialRef: "",
    identityPlaceholder: "Bot User ID (U...)",
    capabilities: ["receive_events", "threads", "mentions", "attachments", "reactions", "proactive_send"],
    requirements: ["managed Socket Mode gateway", "tokens in system Keychain", "Bot invited to configured channels"],
    manifest: "gateway/slack-app-manifest.yaml",
  },
  {
    id: "parall",
    label: "Parall",
    accountPlaceholder: "Organization ID (org_...)",
    credentialPlaceholder: "keychain:com.codexloom.parall.agent.<org>.<agent>",
    defaultCredentialRef: "",
    identityPlaceholder: "Parall identity (prll://...)",
    capabilities: ["receive_events", "explicit_dispatch", "threads", "attachments", "reading", "ack", "proactive_send"],
    requirements: ["managed WebSocket gateway", "Owner and Agent keys in system Keychain", "external Agent membership in configured conversations"],
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

function GatewaySetup({ connection, addresses }: { connection: PlatformConnection; addresses: AgentAddress[] }) {
  const spec = providerSpec(connection.provider);
  const address = addresses[0];
  const command = address ? gatewayCommand(connection, address) : "";
  const online = connection.enabled && connection.status === "connected";
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
        <div className="mt-3 flex min-w-0 items-stretch border border-border bg-muted/25">
          <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap px-3 py-2 font-mono text-[10.5px] text-foreground/80">{command}</code>
          <CopyCommand value={command} />
        </div>
      ) : (
        <div className="mt-3 border-l-2 border-warning bg-warning/5 px-3 py-2 text-[11px] text-muted-foreground">Bind an Agent Address to generate the gateway command.</div>
      )}
      {addresses.length > 1 && <div className="mt-2 font-mono text-[9.5px] text-warning">This command uses the first of {addresses.length} addresses.</div>}
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

function gatewayCommand(connection: PlatformConnection, address: AgentAddress) {
  const base = `node gateway/${connection.provider}.mjs --connection ${shellArg(connection.id)} --address ${shellArg(address.id)}`;
  if (connection.provider === "lark") {
    return `bin/loom-feishu-gateway --connection ${shellArg(connection.id)} --address ${shellArg(address.id)} --app-id ${shellArg(connection.accountRef || stripIdentity(address.externalIdentity))}`;
  }
  if (connection.provider === "slack") {
    const app = slackAppIDFromCredentialRef(connection.credentialRef);
    const bot = stripIdentity(address.externalIdentity);
    const team = connection.accountRef || "";
    return `bin/loom-slack-gateway --connection ${shellArg(connection.id)} --address ${shellArg(address.id)}${app ? ` --app-id ${shellArg(app)}` : ""}${bot ? ` --bot-user-id ${shellArg(bot)}` : ""}${team ? ` --team-id ${shellArg(team)}` : ""}`;
  }
  if (connection.provider === "parall") {
    return `bin/loom-parall-gateway --connection ${shellArg(connection.id)} --address ${shellArg(address.id)} --org-id ${shellArg(connection.accountRef || "")} --agent-id ${shellArg(stripIdentity(address.externalIdentity))}`;
  }
  return base;
}

function slackAppIDFromCredentialRef(value = "") {
  const prefix = "keychain:com.codexloom.slack.";
  if (!value.startsWith(prefix)) return "";
  return value.slice(prefix.length).replace(/\.(bot-token|app-token)$/, "").trim();
}

function stripIdentity(value: string) {
  const index = value.lastIndexOf("/");
  return index >= 0 ? value.slice(index + 1) : value;
}

function shellArg(value: string) {
  return `'${value.replaceAll("'", `'\\''`)}'`;
}

function formatDate(value?: string) {
  if (!value) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function normalizeLarkDiscovery(discovery: LarkDiscovery): LarkDiscovery {
  const chats = new Map<string, LarkDiscovery["chats"][number]>();
  for (const chat of discovery.chats || []) {
    const id = chat.id.trim();
    if (!id) continue;
    const current = chats.get(id);
    if (!current) {
      chats.set(id, { ...chat, id });
      continue;
    }
    chats.set(id, {
      ...current,
      name: current.name && current.name !== id ? current.name : chat.name || current.name,
      description: current.description || chat.description,
      avatar: current.avatar || chat.avatar,
      external: current.external || chat.external,
    });
  }
  return { ...discovery, chats: Array.from(chats.values()) };
}

function normalizeSlackDiscovery(discovery?: SlackDiscovery): SlackDiscovery {
  if (!discovery || typeof discovery !== "object") {
    return {
      available: false,
      runtime: "managed-socket-mode",
      credentialStored: false,
      botReady: false,
      socketReady: false,
      channels: [],
      error: "Slack setup becomes available after CodexLoom restarts with the latest build.",
    };
  }
  const channels = new Map<string, SlackDiscovery["channels"][number]>();
  for (const channel of discovery.channels || []) {
    const id = channel.id.trim();
    if (!id) continue;
    const current = channels.get(id);
    if (!current) {
      channels.set(id, { ...channel, id });
      continue;
    }
    channels.set(id, {
      ...current,
      name: current.name && current.name !== id ? current.name : channel.name || current.name,
      description: current.description || channel.description,
      private: current.private || channel.private,
      member: current.member || channel.member,
    });
  }
  return { ...discovery, channels: Array.from(channels.values()) };
}

function normalizeParallDiscovery(discovery?: ParallDiscovery): ParallDiscovery {
  if (!discovery || typeof discovery !== "object") {
    return {
      available: false,
      runtime: "managed-websocket",
      ownerCredentialStored: false,
      ownerReady: false,
      agentCredentialStored: false,
      externalReady: false,
      socketReady: false,
      agents: [],
      chats: [],
      error: "Parall setup becomes available after CodexLoom restarts with the latest build.",
    };
  }
  const agents = new Map<string, ParallDiscovery["agents"][number]>();
  for (const agent of discovery.agents || []) {
    if (agent.id?.trim()) agents.set(agent.id.trim(), { ...agent, id: agent.id.trim(), name: agent.name || agent.id });
  }
  const chats = new Map<string, ParallDiscovery["chats"][number]>();
  for (const chat of discovery.chats || []) {
    if (chat.id?.trim()) chats.set(chat.id.trim(), { ...chat, id: chat.id.trim(), name: chat.name || chat.id });
  }
  return { ...discovery, agents: Array.from(agents.values()), chats: Array.from(chats.values()) };
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

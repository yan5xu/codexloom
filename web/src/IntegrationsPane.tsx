import { Cable, Check, Link2, Pencil, Plus, RefreshCw, Unplug, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, type AgentAddress, type PlatformConnection, type Agent } from "./types";

export function IntegrationsPane({ agents, onError }: { agents: Agent[]; onError: (message: string) => void }) {
  const [connections, setConnections] = useState<PlatformConnection[]>([]);
  const [addresses, setAddresses] = useState<AgentAddress[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [bindOpen, setBindOpen] = useState(false);
  const [provider, setProvider] = useState("parall");
  const [accountRef, setAccountRef] = useState("");
  const [credentialRef, setCredentialRef] = useState("");
  const [capabilities, setCapabilities] = useState("receive,send,reply");
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
  const stateRef = useRef<Record<string, unknown>>({});

  const refresh = async () => {
    const [connectionData, addressData] = await Promise.all([
      api("GET", "/api/integrations/connections"),
      api("GET", "/api/integrations/addresses"),
    ]);
    const nextConnections = connectionData.connections || [];
    setConnections(nextConnections);
    setAddresses(addressData.addresses || []);
    setSelectedID((current) => current || nextConnections[0]?.id || "");
  };

  useEffect(() => {
    refresh().catch((error: Error) => onError(error.message));
    const es = new EventSource("/api/events");
    es.onmessage = (event) => {
      try {
        const value = JSON.parse(event.data);
        if (["loom/integration-connection", "loom/integration-address"].includes(value.type)) refresh().catch(() => {});
      } catch {
        // Ignore malformed global events.
      }
    };
    return () => es.close();
  }, []);

  const selected = connections.find((connection) => connection.id === selectedID) || null;
  const selectedAddresses = addresses.filter((address) => address.connectionId === selectedID);
  const connectedCount = connections.filter((connection) => connection.enabled && connection.status === "connected").length;
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
    connectionsCount: connections.length,
    connectedCount,
    addressesCount: addresses.length,
    selectedConnectionID: selectedID,
  };
  useEffect(() => {
    const root = ((((window as any).codexLoom ||= (window as any).codexHub || {}) as Record<string, any>));
	(window as any).codexHub = root;
    root.integrations = {
      state: () => stateRef.current,
      select: async (id: string) => {
        setSelectedID(id);
        await new Promise((resolve) => setTimeout(resolve, 0));
        return { ...stateRef.current, selectedConnectionID: id };
      },
      refresh: async () => {
        await refresh();
        return stateRef.current;
      },
    };
    return () => { delete root.integrations; };
  }, []);

  return (
    <main className="flex w-full min-w-0 max-w-full flex-1 flex-col overflow-hidden bg-background">
      <header className="flex min-h-14 w-full max-w-full shrink-0 items-center gap-3 overflow-hidden border-b border-border bg-card/80 py-2 pl-14 pr-3 md:px-5">
        <Cable className="size-4 shrink-0 text-primary" />
        <h1 className="min-w-0 truncate font-serif text-xl tracking-tight">Integrations</h1>
        <div className="hidden text-[11px] text-muted-foreground sm:block">{connectedCount}/{connections.length} connected · {addresses.length} addresses</div>
        <div className="ml-auto flex items-center gap-1">
          <button onClick={() => refresh().catch((error: Error) => onError(error.message))} title="Refresh" className="flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"><RefreshCw className="size-3.5" /></button>
          <button onClick={() => setCreateOpen((value) => !value)} title="Add connection" className="flex size-8 items-center justify-center rounded-md bg-primary text-primary-foreground hover:opacity-90"><Plus className="size-3.5" /></button>
        </div>
      </header>

      {createOpen && (
        <section className="grid shrink-0 gap-2 border-b border-border bg-card px-4 py-3 sm:grid-cols-2 xl:grid-cols-[150px_1fr_1fr_1fr_auto]">
          <select value={provider} onChange={(event) => setProvider(event.target.value)} className={controlClass}><option value="parall">Parall</option><option value="lark">Lark</option><option value="fake">Fake</option><option value="custom">Custom</option></select>
          <input value={accountRef} onChange={(event) => setAccountRef(event.target.value)} placeholder="account reference" className={controlClass} />
          <input value={credentialRef} onChange={(event) => setCredentialRef(event.target.value)} placeholder="env:VARIABLE" className={controlClass} />
          <input value={capabilities} onChange={(event) => setCapabilities(event.target.value)} placeholder="receive,send,reply" className={controlClass} />
          <button onClick={createConnection} disabled={working} className="h-8 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50">Add</button>
        </section>
      )}

      <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden lg:grid-cols-[320px_1fr]">
        <section className={`${selected ? "hidden lg:block" : "block"} min-h-0 overflow-y-auto border-r border-border`}>
          {connections.map((connection) => (
            <button key={connection.id} onClick={() => setSelectedID(connection.id)} className={`block w-full border-b border-border px-4 py-3 text-left ${selectedID === connection.id ? "bg-selection text-selection-foreground" : "hover:bg-muted/45"}`}>
              <div className="flex min-w-0 items-center gap-2"><ConnectionDot connection={connection} /><span className="min-w-0 flex-1 truncate text-[13px] font-semibold capitalize">{connection.provider}</span><span className="font-mono text-[9px] uppercase text-muted-foreground">{connection.status}</span></div>
              <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">{connection.accountRef || connection.id}</div>
              <div className="mt-1 text-[10px] text-muted-foreground">{addresses.filter((address) => address.connectionId === connection.id).length} addresses</div>
            </button>
          ))}
          {connections.length === 0 && <Empty label="No integrations" />}
        </section>

        <section className={`${selected ? "block" : "hidden lg:block"} min-h-0 overflow-y-auto`}>
          {selected ? (
            <div className="mx-auto max-w-4xl p-4 md:p-6">
              <button onClick={() => setSelectedID("")} className="mb-4 text-[12px] text-muted-foreground lg:hidden">← Integrations</button>
              <div className="flex min-w-0 items-start gap-3 border-b border-border pb-4">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted"><Cable className="size-4 text-primary" /></div>
                <div className="min-w-0"><h2 className="truncate text-lg font-semibold capitalize">{selected.provider}</h2><div className="truncate font-mono text-[10px] text-muted-foreground">{selected.id}</div></div>
                <button onClick={() => toggleConnection(selected)} disabled={working} title={selected.enabled ? "Disable connection" : "Enable connection"} className={`ml-auto flex h-8 items-center gap-1.5 rounded-md px-2.5 text-[11px] font-medium ${selected.enabled ? "border border-border text-muted-foreground" : "bg-primary text-primary-foreground"}`}>
                  {selected.enabled ? <Unplug className="size-3.5" /> : <Check className="size-3.5" />}{selected.enabled ? "Disable" : "Enable"}
                </button>
              </div>
              <dl className="grid gap-x-6 gap-y-3 border-b border-border py-4 text-[11px] sm:grid-cols-2 xl:grid-cols-3">
                <Meta label="Status" value={selected.status} /><Meta label="Account" value={selected.accountRef || "-"} /><Meta label="Credential" value={selected.credentialRef || "-"} /><Meta label="Heartbeat" value={formatDate(selected.lastHeartbeatAt)} /><Meta label="Last event" value={formatDate(selected.lastEventAt)} /><Meta label="Cursor" value={selected.cursor || "-"} />
              </dl>
              {selected.lastError && <div className="mt-4 border-l-2 border-destructive bg-destructive/5 px-3 py-2 text-[12px] text-destructive">{selected.lastError}</div>}
              <div className="mt-6 flex items-center justify-between"><h3 className="text-[12px] font-semibold uppercase text-muted-foreground">Agent Addresses</h3><button onClick={bindOpen ? resetAddressForm : startBind} className="flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-[11px] font-medium hover:bg-muted">{bindOpen ? <X className="size-3.5" /> : <Link2 className="size-3.5" />}{bindOpen ? "Close" : "Bind"}</button></div>
              {bindOpen && (
                <div className="mt-3 grid gap-2 border-y border-border bg-card py-3 sm:grid-cols-2 xl:grid-cols-3">
                  <select value={agent} disabled={Boolean(editingAddressID)} onChange={(event) => setAgent(event.target.value)} className={controlClass}><option value="">Agent</option>{agents.map((agent) => <option key={agent.id} value={agent.name}>{agent.name}</option>)}</select>
                  <input value={identity} onChange={(event) => setIdentity(event.target.value)} placeholder="external identity" className={controlClass} />
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
              <div className="mt-3 divide-y divide-border border-y border-border">
                {selectedAddresses.map((address) => (
                  <div key={address.id} className="flex min-w-0 items-center gap-3 py-3">
                    <span className={`size-2 shrink-0 rounded-full ${address.enabled ? "bg-success" : "bg-muted-foreground/40"}`} />
                    <div className="min-w-0 flex-1"><div className="truncate text-[12.5px] font-medium">{address.displayName || address.externalIdentity}</div><div className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">{agentName(address.agentId)} · {address.externalIdentity}</div></div>
                    <div className="hidden max-w-60 text-right text-[10px] text-muted-foreground sm:block"><div>{address.triggerPolicy}</div><div className="truncate">{address.replyPolicy} · {address.trustDomain}</div><div className="truncate">{policySummary(address)}</div></div>
                    <button onClick={() => editAddress(address)} disabled={working} title="Edit address" className="flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"><Pencil className="size-3.5" /></button>
                    <button onClick={() => toggleAddress(address)} disabled={working} title={address.enabled ? "Disable address" : "Enable address"} className="flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground">{address.enabled ? <Unplug className="size-3.5" /> : <Check className="size-3.5" />}</button>
                  </div>
                ))}
                {selectedAddresses.length === 0 && <div className="py-8 text-center text-[12px] text-muted-foreground">No addresses</div>}
              </div>
            </div>
          ) : <Empty label="Select an integration" />}
        </section>
      </div>
    </main>
  );
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

function formatDate(value?: string) {
  if (!value) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

const controlClass = "h-8 min-w-0 rounded-md border border-border bg-background px-2.5 text-[12px] outline-none focus:border-ring";

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

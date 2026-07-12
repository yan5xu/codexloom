import { Check, Loader2, Pencil, Plus, Send, SlidersHorizontal, Square, Trash2, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useEffect, useReducer, useRef, useState } from "react";
import { api, type Agent, type AgentAddress, type AgentProfile, type ConversationMembership, type PlatformConnection } from "./types";
import { emptyFeed, reduceFeed, summarizeTask } from "./feed";
import type { LoomEvent } from "./types";
import { BlockView } from "./Blocks";

const MODEL_PRESETS = [
  { value: "", label: "Default (gpt-5.6)" },
  { value: "gpt-5.6", label: "GPT-5.6 default" },
  { value: "gpt-5.6-sol", label: "GPT-5.6 Sol" },
  { value: "gpt-5.6-terra", label: "GPT-5.6 Terra" },
  { value: "gpt-5.6-luna", label: "GPT-5.6 Luna" },
];

const CUSTOM_MODEL_VALUE = "__custom";

type MembershipDraft = {
  id: string;
  addressId: string;
  conversationId: string;
  displayName: string;
  purpose: string;
  role: string;
  guidance: string;
  triggerPolicy: AgentAddress["triggerPolicy"];
  replyPolicy: AgentAddress["replyPolicy"];
  trustDomain: string;
  enabled: boolean;
  version: number;
};

export function AgentPane({
  agent,
  onKilled,
  onError,
  onAgentUpdated,
}: {
  agent: Agent;
  onKilled: () => void;
  onError: (msg: string) => void;
  onAgentUpdated: (agent: Agent) => void;
}) {
  const [feed, dispatch] = useReducer(reduceFeed, emptyFeed);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [configOpen, setConfigOpen] = useState(false);
  const [configSection, setConfigSection] = useState<"agent" | "profile" | "connections">("agent");
  const [nameDraft, setNameDraft] = useState(agent.name);
  const [modelDraft, setModelDraft] = useState(agent.model || "");
  const [modelCustomOpen, setModelCustomOpen] = useState(isCustomModel(agent.model || ""));
  const [effortDraft, setEffortDraft] = useState(agent.effort || "");
  const [sandboxDraft, setSandboxDraft] = useState(agent.sandbox || "danger-full-access");
  const [approvalDraft, setApprovalDraft] = useState(agent.approvalPolicy || "never");
  const [savingConfig, setSavingConfig] = useState(false);
  const [profile, setProfile] = useState<AgentProfile | null>(null);
  const [identityDraft, setIdentityDraft] = useState("");
  const [domainDraft, setDomainDraft] = useState("");
  const [scopeDraft, setScopeDraft] = useState("");
  const [loadingProfile, setLoadingProfile] = useState(false);
  const [savingProfile, setSavingProfile] = useState(false);
  const [addresses, setAddresses] = useState<AgentAddress[]>([]);
  const [connections, setConnections] = useState<PlatformConnection[]>([]);
  const [memberships, setMemberships] = useState<ConversationMembership[]>([]);
  const [membershipDraft, setMembershipDraft] = useState<MembershipDraft | null>(null);
  const [savingMembership, setSavingMembership] = useState(false);
  const feedRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);
  const loadedRef = useRef(0); // turns loaded so far
  const totalRef = useRef(0); // total turns in rollout
  const loadingRef = useRef(false);
  const keepScrollRef = useRef<number | null>(null); // scrollHeight before a prepend
  const membershipStateRef = useRef<Record<string, unknown>>({});
  const sendingRef = useRef(false);

  const PAGE = 25;

  useEffect(() => {
    setNameDraft(agent.name);
    setModelDraft(agent.model || "");
    setModelCustomOpen(isCustomModel(agent.model || ""));
    setEffortDraft(agent.effort || "");
    setSandboxDraft(agent.sandbox || "danger-full-access");
    setApprovalDraft(agent.approvalPolicy || "never");
  }, [agent.id, agent.name, agent.model, agent.effort, agent.sandbox, agent.approvalPolicy]);

  useEffect(() => {
    if (!configOpen) return;
    let cancelled = false;
    setLoadingProfile(true);
    api("GET", `/api/agents/${agent.id}/profile`)
      .then((data) => {
        if (cancelled) return;
        const next = data.profile as AgentProfile;
        setProfile(next);
        setIdentityDraft(next.identity || "");
        setDomainDraft(next.domain || "");
        setScopeDraft(next.scope || "");
      })
      .catch((err: Error) => {
        if (!cancelled) onError(err.message);
      })
      .finally(() => {
        if (!cancelled) setLoadingProfile(false);
      });
    return () => {
      cancelled = true;
    };
  }, [configOpen, agent.id]);

  const refreshConnections = async () => {
    const [addressData, connectionData, membershipData] = await Promise.all([
      api("GET", `/api/agents/${agent.id}/addresses`),
      api("GET", "/api/integrations/connections"),
      api("GET", `/api/integrations/conversations?agent=${encodeURIComponent(agent.id)}`),
    ]);
    setAddresses(addressData.addresses || []);
    setConnections(connectionData.connections || []);
    setMemberships(membershipData.memberships || []);
  };

  useEffect(() => {
    if (!configOpen) return;
    refreshConnections().catch((err: Error) => onError(err.message));
  }, [configOpen, agent.id]);

  // Seed the newest page of past turns from the rollout (single source of
  // history; works for mirror/idle agents with no live event log).
  useEffect(() => {
    let cancelled = false;
    loadedRef.current = 0;
    totalRef.current = 0;
    api("GET", `/api/agents/${agent.id}/thread/history?count=${PAGE}&offset=0`)
      .then((h) => {
        if (cancelled) return;
        totalRef.current = h.total || 0;
        loadedRef.current = (h.turns || []).length;
        dispatch({ type: "__history__", ts: "", data: h } as any);
      })
      .catch(() => {
        /* ignore — live stream still works */
      });
    return () => {
      cancelled = true;
    };
  }, [agent.id]);

  // Scroll-up lazy load: fetch the next older page and prepend it.
  const loadOlder = () => {
    if (loadingRef.current || loadedRef.current >= totalRef.current) return;
    loadingRef.current = true;
    const el = feedRef.current;
    keepScrollRef.current = el ? el.scrollHeight : null;
    const offset = loadedRef.current;
    api("GET", `/api/agents/${agent.id}/thread/history?count=${PAGE}&offset=${offset}`)
      .then((h) => {
        const older = h.turns || [];
        loadedRef.current += older.length;
        totalRef.current = h.total || totalRef.current;
        dispatch({ type: "__history_prepend__", ts: "", data: { turns: older, offset } } as any);
      })
      .finally(() => {
        loadingRef.current = false;
      });
  };

  // Live event stream: replay=0 → history is the single source of the past,
  // events carry only new activity after open (no duplication with history).
  useEffect(() => {
    const es = new EventSource(`/api/agents/${agent.id}/thread/events?replay=0`);
    es.onmessage = (e) => {
      try {
        dispatch(JSON.parse(e.data) as LoomEvent);
      } catch {
        /* ignore */
      }
    };
    return () => es.close();
  }, [agent.id]);

  // After blocks change: if a prepend just happened, preserve the scroll
  // position (keep the same turn under the viewport); otherwise autoscroll to
  // bottom while pinned there.
  useEffect(() => {
    const el = feedRef.current;
    if (!el) return;
    if (keepScrollRef.current !== null) {
      el.scrollTop += el.scrollHeight - keepScrollRef.current;
      keepScrollRef.current = null;
      return;
    }
    if (stickRef.current) el.scrollTop = el.scrollHeight;
  }, [feed.blocks]);

  const onScroll = () => {
    const el = feedRef.current;
    if (!el) return;
    stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 60;
    if (el.scrollTop < 120) loadOlder(); // near top → load older page
  };

  const send = async () => {
    const draft = input;
    const text = draft.trim();
    if (!text || sendingRef.current) return;
    sendingRef.current = true;
    setSending(true);
    setInput("");
    try {
      await api("POST", `/api/agents/${agent.id}/turns`, { text });
    } catch (err: any) {
      setInput(draft);
      onError(err.message);
    } finally {
      sendingRef.current = false;
      setSending(false);
    }
  };

  const interrupt = async () => {
    try {
      await api("POST", `/api/agents/${agent.id}/turns/current/interrupt`);
    } catch (err: any) {
      onError(err.message);
    }
  };

  const kill = async () => {
    if (!confirm(`archive agent "${agent.name}" and its Codex thread?`))
      return;
    try {
      await api("DELETE", `/api/agents/${agent.id}`);
      onKilled();
    } catch (err: any) {
      onError(err.message);
    }
  };

  const resolveApproval = async (approvalId: string, decision: string) => {
    try {
      await api("POST", `/api/agents/${agent.id}/thread/approvals/${approvalId}`, { decision });
    } catch (err: any) {
      onError(err.message);
    }
  };

  const saveConfig = async () => {
    if (running || savingConfig) return;
    const nextName = nameDraft.trim();
    if (!nextName) {
      onError("name is required");
      return;
    }
    if (!/^[a-zA-Z0-9_-]+$/.test(nextName)) {
      onError("name must match [a-zA-Z0-9_-]+");
      return;
    }
    setSavingConfig(true);
    try {
      const data = await api("PATCH", `/api/agents/${agent.id}/config`, {
        name: nextName,
        model: modelDraft.trim(),
        effort: effortDraft,
        sandbox: sandboxDraft,
        approvalPolicy: approvalDraft,
      });
      onAgentUpdated(data.agent);
      setConfigOpen(false);
    } catch (err: any) {
      onError(err.message);
    } finally {
      setSavingConfig(false);
    }
  };

  const saveProfile = async () => {
    if (!profile || loadingProfile || savingProfile) return;
    setSavingProfile(true);
    try {
      const data = await api("PUT", `/api/agents/${agent.id}/profile`, {
        identity: identityDraft.trim(),
        domain: domainDraft.trim(),
        scope: scopeDraft.trim(),
        expectedVersion: profile.version,
      });
      const next = data.profile as AgentProfile;
      setProfile(next);
      setIdentityDraft(next.identity || "");
      setDomainDraft(next.domain || "");
      setScopeDraft(next.scope || "");
    } catch (err: any) {
      onError(err.message);
    } finally {
      setSavingProfile(false);
    }
  };

  const startMembership = (address: AgentAddress) => {
    setMembershipDraft({
      id: "",
      addressId: address.id,
      conversationId: "",
      displayName: "",
      purpose: "",
      role: "",
      guidance: "",
      triggerPolicy: address.triggerPolicy,
      replyPolicy: address.replyPolicy,
      trustDomain: address.trustDomain,
      enabled: true,
      version: 0,
    });
  };

  const editMembership = (membership: ConversationMembership) => {
    setMembershipDraft({
      id: membership.id,
      addressId: membership.addressId,
      conversationId: membership.conversationId,
      displayName: membership.displayName || "",
      purpose: membership.purpose || "",
      role: membership.role || "",
      guidance: membership.guidance || "",
      triggerPolicy: membership.triggerPolicy,
      replyPolicy: membership.replyPolicy,
      trustDomain: membership.trustDomain,
      enabled: membership.enabled,
      version: membership.version,
    });
  };

  const saveMembership = async () => {
    if (!membershipDraft || savingMembership) return;
    if (!membershipDraft.conversationId.trim()) {
      onError("conversation id is required");
      return;
    }
    setSavingMembership(true);
    try {
      await api(
        "PUT",
        `/api/integrations/addresses/${encodeURIComponent(membershipDraft.addressId)}/conversations/${encodeURIComponent(membershipDraft.conversationId.trim())}`,
        {
          displayName: membershipDraft.displayName.trim(),
          purpose: membershipDraft.purpose.trim(),
          role: membershipDraft.role.trim(),
          guidance: membershipDraft.guidance.trim(),
          triggerPolicy: membershipDraft.triggerPolicy,
          replyPolicy: membershipDraft.replyPolicy,
          trustDomain: membershipDraft.trustDomain,
          enabled: membershipDraft.enabled,
          expectedVersion: membershipDraft.version,
        },
      );
      await refreshConnections();
      setMembershipDraft(null);
    } catch (err: any) {
      onError(err.message);
    } finally {
      setSavingMembership(false);
    }
  };

  const toggleMembership = async (membership: ConversationMembership) => {
    if (savingMembership) return;
    setSavingMembership(true);
    try {
      await api("PATCH", `/api/integrations/conversations/${encodeURIComponent(membership.id)}`, {
        enabled: !membership.enabled,
        expectedVersion: membership.version,
      });
      await refreshConnections();
    } catch (err: any) {
      onError(err.message);
    } finally {
      setSavingMembership(false);
    }
  };

  const approvalEntries = Object.entries(feed.approvals);
  const running = agent.status === "running";
  const modelLabel = agent.model || "default model";
  const effortLabel = displayEffort(agent.effort);
  const sandboxLabel = agent.sandbox || "danger-full-access";
  const approvalLabel = agent.approvalPolicy || "never";
  const currentTaskLabel = agent.currentTask ? summarizeTask(agent.currentTask) : "";
  const modelPresetValue = modelCustomOpen || isCustomModel(modelDraft) ? CUSTOM_MODEL_VALUE : modelDraft;
  const profileDirty = Boolean(
    profile &&
      (identityDraft.trim() !== (profile.identity || "") ||
        domainDraft.trim() !== (profile.domain || "") ||
        scopeDraft.trim() !== (profile.scope || "")),
  );

  membershipStateRef.current = {
    agentId: agent.id,
    count: memberships.length,
    enabledCount: memberships.filter((membership) => membership.enabled).length,
    selectedMembershipId: membershipDraft?.id || "",
    editingConversationId: membershipDraft?.conversationId || "",
  };
  useEffect(() => {
    const root = ((((window as any).codexLoom ||= (window as any).codexHub || {}) as Record<string, any>));
    (window as any).codexHub = root;
    root.conversations = {
      state: () => membershipStateRef.current,
      select: async (id: string) => {
        const membership = memberships.find((value) => value.id === id);
        if (!membership) throw new Error(`conversation membership not found: ${id}`);
        setConfigOpen(true);
        setConfigSection("connections");
        editMembership(membership);
        await new Promise((resolve) => setTimeout(resolve, 0));
        return { ...membershipStateRef.current, selectedMembershipId: id };
      },
      refresh: async () => {
        await refreshConnections();
        return membershipStateRef.current;
      },
    };
    return () => { delete root.conversations; };
  }, [agent.id, memberships]);

  return (
    <main className="flex w-full min-w-0 max-w-full flex-1 flex-col overflow-hidden bg-background">
      {/* header — serif title, status pill, mono meta; soft warm shadow, no hard border */}
      <header
        className="relative grid w-full max-w-full shrink-0 gap-y-2 overflow-hidden py-2 pl-14 pr-3 md:flex md:items-center md:gap-3 md:overflow-visible md:px-6 md:py-2.5"
        style={{ boxShadow: "0 4px 12px -6px oklch(0.2 0.03 78 / 0.12)" }}
      >
        <div className="min-w-0 pr-20 md:order-1 md:pr-0">
          <h1 className="truncate font-serif text-xl leading-none tracking-tight">{agent.name}</h1>
          <div className="mt-1 hidden truncate font-mono text-[10px] uppercase tracking-widest text-muted-foreground/80 md:block">
            {agent.cwd} · {agent.threadId}
          </div>
          <div className="mt-1 hidden max-w-full flex-wrap items-center gap-1.5 overflow-hidden md:flex">
            <span className="max-w-full truncate rounded-md bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
              {modelLabel}
            </span>
            <span className="max-w-full truncate rounded-md bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
              think {effortLabel}
            </span>
            <span className="max-w-full truncate rounded-md bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
              {sandboxLabel}
            </span>
            <span className="max-w-full truncate rounded-md bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
              approval {approvalLabel}
            </span>
          </div>
        </div>
        <span
          className={`inline-flex w-auto min-w-0 max-w-full justify-self-start items-center gap-1.5 rounded-full px-2.5 py-0.5 text-[10px] font-medium md:order-2 md:ml-1 md:shrink-0 ${
            running
              ? "bg-warning/10 text-warning"
              : agent.status === "idle"
                ? "bg-success/10 text-success"
                : "bg-muted text-muted-foreground"
          }`}
        >
          <span
            className={`h-1.5 w-1.5 rounded-full ${
              running ? "animate-pulse bg-warning" : agent.status === "idle" ? "bg-success" : "bg-muted-foreground/50"
            }`}
          />
          <span className="shrink-0">{agent.status}</span>
          {currentTaskLabel && <span className="min-w-0 truncate">— {currentTaskLabel}</span>}
        </span>
        <div className="hidden md:order-3 md:block md:flex-1" />
        <div className="absolute right-3 top-2 flex shrink-0 items-start justify-end gap-1.5 md:relative md:right-auto md:top-auto md:order-4 md:items-center">
          <button
            onClick={() => setConfigOpen((v) => !v)}
            className="flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            title="agent config"
            aria-label="agent config"
          >
            <SlidersHorizontal className="size-4" />
          </button>
          {configOpen && (
            <div className="absolute right-0 top-10 z-20 max-h-[calc(100vh-4.5rem)] w-[calc(100vw-4rem)] max-w-[580px] overflow-y-auto rounded-md border border-border bg-card p-3 shadow-card md:w-[560px]">
              <div className="mb-2 text-[11px] font-semibold uppercase text-muted-foreground">
                Agent Config
              </div>
              <div className="mb-3 grid grid-cols-3 rounded-md bg-muted/60 p-1 text-[12px] font-medium">
                <button onClick={() => setConfigSection("agent")} className={`h-7 rounded ${configSection === "agent" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}>Agent</button>
                <button onClick={() => setConfigSection("profile")} className={`h-7 rounded ${configSection === "profile" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}>Profile</button>
                <button onClick={() => setConfigSection("connections")} className={`h-7 rounded ${configSection === "connections" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}>Connections</button>
              </div>

              {configSection === "agent" ? (
                <>
                  <label className="mb-2 block">
                    <span className="mb-1 block text-[11px] text-muted-foreground">Name</span>
                    <input
                      value={nameDraft}
                      onChange={(e) => setNameDraft(e.target.value)}
                      disabled={running}
                      placeholder="agent-name"
                      spellCheck={false}
                      className="h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition placeholder:text-muted-foreground/60 focus:ring-primary/40 disabled:opacity-60"
                    />
                  </label>
                  <label className="mb-2 block">
                    <span className="mb-1 block text-[11px] text-muted-foreground">Model</span>
                    <select
                      value={modelPresetValue}
                      onChange={(e) => {
                        if (e.target.value === CUSTOM_MODEL_VALUE) {
                          setModelCustomOpen(true);
                          if (MODEL_PRESETS.some((option) => option.value === modelDraft)) setModelDraft("");
                          return;
                        }
                        setModelCustomOpen(false);
                        setModelDraft(e.target.value);
                      }}
                      disabled={running}
                      className="h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition placeholder:text-muted-foreground/60 focus:ring-primary/40 disabled:opacity-60"
                    >
                      {MODEL_PRESETS.map((option) => (
                        <option key={option.label} value={option.value}>{option.label}</option>
                      ))}
                      <option value={CUSTOM_MODEL_VALUE}>Custom...</option>
                    </select>
                    {modelCustomOpen && (
                      <input
                        value={modelDraft}
                        onChange={(e) => setModelDraft(e.target.value)}
                        disabled={running}
                        placeholder="model id"
                        spellCheck={false}
                        className="mt-2 h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition placeholder:text-muted-foreground/60 focus:ring-primary/40 disabled:opacity-60"
                      />
                    )}
                  </label>
                  <label className="mb-2 block">
                    <span className="mb-1 block text-[11px] text-muted-foreground">Thinking Effort</span>
                    <select value={effortDraft} onChange={(e) => setEffortDraft(e.target.value)} disabled={running} className="h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition focus:ring-primary/40 disabled:opacity-60">
                      <option value="">default</option><option value="minimal">minimal</option><option value="low">low</option><option value="medium">medium</option><option value="high">high</option><option value="xhigh">extra high</option>
                    </select>
                  </label>
                  <label className="mb-2 block">
                    <span className="mb-1 block text-[11px] text-muted-foreground">Sandbox</span>
                    <select value={sandboxDraft} onChange={(e) => setSandboxDraft(e.target.value)} disabled={running} className="h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition focus:ring-primary/40 disabled:opacity-60">
                      <option value="danger-full-access">danger-full-access</option><option value="workspace-write">workspace-write</option><option value="read-only">read-only</option>
                    </select>
                  </label>
                  <label className="mb-3 block">
                    <span className="mb-1 block text-[11px] text-muted-foreground">Approval Policy</span>
                    <select value={approvalDraft} onChange={(e) => setApprovalDraft(e.target.value)} disabled={running} className="h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition focus:ring-primary/40 disabled:opacity-60">
                      <option value="never">never</option><option value="on-request">on-request</option>
                    </select>
                  </label>
                  {running && <div className="mb-2 rounded-md bg-warning/10 px-2 py-1.5 text-[11px] text-warning">Config can be changed after this turn finishes.</div>}
                  <div className="flex justify-end gap-2">
                    <button onClick={() => setConfigOpen(false)} className="rounded-md px-2.5 py-1.5 text-[12px] text-muted-foreground transition-colors hover:bg-muted">Cancel</button>
                    <button onClick={saveConfig} disabled={running || savingConfig} className="rounded-md bg-primary px-2.5 py-1.5 text-[12px] font-medium text-primary-foreground transition-colors hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60">{savingConfig ? "Saving" : "Save"}</button>
                  </div>
                </>
              ) : configSection === "profile" ? (
                loadingProfile ? (
                  <div className="py-8 text-center text-[12px] text-muted-foreground">Loading profile...</div>
                ) : profile ? (
                  <>
                    <div className="mb-3 flex items-center justify-between border-b border-border pb-2">
                      <span className="text-[12px] font-medium">Collaboration Profile</span>
                      <span className="font-mono text-[10px] text-muted-foreground">version {profile.version}</span>
                    </div>
                    <AgentProfileField label="Identity" value={identityDraft} onChange={setIdentityDraft} rows={3} placeholder="Who is this long-lived agent?" />
                    <AgentProfileField label="Domain" value={domainDraft} onChange={setDomainDraft} rows={4} placeholder="What enduring subject does this agent maintain?" />
                    <AgentProfileField label="Scope" value={scopeDraft} onChange={setScopeDraft} rows={6} placeholder="What does it own, decide, and explicitly not own?" />
                    <div className="mt-3 flex justify-end gap-2">
                      <button onClick={() => setConfigOpen(false)} className="rounded-md px-2.5 py-1.5 text-[12px] text-muted-foreground transition-colors hover:bg-muted">Close</button>
                      <button onClick={saveProfile} disabled={!profileDirty || savingProfile} className="rounded-md bg-primary px-2.5 py-1.5 text-[12px] font-medium text-primary-foreground transition-colors hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60">{savingProfile ? "Saving" : "Save Profile"}</button>
                    </div>
                  </>
                ) : (
                  <div className="py-8 text-center text-[12px] text-muted-foreground">Profile unavailable.</div>
                )
              ) : (
                <>
                  <div className="mb-2 flex items-center justify-between">
                    <span className="text-[12px] font-medium">External identities</span>
                    <span className="font-mono text-[10px] text-muted-foreground">{memberships.length} conversations</span>
                  </div>
                  <div className="divide-y divide-border border-y border-border">
                    {addresses.map((address) => {
                      const connection = connections.find((value) => value.id === address.connectionId);
                      const addressMemberships = memberships.filter((value) => value.addressId === address.id);
                      return (
                        <section key={address.id} className="py-3">
                          <div className="flex min-w-0 items-center gap-2">
                            <span className={`size-2 shrink-0 rounded-full ${address.enabled && connection?.status === "connected" ? "bg-success" : address.enabled ? "bg-warning" : "bg-muted-foreground/40"}`} />
                            <div className="min-w-0 flex-1">
                              <div className="truncate text-[12px] font-medium">{address.displayName || address.externalIdentity}</div>
                              <div className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">{connection?.provider || address.connectionId} · {address.externalIdentity}</div>
                            </div>
                            <div className="shrink-0 text-right font-mono text-[9px] uppercase text-muted-foreground">
                              <div>{address.trustDomain}</div><div>{connection?.status || "missing"}</div>
                            </div>
                            <button onClick={() => startMembership(address)} title="Add conversation" aria-label="Add conversation" className="flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground">
                              <Plus className="size-3.5" />
                            </button>
                          </div>
                          <div className="ml-4 mt-2 border-l border-border pl-3">
                            {addressMemberships.map((membership) => (
                              <div key={membership.id} className={`flex min-w-0 items-center gap-2 py-1.5 ${membershipDraft?.id === membership.id ? "text-foreground" : "text-muted-foreground"}`}>
                                <span className={`size-1.5 shrink-0 rounded-full ${membership.enabled ? "bg-success" : "bg-muted-foreground/35"}`} />
                                <button onClick={() => editMembership(membership)} className="min-w-0 flex-1 text-left">
                                  <div className="truncate text-[11.5px] font-medium text-foreground">{membership.displayName || membership.conversationId}</div>
                                  <div className="truncate font-mono text-[9px]">{membership.conversationId} · v{membership.version}</div>
                                </button>
                                <span className="hidden shrink-0 font-mono text-[9px] uppercase sm:block">{membership.triggerPolicy}</span>
                                <button onClick={() => editMembership(membership)} title="Edit conversation" aria-label="Edit conversation" className="flex size-7 shrink-0 items-center justify-center rounded-md hover:bg-muted hover:text-foreground"><Pencil className="size-3" /></button>
                                <button onClick={() => toggleMembership(membership)} disabled={savingMembership} title={membership.enabled ? "Disable conversation" : "Enable conversation"} aria-label={membership.enabled ? "Disable conversation" : "Enable conversation"} className="flex size-7 shrink-0 items-center justify-center rounded-md hover:bg-muted hover:text-foreground"><Check className={`size-3 ${membership.enabled ? "opacity-100" : "opacity-30"}`} /></button>
                              </div>
                            ))}
                            {addressMemberships.length === 0 && <div className="py-2 text-[10.5px] text-muted-foreground">No managed conversations</div>}
                          </div>
                        </section>
                      );
                    })}
                    {addresses.length === 0 && <div className="py-8 text-center text-[12px] text-muted-foreground">No external addresses.</div>}
                  </div>
                  {membershipDraft && (
                    <section className="mt-3 border-t border-border pt-3">
                      <div className="mb-3 flex items-center gap-2">
                        <div className="min-w-0 flex-1">
                          <div className="text-[12px] font-medium">{membershipDraft.id ? "Conversation membership" : "New conversation"}</div>
                          <div className="truncate font-mono text-[9px] text-muted-foreground">{membershipDraft.id || membershipDraft.addressId}</div>
                        </div>
                        <span className="font-mono text-[9px] text-muted-foreground">{membershipDraft.version ? `v${membershipDraft.version}` : "new"}</span>
                        <button onClick={() => setMembershipDraft(null)} title="Close editor" aria-label="Close editor" className="flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"><X className="size-3.5" /></button>
                      </div>
                      <div className="grid gap-2 sm:grid-cols-2">
                        <MembershipInput label="Conversation ID" value={membershipDraft.conversationId} disabled={Boolean(membershipDraft.id)} mono onChange={(value) => setMembershipDraft((current) => current ? { ...current, conversationId: value } : current)} />
                        <MembershipInput label="Display name" value={membershipDraft.displayName} onChange={(value) => setMembershipDraft((current) => current ? { ...current, displayName: value } : current)} />
                      </div>
                      <MembershipTextarea label="Purpose" value={membershipDraft.purpose} rows={3} onChange={(value) => setMembershipDraft((current) => current ? { ...current, purpose: value } : current)} />
                      <MembershipTextarea label="Role" value={membershipDraft.role} rows={3} onChange={(value) => setMembershipDraft((current) => current ? { ...current, role: value } : current)} />
                      <MembershipTextarea label="Guidance" value={membershipDraft.guidance} rows={5} onChange={(value) => setMembershipDraft((current) => current ? { ...current, guidance: value } : current)} />
                      <div className="grid gap-2 sm:grid-cols-3">
                        <label className="block"><span className="mb-1 block text-[10px] uppercase text-muted-foreground">Trigger</span><select value={membershipDraft.triggerPolicy} onChange={(event) => setMembershipDraft((current) => current ? { ...current, triggerPolicy: event.target.value as AgentAddress["triggerPolicy"] } : current)} className={membershipControlClass}><option value="mention">mention</option><option value="direct">direct</option><option value="explicit_dispatch">explicit dispatch</option><option value="all">all</option><option value="allowlist">allowlist</option></select></label>
                        <label className="block"><span className="mb-1 block text-[10px] uppercase text-muted-foreground">Reply</span><select value={membershipDraft.replyPolicy} onChange={(event) => setMembershipDraft((current) => current ? { ...current, replyPolicy: event.target.value as AgentAddress["replyPolicy"] } : current)} className={membershipControlClass}><option value="final_answer">final answer</option><option value="explicit">explicit</option><option value="none">none</option></select></label>
                        <MembershipInput label="Trust domain" value={membershipDraft.trustDomain} disabled mono onChange={() => {}} />
                      </div>
                      <div className="mt-3 flex justify-end gap-2">
                        <button onClick={() => setMembershipDraft(null)} className="h-8 rounded-md px-3 text-[11px] text-muted-foreground hover:bg-muted">Cancel</button>
                        <button onClick={saveMembership} disabled={savingMembership || !membershipDraft.conversationId.trim()} className="h-8 rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground disabled:opacity-50">{savingMembership ? "Saving" : "Save conversation"}</button>
                      </div>
                    </section>
                  )}
                </>
              )}
            </div>
          )}
          {running && (
            <button
              onClick={interrupt}
              className="flex size-8 shrink-0 items-center justify-center rounded-xl text-muted-foreground transition-colors hover:bg-warning/10 hover:text-warning md:h-8 md:w-auto md:px-3 md:text-[13px]"
              aria-label="interrupt turn"
            >
              <Square className="size-3.5 md:hidden" />
              <span className="hidden md:inline">interrupt</span>
            </button>
          )}
          <button
            onClick={kill}
            className="flex size-8 shrink-0 items-center justify-center rounded-xl text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive md:h-8 md:w-auto md:px-3 md:text-[13px]"
            aria-label="archive agent"
          >
            <Trash2 className="size-3.5 md:hidden" />
            <span className="hidden md:inline">kill</span>
          </button>
        </div>
      </header>

      {/* event feed + pending approvals */}
      <div ref={feedRef} onScroll={onScroll} className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-3xl px-4 pb-8 pt-4 md:px-6">
          {/* pending approvals */}
          {approvalEntries.map(([id, ap]) => (
            <div key={id} className="mb-3 rounded-2xl border border-warning/30 bg-warning/5 px-4 py-3.5 shadow-card">
              <div className="mb-2 text-sm">
                <span className="text-warning">⚠</span> <b>codex requests approval</b> —{" "}
                <span className="font-mono text-[12px]">{ap.method}</span>
              </div>
              <pre className="mb-3 max-h-40 overflow-auto whitespace-pre-wrap rounded-xl bg-muted/50 px-3 py-2 font-mono text-[12px] text-muted-foreground">
                {JSON.stringify(ap.params, null, 2)}
              </pre>
              <button
                onClick={() => resolveApproval(id, "accept")}
                className="mr-2 rounded-xl bg-primary px-3.5 py-1.5 text-[13px] font-medium text-primary-foreground transition-colors hover:opacity-90"
              >
                approve
              </button>
              <button
                onClick={() => resolveApproval(id, "reject")}
                className="rounded-xl px-3.5 py-1.5 text-[13px] text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
              >
                reject
              </button>
            </div>
          ))}

          {feed.blocks.map((b, i) => (
            <BlockView key={i} block={b} />
          ))}
        </div>
      </div>

      {/* composer — faithful AgentComposer layout: shimmer divider, rounded-2xl
          ring container, icon send/stop, centered hint. */}
      <div className="relative shrink-0 bg-background px-3 py-2 pb-[max(0.5rem,env(safe-area-inset-bottom))] md:px-6 md:py-3 md:pb-3">
        <div
          className="absolute inset-x-0 top-0 h-px"
          style={{
            background:
              "linear-gradient(90deg, transparent, oklch(0.88 0.01 78 / 0.4) 20%, oklch(0.88 0.01 78 / 0.4) 80%, transparent)",
          }}
        />
        <div className="mx-auto max-w-3xl">
          <div className="flex flex-col gap-2 rounded-2xl bg-card p-2 shadow-[0_4px_20px_rgba(0,0,0,0.02)] ring-1 ring-black/[0.04] transition-all duration-150 focus-within:ring-black/[0.08]">
            <textarea
              value={input}
              disabled={sending}
              aria-label="task message"
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing && !sending) {
                  e.preventDefault();
                  send();
                }
              }}
              placeholder={`Send a task to ${agent.name}…`}
              className="max-h-[200px] min-h-11 resize-none overflow-y-auto bg-transparent px-3 py-2 text-sm outline-none placeholder:text-muted-foreground/50 disabled:cursor-wait disabled:opacity-60"
            />
            <div className="flex items-center justify-end px-1">
              {running ? (
                <button
                  onClick={interrupt}
                  className="flex size-9 shrink-0 cursor-pointer items-center justify-center rounded-xl bg-primary text-primary-foreground shadow-[0_4px_12px_rgba(139,92,246,0.2)] transition-all duration-150 hover:bg-primary/90"
                >
                  <Square className="size-4" />
                </button>
              ) : (
                <button
                  onClick={send}
                  disabled={sending || !input.trim()}
                  aria-label={sending ? "sending task" : "send task"}
                  className={cn(
                    "flex size-9 shrink-0 items-center justify-center rounded-xl transition-all duration-150",
                    sending
                      ? "cursor-wait bg-primary text-primary-foreground"
                      : input.trim()
                      ? "cursor-pointer bg-primary text-primary-foreground shadow-[0_4px_12px_rgba(139,92,246,0.2)] hover:bg-primary/90"
                      : "cursor-not-allowed bg-muted text-muted-foreground/40",
                  )}
                >
                  {sending ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
                </button>
              )}
            </div>
          </div>
        </div>
        <p className="mt-1.5 text-center font-mono text-[10px] text-muted-foreground/50">
          Enter to send · Shift+Enter for new line
        </p>
      </div>
    </main>
  );
}

function AgentProfileField({
  label,
  value,
  onChange,
  rows,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  rows: number;
  placeholder: string;
}) {
  return (
    <label className="mb-3 block">
      <span className="mb-1 block text-[11px] font-medium uppercase text-muted-foreground">{label}</span>
      <textarea
        value={value}
        onChange={(event) => onChange(event.target.value)}
        rows={rows}
        maxLength={16000}
        placeholder={placeholder}
        className="w-full resize-y rounded-md bg-background p-2.5 text-[12px] leading-5 outline-none ring-1 ring-border focus:ring-primary/40"
      />
    </label>
  );
}

const membershipControlClass = "h-8 w-full min-w-0 rounded-md bg-background px-2 font-mono text-[10.5px] outline-none ring-1 ring-border focus:ring-primary/40 disabled:opacity-60";

function MembershipInput({ label, value, onChange, disabled = false, mono = false }: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  mono?: boolean;
}) {
  return (
    <label className="block">
      <span className="mb-1 block text-[10px] uppercase text-muted-foreground">{label}</span>
      <input value={value} onChange={(event) => onChange(event.target.value)} disabled={disabled} spellCheck={false} className={`${membershipControlClass} ${mono ? "font-mono" : "font-sans"}`} />
    </label>
  );
}

function MembershipTextarea({ label, value, onChange, rows }: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  rows: number;
}) {
  return (
    <label className="mt-2 block">
      <span className="mb-1 block text-[10px] uppercase text-muted-foreground">{label}</span>
      <textarea value={value} onChange={(event) => onChange(event.target.value)} rows={rows} maxLength={16000} className="w-full resize-y rounded-md bg-background p-2.5 text-[11.5px] leading-5 outline-none ring-1 ring-border focus:ring-primary/40" />
    </label>
  );
}

function displayEffort(effort?: string) {
  if (!effort) return "default effort";
  if (effort === "xhigh") return "extra high";
  return effort;
}

function isCustomModel(model: string) {
  return model !== "" && !MODEL_PRESETS.some((option) => option.value === model);
}

import { ArrowDown, ArrowUpRight, BarChart3, CalendarClock, Check, ChevronRight, CircleHelp, FileText, Inbox, Loader2, MessageSquare, Network, Paperclip, Pause, Pencil, Play, Plus, RefreshCw, RotateCcw, Send, SkipForward, Square, Target, Trash2, X } from "lucide-react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { cn } from "@/lib/utils";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { api, uploadThreadArtifact, type Agent, type AgentAddress, type AgentProfile, type AgentTokenUsage, type ConversationMembership, type HumanRequest, type InboxEntry, type PlatformConnection, type Schedule, type TeamView, type ThreadGoal } from "./types";
import { emptyFeed, reduceFeed } from "./feed";
import type { Block } from "./feed";
import type { LoomEvent } from "./types";
import { BlockView } from "./Blocks";
import { UsageBarTooltip, usageDayLabel } from "./components/UsageBarTooltip";
import { Popover, PopoverContent, PopoverTrigger } from "./components/ui/popover";
import { subscribeThreadEvents } from "./thread-events";
import { oldestWaitingMs } from "./product-state";

const MODEL_PRESETS = [
  { value: "", label: "Default (Codex)" },
  { value: "gpt-5.6-sol", label: "GPT-5.6 Sol" },
  { value: "gpt-5.6-terra", label: "GPT-5.6 Terra" },
  { value: "gpt-5.6-luna", label: "GPT-5.6 Luna" },
];

const CUSTOM_MODEL_VALUE = "__custom";

function readableRuntimeError(value: string) {
  const trimmed = value.trim();
  if (!trimmed.startsWith("{")) return trimmed;
  try {
    const parsed = JSON.parse(trimmed);
    return parsed?.error?.message || parsed?.message || trimmed;
  } catch {
    return trimmed;
  }
}

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

type FeedRow =
  | { key: string; kind: "approval"; id: string; approval: { method: string; params: any } }
  | { key: string; kind: "block"; block: Block };

type PendingArtifact = {
  id: string;
  file: File;
  previewUrl?: string;
};

const MAX_TURN_ARTIFACTS = 8;
const MAX_TURN_ARTIFACT_BYTES = 25 * 1024 * 1024;

function formatAttachmentSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function feedBlockKey(block: Block) {
  if (block.kind === "user") return `user:${block.ts}:${block.text.slice(0, 48)}`;
  if (block.kind === "sys") return `sys:${block.ts}:${block.text.slice(0, 48)}`;
  return `${block.kind}:${block.id}`;
}

export function AgentPane({
  agent,
  active,
  configRequestNonce,
  pendingWork,
  humanRequests,
  onOpenPendingWork,
  onOpenHumanRequest,
  onHumanRequestChanged,
  onPendingWorkChanged,
  onOpenUsage,
  onError,
  onAgentUpdated,
}: {
  agent: Agent;
  active: boolean;
  configRequestNonce: number;
  pendingWork: InboxEntry[];
  humanRequests: HumanRequest[];
  onOpenPendingWork: (itemID: string) => void;
  onOpenHumanRequest: (requestID?: string) => void;
  onHumanRequestChanged: () => Promise<unknown> | void;
  onPendingWorkChanged: () => Promise<unknown> | void;
  onOpenUsage: (agentID: string) => void;
  onError: (msg: string) => void;
  onAgentUpdated: (agent: Agent) => void;
}) {
  const [feed, dispatch] = useReducer(reduceFeed, emptyFeed);
  const [input, setInput] = useState("");
  const [attachments, setAttachments] = useState<PendingArtifact[]>([]);
  const [draggingAttachment, setDraggingAttachment] = useState(false);
  const [sendingAttachmentCount, setSendingAttachmentCount] = useState(0);
  const [answeringRequest, setAnsweringRequest] = useState<HumanRequest | null>(null);
  const [sending, setSending] = useState(false);
  const [heldActionID, setHeldActionID] = useState("");
  const [sendStatus, setSendStatus] = useState<"idle" | "sending" | "sent" | "failed">("idle");
  const [sendKind, setSendKind] = useState<"task" | "answer">("task");
  const [configOpen, setConfigOpen] = useState(false);
  const [configSection, setConfigSection] = useState<"profile" | "team" | "external" | "schedules" | "runtime" | "usage">("profile");
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
  const [usage, setUsage] = useState<AgentTokenUsage | null>(null);
  const [loadingUsage, setLoadingUsage] = useState(false);
  const [addresses, setAddresses] = useState<AgentAddress[]>([]);
  const [connections, setConnections] = useState<PlatformConnection[]>([]);
  const [memberships, setMemberships] = useState<ConversationMembership[]>([]);
  const [membershipDraft, setMembershipDraft] = useState<MembershipDraft | null>(null);
  const [savingMembership, setSavingMembership] = useState(false);
  const [team, setTeam] = useState<TeamView | null>(null);
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [showJumpToBottom, setShowJumpToBottom] = useState(false);
  const feedRef = useRef<HTMLDivElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const stickRef = useRef(true);
  const needsInitialBottomRef = useRef(true);
  const savedScrollTopRef = useRef(0);
  const savedScrollAnchorRef = useRef<{ index: number; offset: number } | null>(null);
  const restoringScrollRef = useRef(false);
  const scrollAnchorTimerRef = useRef<number | null>(null);
  const virtualPinTimerRef = useRef<number | null>(null);
  const initialBottomSettleTimerRef = useRef<number | null>(null);
  const loadedRef = useRef(0); // turns loaded so far
  const totalRef = useRef(0); // total turns in rollout
  const loadingRef = useRef(false);
  const keepScrollRef = useRef<number | null>(null); // scrollHeight before a prepend
  const membershipStateRef = useRef<Record<string, unknown>>({});
  const threadStateRef = useRef<Record<string, unknown>>({});
  const sendingRef = useRef(false);
  const sendStatusTimerRef = useRef<number | null>(null);
  const attachmentsRef = useRef<PendingArtifact[]>([]);
  const feedRows = useMemo<FeedRow[]>(() => [
    ...Object.entries(feed.approvals).map(([id, approval]) => ({
      key: `approval:${id}`,
      kind: "approval" as const,
      id,
      approval,
    })),
    ...feed.blocks.map((block) => ({
      key: feedBlockKey(block),
      kind: "block" as const,
      block,
    })),
  ], [feed.approvals, feed.blocks]);
  const feedVirtualizer = useVirtualizer({
    count: feedRows.length,
    getScrollElement: () => feedRef.current,
    getItemKey: (index) => feedRows[index]?.key || index,
    estimateSize: () => 96,
    overscan: 6,
    onChange: (instance, sync) => {
      if (!active) return;
      const el = feedRef.current;
      if (!el || (!needsInitialBottomRef.current && !stickRef.current)) return;
      if (virtualPinTimerRef.current !== null) window.clearTimeout(virtualPinTimerRef.current);
      virtualPinTimerRef.current = window.setTimeout(() => {
        virtualPinTimerRef.current = null;
        if (!active || (!needsInitialBottomRef.current && !stickRef.current) || feedRows.length === 0) return;
        const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
        if (!needsInitialBottomRef.current && distance < 2) return;
        el.scrollTop = el.scrollHeight;
        el.dispatchEvent(new Event("scroll"));
        savedScrollTopRef.current = el.scrollTop;
        if (needsInitialBottomRef.current) {
          if (initialBottomSettleTimerRef.current !== null) window.clearTimeout(initialBottomSettleTimerRef.current);
          initialBottomSettleTimerRef.current = window.setTimeout(() => {
            initialBottomSettleTimerRef.current = null;
            const lastRendered = instance.getVirtualItems().some((item) => item.index === feedRows.length - 1);
            const settledDistance = el.scrollHeight - el.scrollTop - el.clientHeight;
            if (active && el.clientHeight > 0 && lastRendered && settledDistance < 2) {
              needsInitialBottomRef.current = false;
              savedScrollTopRef.current = el.scrollTop;
              return;
            }
            if (!active || el.clientHeight <= 0) return;
            el.scrollTop = el.scrollHeight;
            el.dispatchEvent(new Event("scroll"));
          }, 500);
        }
      }, sync ? 180 : 0);
    },
  });
  const measureFeedRow = useCallback((node: HTMLDivElement | null) => {
    feedVirtualizer.measureElement(node);
    if (!node) return;
    const index = Number(node.dataset.index);
    if (!Number.isInteger(index)) return;

    // Virtual rows first appear while the user is scrolling. TanStack Virtual
    // deliberately skips its synchronous measurement in that state and waits
    // for ResizeObserver, which can leave a tall Markdown block at the 96px
    // estimate long enough for following rows to render on top of it. Measure
    // once on mount; the virtualizer's observer still handles later resizes.
    feedVirtualizer.resizeItem(index, node.getBoundingClientRect().height);
  }, [feedVirtualizer]);

  const PAGE = 25;

  useEffect(() => () => {
    if (sendStatusTimerRef.current !== null) window.clearTimeout(sendStatusTimerRef.current);
    if (scrollAnchorTimerRef.current !== null) window.clearTimeout(scrollAnchorTimerRef.current);
    if (virtualPinTimerRef.current !== null) window.clearTimeout(virtualPinTimerRef.current);
    if (initialBottomSettleTimerRef.current !== null) window.clearTimeout(initialBottomSettleTimerRef.current);
    for (const attachment of attachmentsRef.current) {
      if (attachment.previewUrl) URL.revokeObjectURL(attachment.previewUrl);
    }
  }, []);

  useEffect(() => {
    attachmentsRef.current = attachments;
  }, [attachments]);

  useEffect(() => {
    if (answeringRequest && !humanRequests.some((request) => request.id === answeringRequest.id)) {
      setAnsweringRequest(null);
    }
  }, [answeringRequest, humanRequests]);

  useEffect(() => {
    setNameDraft(agent.name);
    setModelDraft(agent.model || "");
    setModelCustomOpen(isCustomModel(agent.model || ""));
    setEffortDraft(agent.effort || "");
    setSandboxDraft(agent.sandbox || "danger-full-access");
    setApprovalDraft(agent.approvalPolicy || "never");
  }, [agent.id, agent.name, agent.model, agent.effort, agent.sandbox, agent.approvalPolicy]);

  useEffect(() => {
    if (!active || !configRequestNonce) return;
    setConfigSection("runtime");
    setConfigOpen(true);
  }, [active, configRequestNonce]);

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

  useEffect(() => {
    if (!configOpen || (configSection !== "team" && configSection !== "schedules")) return;
    Promise.all([
      api("GET", "/api/team"),
      api("GET", "/api/schedules"),
    ]).then(([teamData, scheduleData]) => {
      setTeam(teamData.team || null);
      setSchedules((scheduleData.schedules || []).filter((schedule: Schedule) => schedule.to === agent.name || schedule.to === agent.id));
    }).catch((err: Error) => onError(err.message));
  }, [configOpen, configSection, agent.id, agent.name]);

  useEffect(() => {
    if (!configOpen || configSection !== "usage") return;
    let cancelled = false;
    const load = async (showLoading: boolean) => {
      if (showLoading) setLoadingUsage(true);
      try {
        const data = await api("GET", `/api/agents/${agent.id}/usage?days=7`);
        if (!cancelled) setUsage(data.usage);
      } catch (err: any) {
        if (!cancelled) onError(err.message);
      } finally {
        if (!cancelled && showLoading) setLoadingUsage(false);
      }
    };
    load(true);
    const timer = window.setInterval(() => load(false), agent.status === "running" ? 5_000 : 15_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [configOpen, configSection, agent.id, agent.status]);

  // Seed the newest page of past turns from the rollout (single source of
  // history; works for mirror/idle agents with no live event log).
  useEffect(() => {
    let cancelled = false;
    loadedRef.current = 0;
    totalRef.current = 0;
    stickRef.current = true;
    setShowJumpToBottom(false);
    api("GET", `/api/agents/${agent.id}/thread/history?count=${PAGE}&offset=0`)
      .then((h) => {
        if (cancelled) return;
        totalRef.current = h.total || 0;
        loadedRef.current = (h.turns || []).length;
        dispatch({ type: "__history__", ts: "", data: h } as any);
      })
      .catch(() => {
        /* ignore — live stream still works */
      })
      .finally(() => {
        api("GET", `/api/agents/${agent.id}/artifacts`)
          .then((data) => {
            if (!cancelled) dispatch({ type: "__published_artifacts__", ts: "", data } as any);
          })
          .catch(() => {
            /* artifacts are supplementary to rollout history */
          });
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

  // App owns the single browser SSE and multiplexes live Thread events by
  // Agent. Keeping this subscription mounted preserves hidden tabs without
  // spending one HTTP connection per tab.
  useEffect(() => {
    let cancelled = false;
    const unsubscribe = subscribeThreadEvents(agent.id, (event) => {
      if (event.type === "loom/reconcile") {
        api("GET", `/api/agents/${agent.id}/thread/history?count=${PAGE}&offset=0`)
          .then((history) => {
            if (cancelled) return;
            totalRef.current = history.total || 0;
            loadedRef.current = (history.turns || []).length;
            dispatch({ type: "__history_reconcile__", ts: "", data: history } as any);
          })
          .catch(() => {});
        return;
      }
      dispatch(event);
      if (event.type === "turn/completed" || event.type.endsWith("turn-completed")) {
        window.setTimeout(() => {
          api("GET", `/api/agents/${agent.id}/thread/history?count=1&offset=0`)
            .then((history) => {
              if (!cancelled && history.turns?.[0]) {
                dispatch({ type: "__turn_usage__", ts: "", data: { turn: history.turns[0] } } as any);
              }
            })
            .catch(() => {});
        }, 100);
      }
    });
    return () => {
      cancelled = true;
      unsubscribe();
    };
  }, [agent.id]);

  // After blocks change: if a prepend just happened, preserve the scroll
  // position (keep the same turn under the viewport); otherwise autoscroll to
  // bottom while pinned there.
  useEffect(() => {
    if (!active) return;
    const el = feedRef.current;
    if (!el) return;
    let settleTimer = 0;
    let restoreTimer = 0;
    const timer = window.setTimeout(() => {
      if (keepScrollRef.current !== null) {
        el.scrollTop += el.scrollHeight - keepScrollRef.current;
        keepScrollRef.current = null;
        savedScrollTopRef.current = el.scrollTop;
        el.dispatchEvent(new Event("scroll"));
        return;
      }
      const shouldPin = needsInitialBottomRef.current || stickRef.current;
      if (shouldPin && feedRows.length > 0) {
        el.scrollTop = el.scrollHeight;
        settleTimer = window.setTimeout(() => {
          if (needsInitialBottomRef.current || stickRef.current) el.scrollTop = el.scrollHeight;
          savedScrollTopRef.current = el.scrollTop;
        }, 100);
      } else if (feedRows.length > 0) {
        restoringScrollRef.current = true;
        const restorePosition = () => {
          const anchor = savedScrollAnchorRef.current;
          if (!anchor) {
            el.scrollTop = savedScrollTopRef.current;
            return;
          }
          const item = feedVirtualizer.getVirtualItems().find((candidate) => candidate.index === anchor.index);
          if (item) el.scrollTop = item.start + anchor.offset;
          else feedVirtualizer.scrollToIndex(anchor.index, { align: "start" });
        };
        const startedAt = Date.now();
        const settleRestore = () => {
          restorePosition();
          el.dispatchEvent(new Event("scroll"));
          if (Date.now() - startedAt >= 1_800) {
            restoringScrollRef.current = false;
            savedScrollTopRef.current = el.scrollTop;
            el.dispatchEvent(new Event("scroll"));
            return;
          }
          restoreTimer = window.setTimeout(settleRestore, 150);
        };
        settleTimer = window.setTimeout(settleRestore, 0);
      }
    }, 0);
    return () => {
      window.clearTimeout(timer);
      window.clearTimeout(settleTimer);
      window.clearTimeout(restoreTimer);
      restoringScrollRef.current = false;
    };
  }, [active, feed.blocks, feedRows.length]);

  const onScroll = () => {
    const el = feedRef.current;
    if (!el) return;
    if (restoringScrollRef.current) return;
    const previousTop = savedScrollTopRef.current;
    const movedUp = el.scrollTop < previousTop - 1;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    if (needsInitialBottomRef.current) {
      if (!movedUp && distance < 60) return;
      needsInitialBottomRef.current = false;
    }
    const pinned = !movedUp && distance < 60;
    if (!pinned) {
      if (virtualPinTimerRef.current !== null) {
        window.clearTimeout(virtualPinTimerRef.current);
        virtualPinTimerRef.current = null;
      }
      if (initialBottomSettleTimerRef.current !== null) {
        window.clearTimeout(initialBottomSettleTimerRef.current);
        initialBottomSettleTimerRef.current = null;
      }
    }
    savedScrollTopRef.current = el.scrollTop;
    stickRef.current = pinned;
    setShowJumpToBottom(!pinned);
    if (pinned) {
      savedScrollAnchorRef.current = null;
    } else {
      if (scrollAnchorTimerRef.current !== null) window.clearTimeout(scrollAnchorTimerRef.current);
      scrollAnchorTimerRef.current = window.setTimeout(() => {
        scrollAnchorTimerRef.current = null;
        const top = el.scrollTop;
        const item = feedVirtualizer.getVirtualItems().find((candidate) => candidate.start <= top && candidate.end >= top)
          || feedVirtualizer.getVirtualItems().find((candidate) => candidate.start >= top);
        if (item) savedScrollAnchorRef.current = { index: item.index, offset: Math.max(0, top - item.start) };
      }, 0);
    }
    if (el.scrollTop < 120) loadOlder(); // near top → load older page
  };

  const jumpToBottom = () => {
    const el = feedRef.current;
    if (!el) return;
    needsInitialBottomRef.current = false;
    stickRef.current = true;
    savedScrollAnchorRef.current = null;
    setShowJumpToBottom(false);
    if (feedRows.length > 0) {
      el.scrollTop = el.scrollHeight;
      el.dispatchEvent(new Event("scroll"));
      window.setTimeout(() => {
        if (!stickRef.current) return;
        el.scrollTop = el.scrollHeight;
        savedScrollTopRef.current = el.scrollTop;
        el.dispatchEvent(new Event("scroll"));
      }, 0);
    }
  };

  const addAttachmentFiles = (files: File[]) => {
    if (answeringRequest || files.length === 0) return;
    const existing = new Set(attachments.map((attachment) => attachment.id));
    const next: PendingArtifact[] = [];
    for (const file of files) {
      const id = `${file.name}:${file.size}:${file.lastModified}`;
      if (existing.has(id)) continue;
      if (file.size <= 0 || file.size > MAX_TURN_ARTIFACT_BYTES) {
        onError(`${file.name} must be between 1 byte and 25 MB`);
        continue;
      }
      if (attachments.length + next.length >= MAX_TURN_ARTIFACTS) {
        onError(`A Turn supports at most ${MAX_TURN_ARTIFACTS} attachments`);
        break;
      }
      existing.add(id);
      next.push({
        id,
        file,
        previewUrl: file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined,
      });
    }
    if (next.length > 0) setAttachments((current) => [...current, ...next]);
    if (sendStatus === "failed") setSendStatus("idle");
  };

  const removeAttachment = (id: string) => {
    setAttachments((current) => current.filter((attachment) => {
      if (attachment.id !== id) return true;
      if (attachment.previewUrl) URL.revokeObjectURL(attachment.previewUrl);
      return false;
    }));
  };

  const send = async () => {
    const draft = input;
    const text = draft.trim();
    const draftAttachments = attachments;
    if ((!text && draftAttachments.length === 0) || sendingRef.current) return;
    const request = answeringRequest;
    if (request && draftAttachments.length > 0) {
      onError("Attachments cannot be added while answering a Needs You request");
      return;
    }
    sendingRef.current = true;
    setSending(true);
    setSendingAttachmentCount(draftAttachments.length);
    setSendKind(request ? "answer" : "task");
    setSendStatus("sending");
    setInput("");
    setAttachments([]);
    try {
      if (request) {
        await api("POST", `/api/human-requests/${encodeURIComponent(request.id)}/answer`, { answer: text });
        setAnsweringRequest(null);
        await onHumanRequestChanged();
      } else {
        const uploaded: Array<{ id: string }> = [];
        for (const attachment of draftAttachments) {
          uploaded.push(await uploadThreadArtifact(agent.id, attachment.file));
        }
        await api("POST", `/api/agents/${agent.id}/turns`, { text, artifactIds: uploaded.map((artifact) => artifact.id) });
      }
      for (const attachment of draftAttachments) {
        if (attachment.previewUrl) URL.revokeObjectURL(attachment.previewUrl);
      }
      setSendStatus("sent");
      if (sendStatusTimerRef.current !== null) window.clearTimeout(sendStatusTimerRef.current);
      sendStatusTimerRef.current = window.setTimeout(() => setSendStatus("idle"), 2200);
    } catch (err: any) {
      setInput(draft);
      setAttachments(draftAttachments);
      setSendStatus("failed");
      onError(err.message);
    } finally {
      sendingRef.current = false;
      setSending(false);
      setSendingAttachmentCount(0);
    }
  };

  const interrupt = async () => {
    try {
      await api("POST", `/api/agents/${agent.id}/turns/current/interrupt`);
    } catch (err: any) {
      onError(err.message);
    }
  };

  const continueHeldMessage = async (entry: InboxEntry) => {
    const messageID = entry.internalMessage?.id;
    if (!messageID || heldActionID) return;
    setHeldActionID(messageID);
    try {
      await api("POST", `/api/comms/messages/${encodeURIComponent(messageID)}/retry`, {});
      await onPendingWorkChanged();
    } catch (err: any) {
      onError(err.message);
    } finally {
      setHeldActionID("");
    }
  };

  const closeHeldMessage = async (entry: InboxEntry) => {
    const messageID = entry.internalMessage?.id;
    if (!messageID || heldActionID) return;
    setHeldActionID(messageID);
    try {
      await api("POST", `/api/comms/messages/${encodeURIComponent(messageID)}/no-reply`, { from: agent.name });
      await onPendingWorkChanged();
    } catch (err: any) {
      onError(err.message);
    } finally {
      setHeldActionID("");
    }
  };

  const updateGoal = async (body: Record<string, unknown>) => {
    const data = await api("PUT", `/api/agents/${agent.id}/goal`, body);
    onAgentUpdated({ ...agent, goal: data.goal as ThreadGoal });
    return data.goal as ThreadGoal;
  };

  const clearGoal = async () => {
    await api("DELETE", `/api/agents/${agent.id}/goal`);
    onAgentUpdated({ ...agent, goal: undefined });
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

  const running = agent.status === "running";
  const heldMessages = pendingWork.filter((entry) => entry.internalMessage?.handlingStatus === "interrupted" || entry.internalMessage?.handlingStatus === "failed");
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
  threadStateRef.current = {
    agentId: agent.id,
    agentName: agent.name,
    goalStatus: agent.goal?.status || null,
    goalObjective: agent.goal?.objective || null,
    heldCount: heldMessages.length,
    heldMessageIds: heldMessages.map((entry) => entry.internalMessage!.id),
    feedBlockCount: feed.blocks.length,
    renderedFeedRowCount: feedVirtualizer.getVirtualItems().length,
    feedTotalSize: Math.round(feedVirtualizer.getTotalSize()),
    initialBottomPending: needsInitialBottomRef.current,
    pinnedToBottom: stickRef.current,
    savedScrollTop: Math.round(savedScrollTopRef.current),
    savedScrollAnchor: savedScrollAnchorRef.current,
    restoringScroll: restoringScrollRef.current,
    atBottom: !showJumpToBottom,
    distanceFromBottom: feedRef.current
      ? Math.max(0, feedRef.current.scrollHeight - feedRef.current.scrollTop - feedRef.current.clientHeight)
      : 0,
  };
  useEffect(() => {
    if (!active) return;
    const root = ((((window as any).codexLoom ||= (window as any).codexHub || {}) as Record<string, any>));
    (window as any).codexHub = root;
    const automation = {
      state: () => membershipStateRef.current,
      select: async (id: string) => {
        const membership = memberships.find((value) => value.id === id);
        if (!membership) throw new Error(`conversation membership not found: ${id}`);
        setConfigOpen(true);
        setConfigSection("external");
        editMembership(membership);
        await new Promise((resolve) => setTimeout(resolve, 0));
        return { ...membershipStateRef.current, selectedMembershipId: id };
      },
      refresh: async () => {
        await refreshConnections();
        return membershipStateRef.current;
      },
    };
    root.conversations = automation;
    return () => {
      if (root.conversations === automation) delete root.conversations;
    };
  }, [active, agent.id, memberships]);

  useEffect(() => {
    if (!active) return;
    const root = ((((window as any).codexLoom ||= (window as any).codexHub || {}) as Record<string, any>));
    (window as any).codexHub = root;
    const automation = {
      state: () => threadStateRef.current,
      jumpToBottom: async () => {
        jumpToBottom();
        await new Promise((resolve) => window.setTimeout(resolve, 0));
        return threadStateRef.current;
      },
    };
    root.thread = automation;
    return () => {
      if (root.thread === automation) delete root.thread;
    };
  }, [active, agent.id]);

  return (
    <main className="relative flex w-full min-w-0 max-w-full flex-1 flex-col overflow-hidden bg-background">
      {configOpen && (
        <div className="fixed inset-0 z-30 w-full overflow-y-auto bg-card p-3 shadow-card md:inset-auto md:right-4 md:top-11 md:max-h-[calc(100vh-3.25rem)] md:w-[560px] md:max-w-[calc(100vw-1rem)] md:rounded-md md:border md:border-border">
              <div className="mb-2 flex items-center justify-between text-[11px] font-semibold uppercase text-muted-foreground">
                <span>Agent Inspector</span>
                <button onClick={() => setConfigOpen(false)} className="flex size-7 items-center justify-center rounded-sm hover:bg-muted hover:text-foreground" title="Close agent config" aria-label="Close agent config"><X className="size-3.5" /></button>
              </div>
              <div className="mb-3 flex overflow-x-auto rounded-md bg-muted/60 p-1 text-[11px] font-medium [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
                <button onClick={() => setConfigSection("profile")} className={`h-7 rounded px-3 ${configSection === "profile" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}>Profile</button>
                <button onClick={() => setConfigSection("team")} className={`h-7 rounded px-3 ${configSection === "team" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}>Team</button>
                <button onClick={() => setConfigSection("external")} className={`h-7 rounded px-3 ${configSection === "external" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}>External</button>
                <button onClick={() => setConfigSection("schedules")} className={`h-7 rounded px-3 ${configSection === "schedules" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}>Schedules</button>
                <button onClick={() => setConfigSection("runtime")} className={`h-7 rounded px-3 ${configSection === "runtime" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}>Runtime</button>
                <button onClick={() => setConfigSection("usage")} className={`h-7 rounded px-3 ${configSection === "usage" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}>Usage</button>
              </div>

              {configSection === "runtime" ? (
                <>
                  <label className="mb-2 block">
                    <span className="mb-1 block text-[11px] text-muted-foreground">Name</span>
                    <input
                      value={nameDraft}
                      onChange={(e) => setNameDraft(e.target.value)}
                      disabled={running}
                      placeholder="agent-name"
                      spellCheck={false}
                      className="h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition placeholder:text-muted-foreground/60 focus:ring-ring/25 disabled:opacity-60"
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
                      className="h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition placeholder:text-muted-foreground/60 focus:ring-ring/25 disabled:opacity-60"
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
                        className="mt-2 h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition placeholder:text-muted-foreground/60 focus:ring-ring/25 disabled:opacity-60"
                      />
                    )}
                  </label>
                  <label className="mb-2 block">
                    <span className="mb-1 block text-[11px] text-muted-foreground">Thinking Effort</span>
                    <select value={effortDraft} onChange={(e) => setEffortDraft(e.target.value)} disabled={running} className="h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition focus:ring-ring/25 disabled:opacity-60">
                      <option value="">default</option><option value="minimal">minimal</option><option value="low">low</option><option value="medium">medium</option><option value="high">high</option><option value="xhigh">extra high</option>
                    </select>
                  </label>
                  <label className="mb-2 block">
                    <span className="mb-1 block text-[11px] text-muted-foreground">Sandbox</span>
                    <select value={sandboxDraft} onChange={(e) => setSandboxDraft(e.target.value)} disabled={running} className="h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition focus:ring-ring/25 disabled:opacity-60">
                      <option value="danger-full-access">danger-full-access</option><option value="workspace-write">workspace-write</option><option value="read-only">read-only</option>
                    </select>
                  </label>
                  <label className="mb-3 block">
                    <span className="mb-1 block text-[11px] text-muted-foreground">Approval Policy</span>
                    <select value={approvalDraft} onChange={(e) => setApprovalDraft(e.target.value)} disabled={running} className="h-8 w-full rounded-md bg-background px-2.5 font-mono text-[12px] outline-none ring-1 ring-border transition focus:ring-ring/25 disabled:opacity-60">
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
              ) : configSection === "team" ? (
                <AgentTeamPanel agent={agent} team={team} />
              ) : configSection === "schedules" ? (
                <AgentSchedulesPanel schedules={schedules} />
              ) : configSection === "usage" ? (
                <AgentUsagePanel usage={usage} loading={loadingUsage} onOpenOverview={() => onOpenUsage(agent.id)} onRefresh={() => {
                  setLoadingUsage(true);
                  api("GET", `/api/agents/${agent.id}/usage?days=7`)
                    .then((data) => setUsage(data.usage))
                    .catch((err: Error) => onError(err.message))
                    .finally(() => setLoadingUsage(false));
                }} />
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

      {/* event feed + pending approvals */}
      <div className="relative min-h-0 flex-1 overflow-hidden">
        <div ref={feedRef} onScroll={onScroll} className="absolute inset-0 overflow-y-auto">
          <div className="mx-auto max-w-[880px] px-3 pb-8 pt-3 md:px-6 md:pt-5">
            <div className="relative w-full" style={{ height: `${feedVirtualizer.getTotalSize()}px` }}>
              {active ? feedVirtualizer.getVirtualItems().map((virtualRow) => {
                const row = feedRows[virtualRow.index];
                if (!row) return null;
                return (
                  <div
                    key={virtualRow.key}
                    data-index={virtualRow.index}
                    ref={measureFeedRow}
                    className="absolute left-0 top-0 flow-root w-full py-px"
                    style={{ transform: `translateY(${virtualRow.start}px)` }}
                  >
                    {row.kind === "approval" ? (
                      <div className="my-1 rounded-md border border-warning/30 bg-warning/5 px-4 py-3 shadow-card">
                        <div className="mb-2 text-sm">
                          <span className="text-warning">⚠</span> <b>codex requests approval</b> —{" "}
                          <span className="font-mono text-[12px]">{row.approval.method}</span>
                        </div>
                        <pre className="mb-3 max-h-40 overflow-auto whitespace-pre-wrap rounded-md bg-muted/50 px-3 py-2 font-mono text-[12px] text-muted-foreground">
                          {JSON.stringify(row.approval.params, null, 2)}
                        </pre>
                        <button
                          onClick={() => resolveApproval(row.id, "accept")}
                          className="mr-2 rounded-md bg-primary px-3.5 py-1.5 text-[13px] font-medium text-primary-foreground transition-colors hover:opacity-90"
                        >
                          approve
                        </button>
                        <button
                          onClick={() => resolveApproval(row.id, "reject")}
                          className="rounded-md px-3.5 py-1.5 text-[13px] text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
                        >
                          reject
                        </button>
                      </div>
                    ) : (
                      <BlockView block={row.block} />
                    )}
                  </div>
                );
              }) : null}
            </div>
          </div>
        </div>
        {active && showJumpToBottom && (
          <button
            type="button"
            onClick={jumpToBottom}
            title="Jump to latest"
            aria-label="Jump to latest"
            className="absolute bottom-3 left-1/2 z-10 flex size-9 -translate-x-1/2 items-center justify-center rounded-md border border-border bg-card text-foreground shadow-card transition hover:bg-muted active:translate-y-px"
          >
            <ArrowDown className="size-4" />
          </button>
        )}
      </div>

      <div className="relative shrink-0 border-t border-border bg-background px-3 py-2 pb-[max(0.5rem,env(safe-area-inset-bottom))] md:px-6 md:py-3 md:pb-3">
        <div className="mx-auto max-w-[880px]">
          {agent.lastError && !running ? (
            <div className="mb-2 border-l-2 border-destructive bg-destructive/5 px-3 py-2 text-[11.5px] text-destructive" role="alert">
              <span className="font-semibold">Last turn failed:</span>{" "}
              <span className="break-words">{readableRuntimeError(agent.lastError)}</span>
            </div>
          ) : null}
          <GoalBar goal={agent.goal} onUpdate={updateGoal} onClear={clearGoal} onError={onError} />
          <NeedsYouBar entries={humanRequests} onAnswer={(request, value = "") => {
            for (const attachment of attachments) {
              if (attachment.previewUrl) URL.revokeObjectURL(attachment.previewUrl);
            }
            setAttachments([]);
            setAnsweringRequest(request);
            setInput(value);
          }} onOpen={onOpenHumanRequest} />
          <HeldWorkBar entries={heldMessages} workingID={heldActionID} onContinue={continueHeldMessage} onOpen={onOpenPendingWork} onClose={closeHeldMessage} />
          <PendingWorkBar entries={pendingWork} onOpen={onOpenPendingWork} />
          <div
            className={cn(
              "flex flex-col gap-1.5 rounded-md border border-input bg-card p-1.5 shadow-card transition focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/20",
              draggingAttachment && "border-primary bg-primary/[0.03] ring-2 ring-primary/20",
            )}
            onDragEnter={(event) => {
              if (!answeringRequest && event.dataTransfer.types.includes("Files")) {
                event.preventDefault();
                setDraggingAttachment(true);
              }
            }}
            onDragOver={(event) => {
              if (!answeringRequest && event.dataTransfer.types.includes("Files")) event.preventDefault();
            }}
            onDragLeave={(event) => {
              if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDraggingAttachment(false);
            }}
            onDrop={(event) => {
              event.preventDefault();
              setDraggingAttachment(false);
              addAttachmentFiles(Array.from(event.dataTransfer.files));
            }}
          >
            {answeringRequest ? (
              <div className="flex min-w-0 items-start gap-2 border-b border-border px-2 py-1.5">
                <CircleHelp className="mt-0.5 size-3.5 shrink-0 text-warning" />
                <div className="min-w-0 flex-1">
                  <div className="font-mono text-[8.5px] uppercase text-warning">Answering request</div>
                  <div className="mt-0.5 truncate text-[10.5px] text-foreground" title={answeringRequest.question}>{answeringRequest.question}</div>
                </div>
                <button type="button" onClick={() => { setAnsweringRequest(null); setInput(""); }} className="flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-muted" aria-label="Cancel answering mode"><X className="size-3" /></button>
              </div>
            ) : null}
            {attachments.length > 0 ? (
              <div className="grid grid-cols-1 gap-1.5 border-b border-border px-1 pb-2 sm:grid-cols-2">
                {attachments.map((attachment) => (
                  <div key={attachment.id} className="flex min-w-0 items-center gap-2 rounded-sm border border-border/80 bg-background/70 p-1.5">
                    {attachment.previewUrl ? (
                      <img src={attachment.previewUrl} alt="" className="size-10 shrink-0 rounded-sm border border-border object-cover" />
                    ) : (
                      <span className="flex size-10 shrink-0 items-center justify-center rounded-sm border border-border bg-muted/40 text-muted-foreground"><FileText className="size-4" /></span>
                    )}
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-[11px] font-medium text-foreground" title={attachment.file.name}>{attachment.file.name}</span>
                      <span className="block font-mono text-[9px] text-muted-foreground">{formatAttachmentSize(attachment.file.size)}</span>
                    </span>
                    <button type="button" onClick={() => removeAttachment(attachment.id)} disabled={sending} className="flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-40" aria-label={`Remove ${attachment.file.name}`} title="Remove attachment"><X className="size-3" /></button>
                  </div>
                ))}
              </div>
            ) : null}
            <textarea
              rows={1}
              value={input}
              disabled={sending}
              aria-label="task message"
              onChange={(e) => {
                setInput(e.target.value);
                if (sendStatus === "failed") setSendStatus("idle");
              }}
              onPaste={(event) => {
                const files = Array.from(event.clipboardData.files);
                if (files.length > 0 && !answeringRequest) {
                  event.preventDefault();
                  addAttachmentFiles(files);
                }
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing && !sending) {
                  e.preventDefault();
                  send();
                }
              }}
              placeholder={answeringRequest ? `Answer ${agent.name}…` : `Send a task to ${agent.name}…`}
              className="max-h-[200px] min-h-10 resize-none overflow-y-auto bg-transparent px-2.5 py-2 text-sm leading-6 outline-none placeholder:text-muted-foreground/60 disabled:cursor-wait disabled:opacity-60"
            />
            <div className="flex min-h-8 items-center justify-between gap-2 px-1">
              <div className="flex min-w-0 items-center gap-1">
                <input
                  ref={fileInputRef}
                  type="file"
                  multiple
                  className="hidden"
                  onChange={(event) => {
                    addAttachmentFiles(Array.from(event.target.files || []));
                    event.target.value = "";
                  }}
                />
                <button type="button" onClick={() => fileInputRef.current?.click()} disabled={sending || !!answeringRequest || attachments.length >= MAX_TURN_ARTIFACTS} className="flex size-8 shrink-0 items-center justify-center rounded-sm text-muted-foreground transition hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-35" aria-label="Attach files" title={answeringRequest ? "Attachments are unavailable while answering a request" : "Attach files or images"}><Paperclip className="size-4" /></button>
                <div className="min-w-0 truncate font-mono text-[10px] text-muted-foreground" aria-live="polite">
                {sendStatus === "sending" && <span className="inline-flex items-center gap-1.5"><Loader2 className="size-3 animate-spin" />{sendKind === "answer" ? "Submitting answer" : sendingAttachmentCount > 0 ? `Uploading ${sendingAttachmentCount} attachment${sendingAttachmentCount === 1 ? "" : "s"}` : "Sending to thread"}</span>}
                {sendStatus === "sent" && <span className="inline-flex items-center gap-1.5 text-success"><Check className="size-3" />{sendKind === "answer" ? "Answer queued" : "Sent to thread"}</span>}
                {sendStatus === "failed" && <span className="text-destructive">Send failed · draft restored</span>}
                {sendStatus === "idle" && <span className="hidden sm:inline">Enter to send · Shift+Enter for new line</span>}
                </div>
              </div>
              {running && !answeringRequest ? (
                <button
                  onClick={interrupt}
                  className="shadow-action flex size-9 shrink-0 cursor-pointer items-center justify-center rounded-md bg-primary text-primary-foreground transition-all duration-150 hover:bg-primary/90"
                  aria-label="stop current turn"
                >
                  <Square className="size-4" />
                </button>
              ) : (
                <button
                  onClick={send}
                  disabled={sending || (!input.trim() && attachments.length === 0)}
                  aria-label={sending ? (answeringRequest ? "submitting answer" : "sending task") : (answeringRequest ? "submit answer" : "send task")}
                  className={cn(
                    "flex size-9 shrink-0 items-center justify-center rounded-md transition-all duration-150",
                    sending
                      ? "cursor-wait bg-primary text-primary-foreground"
                    : input.trim() || attachments.length > 0
                      ? "shadow-action cursor-pointer bg-primary text-primary-foreground hover:bg-primary/90"
                      : "cursor-not-allowed bg-muted text-muted-foreground/40",
                  )}
                >
                  {sending ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
                </button>
              )}
            </div>
          </div>
        </div>
      </div>
    </main>
  );
}

function GoalBar({
  goal,
  onUpdate,
  onClear,
  onError,
}: {
  goal?: ThreadGoal;
  onUpdate: (body: Record<string, unknown>) => Promise<ThreadGoal>;
  onClear: () => Promise<void>;
  onError: (message: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [objective, setObjective] = useState(goal?.objective || "");
  const [budget, setBudget] = useState(goal?.tokenBudget ? String(goal.tokenBudget) : "");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setObjective(goal?.objective || "");
    setBudget(goal?.tokenBudget ? String(goal.tokenBudget) : "");
  }, [goal?.objective, goal?.tokenBudget, goal?.updatedAt]);

  const save = async () => {
    const nextObjective = objective.trim();
    if (!nextObjective) {
      onError("Goal objective is required");
      return;
    }
    const body: Record<string, unknown> = { objective: nextObjective };
    if (!goal) body.status = "active";
    if (budget.trim()) {
      const value = Number(budget);
      if (!Number.isSafeInteger(value) || value <= 0) {
        onError("Goal token budget must be a positive integer");
        return;
      }
      body.tokenBudget = value;
    } else if (goal?.tokenBudget != null) {
      body.clearTokenBudget = true;
    }
    setSaving(true);
    try {
      await onUpdate(body);
      setOpen(false);
    } catch (error: any) {
      onError(error.message);
    } finally {
      setSaving(false);
    }
  };

  const setStatus = async (status: "active" | "paused") => {
    setSaving(true);
    try {
      await onUpdate({ status });
      setOpen(false);
    } catch (error: any) {
      onError(error.message);
    } finally {
      setSaving(false);
    }
  };

  const clear = async () => {
    if (!window.confirm("Clear this Goal? Its Codex Goal state and automatic continuation will be removed.")) return;
    setSaving(true);
    try {
      await onClear();
      setOpen(false);
    } catch (error: any) {
      onError(error.message);
    } finally {
      setSaving(false);
    }
  };

  const statusLabel = goal ? goalStatusLabel(goal.status) : "No active Goal";
  const usage = goal
    ? `${compactTokens(goal.tokensUsed)}${goal.tokenBudget ? ` / ${compactTokens(goal.tokenBudget)}` : ""} tokens · ${formatGoalDuration(goal.timeUsedSeconds)}`
    : "Start long-running work";

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        className={cn(
          "mb-1 flex h-8 w-full min-w-0 items-center gap-2 rounded-sm px-2 text-left outline-none transition-colors hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring/30",
          goal?.status === "active" ? "bg-primary/[0.035]" : "",
        )}
        aria-label={goal ? `Open ${statusLabel}` : "Set a Goal"}
      >
        <Target className={cn("size-3.5 shrink-0", goal?.status === "active" ? "text-primary" : "text-muted-foreground")} />
        <span className="shrink-0 text-[11px] font-semibold text-foreground">Goal</span>
        {goal ? <span className={cn("shrink-0 rounded-sm px-1.5 py-0.5 font-mono text-[8.5px] uppercase", goalStatusClass(goal.status))}>{statusLabel}</span> : null}
        <span className="min-w-0 flex-1 truncate text-[10.5px] text-muted-foreground">{goal?.objective || usage}</span>
        {goal ? <span className="hidden shrink-0 font-mono text-[8.5px] text-muted-foreground sm:inline">{usage}</span> : null}
        <ChevronRight className="size-3.5 shrink-0 text-muted-foreground/60" />
      </PopoverTrigger>
      <PopoverContent side="top" align="start" className="w-[min(36rem,calc(100vw-1rem))] p-3" aria-label="Goal controls">
        <div className="flex items-center gap-2 border-b border-border pb-2">
          <Target className="size-4 text-primary" />
          <div className="min-w-0 flex-1">
            <div className="text-[12px] font-semibold">Thread Goal</div>
            <div className="font-mono text-[9px] text-muted-foreground">{goal ? `${statusLabel} · ${usage}` : "Codex native Goal"}</div>
          </div>
          {goal ? <span className={cn("rounded-sm px-1.5 py-0.5 font-mono text-[8.5px] uppercase", goalStatusClass(goal.status))}>{statusLabel}</span> : null}
        </div>
        {goal?.status === "complete" ? (
          <div className="py-3 text-[11px] leading-5 text-muted-foreground">{goal.objective}</div>
        ) : (
          <>
            <label className="mt-3 block">
              <span className="mb-1 block text-[9px] font-semibold uppercase text-muted-foreground">Objective</span>
              <textarea value={objective} onChange={(event) => setObjective(event.target.value)} rows={4} maxLength={4000} disabled={saving} autoFocus={!goal} className="w-full resize-y rounded-md bg-background p-2.5 text-[12px] leading-5 outline-none ring-1 ring-border focus:ring-ring/30 disabled:opacity-60" />
            </label>
            <label className="mt-2 block">
              <span className="mb-1 block text-[9px] font-semibold uppercase text-muted-foreground">Token budget</span>
              <input value={budget} onChange={(event) => setBudget(event.target.value.replace(/[^0-9]/g, ""))} inputMode="numeric" placeholder="No limit" disabled={saving} className="h-8 w-full rounded-md bg-background px-2.5 font-mono text-[11px] outline-none ring-1 ring-border focus:ring-ring/30 disabled:opacity-60" />
            </label>
          </>
        )}
        <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-border pt-2">
          <div className="flex items-center gap-1">
            {goal?.status === "active" ? <button type="button" onClick={() => setStatus("paused")} disabled={saving} className="flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-[10.5px] hover:bg-muted disabled:opacity-50"><Pause className="size-3" />Pause</button> : null}
            {goal && !["active", "complete"].includes(goal.status) ? <button type="button" onClick={() => setStatus("active")} disabled={saving} className="flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-[10.5px] hover:bg-muted disabled:opacity-50"><Play className="size-3" />Resume</button> : null}
            {goal ? <button type="button" onClick={clear} disabled={saving} title="Clear Goal" aria-label="Clear Goal" className="flex size-8 items-center justify-center rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:opacity-50"><Trash2 className="size-3.5" /></button> : null}
          </div>
          {goal?.status !== "complete" ? <button type="button" onClick={save} disabled={saving || !objective.trim()} className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[10.5px] font-medium text-primary-foreground disabled:opacity-50">{saving ? <Loader2 className="size-3 animate-spin" /> : <Check className="size-3" />}{goal ? "Save Goal" : "Start Goal"}</button> : null}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function goalStatusLabel(status: ThreadGoal["status"]) {
  switch (status) {
    case "usageLimited": return "Usage limited";
    case "budgetLimited": return "Budget limited";
    default: return status;
  }
}

function goalStatusClass(status: ThreadGoal["status"]) {
  if (status === "active") return "bg-success/10 text-success";
  if (status === "complete") return "bg-muted text-muted-foreground";
  if (status === "blocked" || status === "usageLimited" || status === "budgetLimited") return "bg-warning/10 text-warning";
  return "bg-primary/10 text-primary";
}

function formatGoalDuration(seconds: number) {
  if (seconds >= 3600) return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
  if (seconds >= 60) return `${Math.floor(seconds / 60)}m`;
  return `${Math.max(0, Math.floor(seconds || 0))}s`;
}

function NeedsYouBar({ entries, onAnswer, onOpen }: { entries: HumanRequest[]; onAnswer: (request: HumanRequest, value?: string) => void; onOpen: (requestID?: string) => void }) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (entries.length === 0) setOpen(false);
  }, [entries.length]);
  if (entries.length === 0) return null;
  const requiredCount = entries.filter((entry) => entry.expectation === "required").length;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        className="mb-1 flex h-8 w-full min-w-0 items-center gap-2 rounded-sm bg-warning/5 px-2 text-left text-[11px] text-muted-foreground outline-none transition-colors hover:bg-warning/10 focus-visible:ring-2 focus-visible:ring-ring/30"
        aria-label={`Show ${entries.length} requests that need your input`}
      >
        <CircleHelp className="size-3.5 shrink-0 text-warning" />
        <span className="font-semibold text-foreground">Needs your input</span>
        <span className="flex size-5 shrink-0 items-center justify-center rounded-sm bg-warning/15 font-mono text-[10px] font-semibold text-warning">{entries.length}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-[9.5px]">{requiredCount > 0 ? `${requiredCount} required` : "Optional feedback"}</span>
        <ChevronRight className="size-3.5 shrink-0 text-muted-foreground/60" />
      </PopoverTrigger>
      <PopoverContent side="top" align="start" className="w-[min(34rem,calc(100vw-1rem))] p-2" aria-label="Requests needing your input">
        <div className="flex items-center justify-between px-1 py-1">
          <span className="text-[11.5px] font-semibold">Requests from {entries[0]?.agentName}</span>
          <button type="button" onClick={() => { setOpen(false); onOpen(); }} className="rounded-sm px-2 py-1 font-mono text-[9px] text-muted-foreground hover:bg-muted hover:text-foreground">Open workspace</button>
        </div>
        <div className="mt-1 max-h-72 overflow-y-auto">
          {entries.map((request) => (
            <div key={request.id} className="flex min-w-0 items-start gap-2 border-t border-border/70 px-2 py-2.5 first:border-t-0">
              <span className={`mt-1 size-2 shrink-0 rounded-full ${request.expectation === "required" ? "bg-warning" : "bg-ring/70"}`} />
              <div className="min-w-0 flex-1">
                <div className="text-[11.5px] font-semibold leading-4">{request.question}</div>
                {request.blockedWork ? <div className="mt-1 line-clamp-2 text-[10px] leading-4 text-muted-foreground">Waiting: {request.blockedWork}</div> : null}
                {request.options?.length ? (
                  <div className="mt-2 flex flex-wrap gap-1">
                    {request.options.map((option) => <button key={option.label} type="button" onClick={() => { setOpen(false); onAnswer(request, option.label); }} className="rounded-sm border border-border bg-background px-2 py-1 text-[9.5px] text-foreground hover:border-ring/50 hover:bg-selection">{option.label}</button>)}
                  </div>
                ) : null}
                <div className="mt-2 flex items-center gap-1.5">
                  <button type="button" onClick={() => { setOpen(false); onAnswer(request); }} className="rounded-sm bg-primary px-2.5 py-1 text-[10px] font-medium text-primary-foreground">Answer here</button>
                  <button type="button" onClick={() => { setOpen(false); onOpen(request.id); }} className="rounded-sm px-2.5 py-1 text-[10px] text-muted-foreground hover:bg-muted hover:text-foreground">View context</button>
                </div>
              </div>
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function HeldWorkBar({
  entries,
  workingID,
  onContinue,
  onOpen,
  onClose,
}: {
  entries: InboxEntry[];
  workingID: string;
  onContinue: (entry: InboxEntry) => void;
  onOpen: (itemID: string) => void;
  onClose: (entry: InboxEntry) => void;
}) {
  if (entries.length === 0) return null;
  const entry = entries[0];
  const message = entry.internalMessage!;
  const working = workingID === message.id;
  const failed = message.handlingStatus === "failed";
  return (
    <div className="mb-1.5 flex min-w-0 flex-wrap items-center gap-2 border-l-2 border-warning bg-warning/5 px-2.5 py-2">
      <Square className="size-3 shrink-0 fill-warning/15 text-warning" />
      <div className="min-w-0 flex-1 basis-48">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="shrink-0 font-mono text-[8.5px] font-semibold uppercase text-warning">{failed ? "Failed" : "Stopped"}</span>
          <span className="truncate text-[11px] font-semibold text-foreground" title={message.subject}>{message.subject}</span>
          {entries.length > 1 ? <span className="shrink-0 font-mono text-[8.5px] text-muted-foreground">+{entries.length - 1}</span> : null}
        </div>
        <div className="mt-0.5 truncate text-[10px] text-muted-foreground" title={message.lastHandlingError || "Held until explicitly continued"}>
          {failed ? "Held after a failed handling attempt" : "Held after interruption"} · it will not restart automatically
        </div>
      </div>
      <div className="flex basis-full shrink-0 items-center justify-start gap-1 sm:basis-auto sm:justify-end">
        <button type="button" onClick={() => onContinue(entry)} disabled={Boolean(workingID)} className="flex h-7 items-center gap-1 rounded-sm bg-primary px-2 text-[10px] font-medium text-primary-foreground disabled:opacity-50">
          {working ? <Loader2 className="size-3 animate-spin" /> : <RotateCcw className="size-3" />}Continue
        </button>
        <button type="button" onClick={() => onOpen(entry.item.id)} title="Open message" aria-label="Open held message" className="flex size-7 items-center justify-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground">
          <MessageSquare className="size-3.5" />
        </button>
        <button type="button" onClick={() => onClose(entry)} disabled={Boolean(workingID)} title="Close without reply" aria-label="Close held message without reply" className="flex size-7 items-center justify-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">
          <SkipForward className="size-3.5" />
        </button>
      </div>
    </div>
  );
}

function PendingWorkBar({ entries, onOpen }: { entries: InboxEntry[]; onOpen: (itemID: string) => void }) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (entries.length === 0) setOpen(false);
  }, [entries.length]);
  if (entries.length === 0) return null;
  const internalCount = entries.filter(isInternalWork).length;
  const externalCount = entries.length - internalCount;
  const failedCount = entries.filter((entry) => entry.item.state === "failed" || entry.item.state === "pending_access").length;
  const handlingCount = entries.filter((entry) => entry.item.state === "handling" || entry.item.state === "awaiting_delivery").length;
  const heldCount = entries.filter((entry) => entry.item.state === "interrupted").length;
  const oldest = oldestWaitingMs(entries);
  const summary = [
    externalCount > 0 ? `${externalCount} external` : "",
    internalCount > 0 ? `${internalCount} agent msg` : "",
    handlingCount > 0 ? `${handlingCount} handling` : "",
    heldCount > 0 ? `${heldCount} held` : "",
  ].filter(Boolean).join(" · ");

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        className="mb-1.5 flex h-8 w-full min-w-0 items-center gap-2 rounded-sm px-2 text-left text-[11px] text-muted-foreground outline-none transition-colors hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring/30"
        aria-label={`Show ${entries.length} pending work items`}
      >
        <Inbox className="size-3.5 shrink-0 text-primary" />
        <span className="font-semibold text-foreground">Agent Inbox</span>
        <span className="flex size-5 shrink-0 items-center justify-center rounded-sm bg-muted font-mono text-[10px] font-semibold text-foreground">{entries.length}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-[9.5px]">{summary}{oldest ? ` · oldest ${formatQueueAge(oldest)}` : ""}</span>
        {failedCount > 0 && <span className="shrink-0 font-mono text-[9.5px] text-destructive">{failedCount} attention</span>}
        <ChevronRight className="size-3.5 shrink-0 text-muted-foreground/60" />
      </PopoverTrigger>
      <PopoverContent side="top" align="start" className="w-[min(32rem,calc(100vw-1rem))] p-2" aria-label="Agent Inbox">
        <div className="flex items-center justify-between px-1 py-1">
          <span className="text-[11.5px] font-semibold">Agent Inbox</span>
          <span className="font-mono text-[9.5px] text-muted-foreground">{entries.length} open</span>
        </div>
        <div className="mt-1 max-h-72 overflow-y-auto">
          {entries.map((entry) => {
            const internal = isInternalWork(entry);
            const subject = String(entry.message.providerMetadata?.subject || "");
            const sender = entry.message.sender.displayName || entry.message.sender.externalId || "Unknown sender";
            const source = internal ? "Agent message" : entry.message.origin;
            return (
              <button
                key={entry.item.id}
                type="button"
                onClick={() => {
                  setOpen(false);
                  onOpen(entry.item.id);
                }}
                className="flex w-full min-w-0 items-start gap-2 rounded-sm px-2 py-2 text-left outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/30"
              >
                {internal ? <MessageSquare className="mt-0.5 size-3.5 shrink-0 text-primary" /> : <Inbox className="mt-0.5 size-3.5 shrink-0 text-primary" />}
                <span className="min-w-0 flex-1">
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span className="truncate text-[11.5px] font-semibold">{subject || sender}</span>
                    <span className="shrink-0 font-mono text-[8.5px] uppercase text-muted-foreground">{source}</span>
                  </span>
                  <span className="mt-0.5 block truncate text-[10.5px] text-muted-foreground">
                    {subject ? `${sender} · ` : ""}{entry.message.content.text || "Attachment"}
                  </span>
                </span>
                <span className={`shrink-0 font-mono text-[8.5px] uppercase ${entry.item.state === "failed" || entry.item.state === "pending_access" ? "text-destructive" : entry.item.state === "handling" || entry.item.state === "awaiting_delivery" || entry.item.state === "interrupted" ? "text-warning" : "text-muted-foreground"}`}>{entry.item.state === "interrupted" ? "held" : entry.item.state.replaceAll("_", " ")}</span>
              </button>
            );
          })}
        </div>
      </PopoverContent>
    </Popover>
  );
}

function formatQueueAge(milliseconds: number) {
  const minutes = Math.max(1, Math.floor(milliseconds / 60_000));
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

function isInternalWork(entry: InboxEntry) {
  return entry.message.origin === "loom" || entry.message.origin === "chub";
}

function AgentTeamPanel({ agent, team }: { agent: Agent; team: TeamView | null }) {
  if (!team) return <div className="flex items-center justify-center gap-2 py-10 font-mono text-[10px] text-muted-foreground"><Loader2 className="size-3 animate-spin" />Reading Team relationships</div>;
  const organization = team.organizationLinks.filter((link) => link.parentAgentId === agent.id || link.childAgentId === agent.id);
  const collaboration = team.collaborationLinks.filter((link) => link.fromAgentId === agent.id || link.toAgentId === agent.id);
  return <div>
    <div className="mb-3 flex items-center gap-2"><Network className="size-3.5 text-primary" /><span className="text-[12px] font-medium">Team relationships</span><span className="ml-auto font-mono text-[9.5px] text-muted-foreground">{organization.length + collaboration.length} declared</span></div>
    <div className="divide-y divide-border border-y border-border">
      {organization.map((link) => <div key={link.id} className="py-3"><div className="flex items-center gap-2 text-[11.5px] font-semibold"><span className="font-mono text-[8.5px] uppercase text-muted-foreground">Organization</span>{link.parentAgentId === agent.id ? `Internal Agent: ${link.child}` : `Parent: ${link.parent}`}</div><div className="mt-1 text-[10.5px] leading-4 text-muted-foreground">{link.description}</div></div>)}
      {collaboration.map((link) => <div key={link.id} className="py-3"><div className="flex items-center gap-2 text-[11.5px] font-semibold"><span className="font-mono text-[8.5px] uppercase text-muted-foreground">Collaboration</span>{link.fromAgentId === agent.id ? link.to : link.from}</div><div className="mt-1 text-[10.5px] leading-4 text-muted-foreground">{link.description}</div></div>)}
      {organization.length === 0 && collaboration.length === 0 ? <div className="py-8 text-center text-[11px] text-muted-foreground">No declared Team relationships.</div> : null}
    </div>
  </div>;
}

function AgentSchedulesPanel({ schedules }: { schedules: Schedule[] }) {
  return <div>
    <div className="mb-3 flex items-center gap-2"><CalendarClock className="size-3.5 text-primary" /><span className="text-[12px] font-medium">Schedules</span><span className="ml-auto font-mono text-[9.5px] text-muted-foreground">{schedules.filter((schedule) => schedule.enabled).length} enabled</span></div>
    <div className="divide-y divide-border border-y border-border">
      {schedules.map((schedule) => <div key={schedule.id} className="flex min-w-0 items-start gap-3 py-3"><span className={`mt-1 size-2 shrink-0 rounded-full ${schedule.enabled ? "bg-success" : "bg-muted-foreground/35"}`} /><div className="min-w-0 flex-1"><div className="truncate text-[11.5px] font-semibold">{schedule.name}</div><div className="mt-0.5 truncate text-[10.5px] text-muted-foreground">{schedule.subject}</div><div className="mt-1 font-mono text-[9px] text-muted-foreground">{schedule.cron || schedule.at || "No trigger"}{schedule.nextRunAt ? ` · next ${new Date(schedule.nextRunAt).toLocaleString()}` : ""}</div></div></div>)}
      {schedules.length === 0 ? <div className="py-8 text-center text-[11px] text-muted-foreground">No schedules target this Agent.</div> : null}
    </div>
  </div>;
}

function AgentUsagePanel({ usage, loading, onRefresh, onOpenOverview }: { usage: AgentTokenUsage | null; loading: boolean; onRefresh: () => void; onOpenOverview: () => void }) {
  if (loading && !usage) {
    return <div className="flex items-center justify-center gap-2 py-10 font-mono text-[10px] text-muted-foreground"><Loader2 className="size-3 animate-spin" />Reading Thread usage</div>;
  }
  if (!usage?.available) {
    return (
      <div className="py-10 text-center">
        <BarChart3 className="mx-auto size-5 text-muted-foreground/45" />
        <div className="mt-2 text-[12px] font-medium">No Thread usage yet</div>
        <div className="mt-1 text-[10.5px] text-muted-foreground">Usage appears after this Agent completes a model call.</div>
        <button type="button" onClick={onOpenOverview} className="mx-auto mt-4 flex h-8 items-center gap-1.5 rounded-md border border-border px-3 text-[11px] font-medium text-foreground hover:bg-muted">Open Token usage <ArrowUpRight className="size-3" /></button>
      </div>
    );
  }
  const maxDaily = Math.max(1, ...usage.daily.map((day) => day.usage.totalTokens));
  return (
    <div>
      <div className="mb-3 flex items-center justify-between border-b border-border pb-2">
        <div>
          <div className="text-[12px] font-medium">Thread token usage</div>
          <div className="mt-0.5 font-mono text-[9px] text-muted-foreground">7 days · {usage.latestModel || "model unknown"}</div>
        </div>
        <button onClick={onRefresh} disabled={loading} className="flex size-7 items-center justify-center rounded-md border border-border text-muted-foreground hover:text-foreground" title="Refresh usage" aria-label="Refresh usage">
          <RefreshCw className={`size-3 ${loading ? "animate-spin" : ""}`} />
        </button>
      </div>

      <div className="grid grid-cols-3 divide-x divide-border border-y border-border">
        <AgentUsageMetric label="7 day" value={compactTokens(usage.period.totalTokens)} detail={`${compactTokens(usage.period.calls)} calls`} />
        <AgentUsageMetric label="Today" value={compactTokens(usage.today.totalTokens)} detail={`${compactTokens(usage.today.outputTokens)} output`} />
        <AgentUsageMetric label="Lifetime" value={compactTokens(usage.lifetime.totalTokens)} detail={`${compactTokens(usage.lifetime.calls)} calls`} />
      </div>

      <section className="border-b border-border py-4">
        <div className="flex items-end justify-between gap-3">
          <div>
            <div className="text-[10px] font-semibold uppercase text-muted-foreground">Current context</div>
            <div className="mt-1 font-mono text-[13px] font-semibold">{compactTokens(usage.context.inputTokens)} <span className="text-[9px] font-normal text-muted-foreground">/ {compactTokens(usage.context.windowTokens)}</span></div>
          </div>
          <div className="text-right font-mono text-[10px] text-muted-foreground">{usage.context.usedPercent.toFixed(0)}% used</div>
        </div>
        <div className="mt-2 h-1.5 bg-muted"><div className="h-full bg-[var(--loom-blue)]/70" style={{ width: `${usage.context.usedPercent}%` }} /></div>
        <div className="mt-2 flex justify-between font-mono text-[9px] text-muted-foreground">
          <span>{usage.cacheHitPercent.toFixed(0)}% cache hit</span>
          <span>{compactTokens(usage.period.cachedInputTokens)} cached input</span>
        </div>
      </section>

      <section className="border-b border-border py-4">
        <div className="mb-3 text-[10px] font-semibold uppercase text-muted-foreground">Daily activity</div>
        <div data-usage-chart="agent" className="flex h-20 items-end gap-1">
          {usage.daily.map((day, index) => (
            <div key={day.date} data-usage-date={day.date} className="group relative flex h-full min-w-0 flex-1 items-end outline-none" tabIndex={-1} aria-label={usageDayLabel(day)}>
              <UsageBarTooltip day={day} align={index === 0 ? "start" : index === usage.daily.length - 1 ? "end" : "center"} />
              <div className="relative w-full bg-muted" style={{ height: `${day.usage.totalTokens ? Math.max(4, (day.usage.totalTokens / maxDaily) * 100) : 2}%` }}>
                <div className="absolute inset-x-0 bottom-0 bg-[var(--loom-teal)]/65" style={{ height: `${day.usage.inputTokens ? (day.usage.cachedInputTokens / day.usage.inputTokens) * 100 : 0}%` }} />
              </div>
            </div>
          ))}
        </div>
        <div className="mt-1 flex justify-between font-mono text-[8.5px] text-muted-foreground"><span>{usage.daily[0]?.date.slice(5)}</span><span>{usage.daily.at(-1)?.date.slice(5)}</span></div>
      </section>

      <section className="py-4">
        <div className="mb-2 text-[10px] font-semibold uppercase text-muted-foreground">Models</div>
        <div className="divide-y divide-border border-y border-border">
          {usage.models.map((model) => (
            <div key={model.model} className="flex items-center justify-between gap-3 py-2 font-mono text-[10px]">
              <span className="truncate text-foreground">{model.model}</span>
              <span className="shrink-0 text-muted-foreground">{compactTokens(model.usage.totalTokens)}</span>
            </div>
          ))}
        </div>
      </section>
      <div className="font-mono text-[8.5px] text-muted-foreground">Cached input is included in input. Reasoning is included in output.</div>
      <button type="button" onClick={onOpenOverview} className="mt-4 flex h-8 w-full items-center justify-center gap-1.5 rounded-md border border-border text-[11px] font-medium text-foreground hover:bg-muted">Open in Overview <ArrowUpRight className="size-3" /></button>
    </div>
  );
}

function AgentUsageMetric({ label, value, detail }: { label: string; value: string; detail: string }) {
  return <div className="min-w-0 px-3 py-3"><div className="text-[9px] font-semibold uppercase text-muted-foreground">{label}</div><div className="mt-1 font-mono text-[17px] font-semibold">{value}</div><div className="truncate font-mono text-[8.5px] text-muted-foreground">{detail}</div></div>;
}

function compactTokens(value: number) {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(value >= 10_000_000_000 ? 1 : 2)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 1 : 2)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(value >= 10_000 ? 1 : 2)}K`;
  return Math.round(value || 0).toLocaleString();
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
        className="w-full resize-y rounded-md bg-background p-2.5 text-[12px] leading-5 outline-none ring-1 ring-border focus:ring-ring/25"
      />
    </label>
  );
}

const membershipControlClass = "h-8 w-full min-w-0 rounded-md bg-background px-2 font-mono text-[10.5px] outline-none ring-1 ring-border focus:ring-ring/25 disabled:opacity-60";

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
      <textarea value={value} onChange={(event) => onChange(event.target.value)} rows={rows} maxLength={16000} className="w-full resize-y rounded-md bg-background p-2.5 text-[11.5px] leading-5 outline-none ring-1 ring-border focus:ring-ring/25" />
    </label>
  );
}

function isCustomModel(model: string) {
  return model !== "" && !MODEL_PRESETS.some((option) => option.value === model);
}

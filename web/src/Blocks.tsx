import { BarChart3, GitBranch, Inbox, Paperclip, RadioTower } from "lucide-react";
import { PublishedArtifactCard } from "./components/ArtifactPreview";
import { RawEnvelope, StructuredContextRow } from "./components/StructuredContext";
import { UserBubble, AssistantBubble } from "./pages/agent/MessageBubbles";
import { MarkdownContent } from "./pages/agent/markdown";
import type { Block, ExternalAttachment, ExternalThreadContext } from "./feed";
import { localTimeWithSeconds } from "./lib/format";

// Maps trajectory events onto the semantic palette.
const sysColor: Record<string, string> = {
  ok: "text-success",
  warn: "text-warning",
  err: "text-destructive",
  dim: "text-muted-foreground",
};

function tsShort(ts: string) {
  return localTimeWithSeconds(ts);
}

export function BlockView({ block }: { block: Block }) {
  switch (block.kind) {
    // User turn — rendered by the ported UserBubble (verbatim topic component).
    case "user":
      return (
        <UserBubble
          message={{ id: "u", topic_id: "", role: "user", content: block.text, created_at: block.ts }}
        >
          <UserAttachmentRows attachments={block.attachments} />
        </UserBubble>
      );

    case "agentMessage": {
      const style =
        block.variant === "req"
          ? {
              label: "REQ",
              chip: "bg-warning/10 text-warning",
              border: "border-warning/30",
              accent: "bg-warning",
            }
          : block.variant === "res"
            ? {
                label: "RES",
                chip: "bg-success/10 text-success",
                border: "border-success/30",
                accent: "bg-success",
              }
            : {
                label: "NOTIFY",
                chip: "bg-muted text-muted-foreground",
                border: "border-border",
                accent: "bg-muted-foreground/60",
              };
      return (
        <article className={`relative my-2 overflow-hidden rounded-md border ${style.border} bg-card shadow-card`}>
          <div className={`absolute inset-y-0 left-0 w-0.5 ${style.accent}`} />
          <div className="px-4 py-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className={`rounded-md px-2 py-0.5 font-mono text-[10.5px] font-semibold ${style.chip}`}>
                    {style.label}
                  </span>
                  <span className="truncate font-mono text-[11px] text-muted-foreground">
                    {block.from} -&gt; {block.to}
                  </span>
                </div>
                <h3 className="mt-1 truncate text-[14px] font-semibold text-foreground">
                  {block.subject || "(no subject)"}
                </h3>
              </div>
              <div className="shrink-0 text-right font-mono text-[10.5px] text-muted-foreground">
                <div>{tsShort(block.ts)}</div>
                <div className="mt-0.5">{block.id}</div>
              </div>
            </div>
            {block.replyTo && (
              <div className="mt-2 font-mono text-[11px] text-muted-foreground">
                reply to {block.replyTo}
              </div>
            )}
            <div className="mt-3 min-w-0 border-t border-border/70 pt-3 text-[13px] leading-6 text-foreground/90">
              <MarkdownContent content={block.body} />
            </div>
            {block.replyCommand && (
              <pre className="mt-2 overflow-auto border-l-2 border-border bg-muted/25 px-3 py-2 font-mono text-[11.5px] text-muted-foreground">
                {block.replyCommand}
              </pre>
            )}
            <RawEnvelope raw={block.raw} />
          </div>
        </article>
      );
    }

    case "externalMessage": {
      const expectationStyle =
        block.expectation === "required"
          ? "bg-warning/10 text-warning"
          : block.expectation === "none"
            ? "bg-muted text-muted-foreground"
            : "bg-secondary text-secondary-foreground";
      const expectationLabel =
        block.expectation === "required"
          ? "REPLY EXPECTED"
          : block.expectation === "none"
            ? "NO REPLY"
            : "OPTIONAL REPLY";
      const conversationLabel = block.membershipName || block.conversationId || "External conversation";
      return (
        <article className="relative my-2 overflow-hidden rounded-md border border-foreground/20 bg-card shadow-card">
          <div className="absolute inset-y-0 left-0 w-0.5 bg-foreground/65" />
          <div className="px-4 py-3">
            <div className="flex min-w-0 flex-wrap items-start gap-3">
              <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-secondary text-foreground">
                <Inbox className="size-4" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span className="rounded-md bg-foreground px-2 py-0.5 font-mono text-[10px] font-semibold text-background">
                    EXTERNAL
                  </span>
                  <span className="rounded-md border border-border px-1.5 py-0.5 font-mono text-[9.5px] font-semibold uppercase text-muted-foreground">
                    {block.provider}
                  </span>
                  <span className="min-w-0 truncate font-mono text-[10px] text-muted-foreground">
                    {block.conversationType || "conversation"}
                  </span>
                </div>
                <h3 className="mt-1 truncate text-[14px] font-semibold text-foreground">{block.sender}</h3>
                <div className="mt-0.5 min-w-0 truncate text-[11px] text-muted-foreground">
                  in <span className="font-medium text-foreground/75">{conversationLabel}</span>
                </div>
              </div>
              <div className="shrink-0 text-right">
                <span className={`inline-flex rounded-md px-2 py-0.5 font-mono text-[9.5px] font-semibold ${expectationStyle}`}>
                  {expectationLabel}
                </span>
                <div className="mt-1 font-mono text-[10px] text-muted-foreground">{tsShort(block.ts)}</div>
              </div>
            </div>

            {block.threadContext && <ExternalThreadContextView context={block.threadContext} />}

            <div className="mt-3 min-w-0 border-t border-border/70 pt-3 text-[13px] leading-6 text-foreground/90">
              {block.body ? <MarkdownContent content={block.body} /> : <span className="text-muted-foreground">Attachment message</span>}
            </div>

            <ExternalAttachmentRows attachments={block.attachments} />

            {(block.replyCommand || block.noReplyCommand) && (
              <div className="mt-3 space-y-1.5">
                {block.replyCommand && <pre className="overflow-auto rounded-md bg-muted/35 px-3 py-2 font-mono text-[10.5px] text-muted-foreground">{block.replyCommand}</pre>}
                {block.noReplyCommand && <pre className="overflow-auto rounded-md bg-muted/35 px-3 py-2 font-mono text-[10.5px] text-muted-foreground">{block.noReplyCommand}</pre>}
              </div>
            )}

            <details className="mt-2 text-[10px] text-muted-foreground">
              <summary className="cursor-pointer select-none font-mono hover:text-foreground">routing details</summary>
              <dl className="mt-2 grid min-w-0 gap-x-4 gap-y-1 border-t border-border/60 pt-2 sm:grid-cols-2">
                <RouteMeta label="Inbox" value={block.inboxItemId} />
                <RouteMeta label="Message" value={block.id} />
                <RouteMeta label="Conversation" value={block.conversationId} />
                <RouteMeta label="Sender" value={block.senderId} />
                {block.membershipId && <RouteMeta label="Membership" value={`${block.membershipId}${block.membershipVersion ? ` · v${block.membershipVersion}` : ""}`} />}
                <RouteMeta label="Reply policy" value={block.replyPolicy || "none"} />
              </dl>
            </details>
            <RawEnvelope raw={block.raw} />
          </div>
        </article>
      );
    }

    case "externalTrigger": {
      return (
        <article className="relative my-2 overflow-hidden rounded-md border border-[var(--loom-teal)]/35 bg-card shadow-card">
          <div className="absolute inset-y-0 left-0 w-0.5 bg-[var(--loom-teal)]" />
          <div className="px-4 py-3">
            <div className="flex min-w-0 flex-wrap items-start gap-3">
              <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-[var(--loom-teal)]/10 text-[var(--loom-teal)]">
                <RadioTower className="size-4" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span className="rounded-md bg-[var(--loom-teal)]/12 px-2 py-0.5 font-mono text-[10px] font-semibold text-[var(--loom-teal)]">TRIGGER</span>
                  <span className="rounded-md border border-border px-1.5 py-0.5 font-mono text-[9.5px] font-semibold uppercase text-muted-foreground">{block.provider}</span>
                  <span className="font-mono text-[9.5px] uppercase text-muted-foreground">{block.resourceKind}</span>
                </div>
                <h3 className="mt-1 truncate font-mono text-[13px] font-semibold text-foreground" title={block.subjectKey}>{block.subjectKey}</h3>
                <div className="mt-0.5 text-[11px] text-muted-foreground">External state changed: <span className="font-medium text-foreground/80">{block.event}</span></div>
              </div>
              <div className="shrink-0 text-right font-mono text-[9.5px] text-muted-foreground">
                {block.occurredAt && <div title="Provider event time">occurred {tsShort(block.occurredAt)}</div>}
                {block.observedAt && <div className="mt-0.5" title="Time CodexLoom observed the event">observed {tsShort(block.observedAt)}</div>}
              </div>
            </div>
            {block.summary && <div className="mt-3 border-t border-border/70 pt-3 text-[13px] leading-6 text-foreground/90"><MarkdownContent content={block.summary} /></div>}
            <div className="mt-3 border-l-2 border-[var(--loom-teal)]/55 bg-[var(--loom-teal)]/5 px-3 py-2.5">
              <div className="font-mono text-[9.5px] font-semibold uppercase text-[var(--loom-teal)]">Resume from here</div>
              <div className="mt-1 text-[12.5px] leading-5 text-foreground/85"><MarkdownContent content={block.resumeInstruction} /></div>
            </div>
            {block.instruction && <div className="mt-2 text-[10.5px] leading-4 text-muted-foreground">{block.instruction}</div>}
            {block.observation && (
              <details className="mt-2 text-[10px] text-muted-foreground">
                <summary className="cursor-pointer select-none font-mono hover:text-foreground">observed snapshot</summary>
                <pre className="mt-2 max-h-60 overflow-auto whitespace-pre-wrap break-words border-t border-border/60 bg-muted/25 px-3 py-2 font-mono text-[10.5px] leading-5 text-foreground/75">{JSON.stringify(block.observation, null, 2)}</pre>
              </details>
            )}
            <details className="mt-2 text-[10px] text-muted-foreground">
              <summary className="cursor-pointer select-none font-mono hover:text-foreground">routing details</summary>
              <dl className="mt-2 grid min-w-0 gap-x-4 gap-y-1 border-t border-border/60 pt-2 sm:grid-cols-2">
                <RouteMeta label="Trigger" value={block.triggerId} />
                <RouteMeta label="Message" value={block.id} />
                <RouteMeta label="Connection" value={block.connectionId} />
                <RouteMeta label="Event key" value={block.eventKey} />
              </dl>
            </details>
            <RawEnvelope raw={block.raw} />
          </div>
        </article>
      );
    }

    case "topicContext": {
      const payloadTone =
        block.payload?.kind === "intervention" || block.payload?.variant === "req"
          ? "bg-warning/10 text-warning"
          : block.payload?.variant === "res"
            ? "bg-success/10 text-success"
            : "bg-secondary text-secondary-foreground";
      return (
        <article className="relative my-2 overflow-hidden rounded-md border border-primary/25 bg-card shadow-card">
          <div className="absolute inset-y-0 left-0 w-0.5 bg-primary" />
          <div className="px-4 py-3">
            <header className="flex min-w-0 flex-wrap items-start gap-3">
              <div className="flex size-8 shrink-0 items-center justify-center rounded-sm bg-primary/10 text-primary">
                <GitBranch className="size-4" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span className="rounded-sm bg-primary px-2 py-0.5 font-mono text-[9.5px] font-semibold text-primary-foreground">TOPIC</span>
                  <span className="rounded-sm border border-border px-1.5 py-0.5 font-mono text-[9px] font-semibold uppercase text-muted-foreground">{block.status}</span>
                  {block.responsibleAgent && <span className="truncate font-mono text-[10px] text-muted-foreground">Responsible · {block.responsibleAgent}</span>}
                </div>
                <h3 className="mt-1 text-[14px] font-semibold leading-5 text-foreground">{block.title || "Topic work"}</h3>
              </div>
              <div className="shrink-0 text-right font-mono text-[9.5px] text-muted-foreground">
                <div>{tsShort(block.ts)}</div>
                <div className="mt-0.5">v{block.briefVersion || "?"} · e{block.eventSeq || "?"}</div>
              </div>
            </header>

            {block.payload && (
              <section className="mt-3 border-t border-border/70 pt-3">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <span className={`rounded-sm px-2 py-0.5 font-mono text-[9.5px] font-semibold ${payloadTone}`}>{block.payload.label}</span>
                  {block.payload.from && <span className="min-w-0 truncate font-mono text-[10px] text-muted-foreground">{block.payload.from} → {block.payload.to || block.responsibleAgent}</span>}
                  {block.payload.turnId && <span className="min-w-0 truncate font-mono text-[9.5px] text-muted-foreground">Turn {block.payload.turnId}</span>}
                </div>
                {block.payload.subject && <div className="mt-1.5 text-[13px] font-semibold text-foreground">{block.payload.subject}</div>}
                {block.payload.body && <div className="mt-2 min-w-0 text-[13px] leading-6 text-foreground/90"><MarkdownContent content={block.payload.body} /></div>}
                {block.payload.reason && <div className="mt-2 border-l-2 border-warning/40 pl-3 text-[11.5px] leading-5 text-muted-foreground">Reason: {block.payload.reason}</div>}
              </section>
            )}

            {(block.briefSummary || block.currentState || block.yourResponsibility) && (
              <section className="mt-3 border-y border-border/70 py-3">
                {block.briefSummary && <div className="text-[12.5px] font-medium leading-5 text-foreground">{block.briefSummary}</div>}
                {block.currentState && <div className="mt-1.5 text-[12px] leading-5 text-muted-foreground"><MarkdownContent content={block.currentState} /></div>}
                {block.yourResponsibility && (
                  <div className="mt-2 grid min-w-0 gap-1 sm:grid-cols-[112px_1fr] sm:gap-3">
                    <span className="font-mono text-[9.5px] font-semibold uppercase text-primary">Your responsibility</span>
                    <span className="min-w-0 text-[11.5px] leading-5 text-foreground/80">{block.yourResponsibility}</span>
                  </div>
                )}
              </section>
            )}

            <details className="group/topic mt-2 text-[10.5px] text-muted-foreground">
              <summary className="flex cursor-pointer list-none select-none items-center gap-2 py-1 font-mono hover:text-foreground">
                <span className="text-[8px] transition-transform group-open/topic:rotate-90">▶</span>
                topic context
                <span className="ml-auto truncate text-[9px]">{block.topicId}</span>
              </summary>
              <div className="mt-2 divide-y divide-border/60 border-y border-border/60">
                <TopicContextRow label="Purpose" value={block.purpose} markdown />
                <TopicContextRow label="Completion" value={block.completionBoundary} markdown />
                <TopicContextRow label="Next step" value={block.nextStep} markdown />
                <TopicContextRow label="Limitations" value={block.limitations} markdown />
                {block.links.length > 0 && (
                  <div className="grid min-w-0 gap-2 py-2.5 sm:grid-cols-[112px_1fr] sm:gap-3">
                    <span className="font-mono text-[9px] font-semibold uppercase">Key links</span>
                    <div className="min-w-0 space-y-1">
                      {block.links.map((link, index) => <div key={`${link.type}:${link.id}:${index}`} className="min-w-0 break-words"><span className="font-mono text-primary">{link.type}</span> · {link.label || link.id} <span className="font-mono text-[9px] text-muted-foreground/70">{link.id}</span></div>)}
                    </div>
                  </div>
                )}
                {block.delta.length > 0 && (
                  <div className="grid min-w-0 gap-2 py-2.5 sm:grid-cols-[112px_1fr] sm:gap-3">
                    <span className="font-mono text-[9px] font-semibold uppercase">Delta</span>
                    <div className="min-w-0 space-y-1.5">
                      {block.delta.map((event, index) => <div key={`${event.seq}:${index}`} className="grid min-w-0 grid-cols-[32px_minmax(0,1fr)] gap-2"><span className="font-mono text-[9px] text-primary">#{event.seq}</span><span className="min-w-0 break-words"><span className="font-mono text-[9px] uppercase text-muted-foreground">{event.type}</span>{event.summary ? ` · ${event.summary}` : ""}</span></div>)}
                    </div>
                  </div>
                )}
                <TopicContextRow label="Instruction" value={block.instruction} />
              </div>
            </details>
            <RawEnvelope raw={block.raw} />
          </div>
        </article>
      );
    }

    // Agent reply — rendered by the ported AssistantBubble (verbatim topic
    // component). A minimal single-iteration group carries the text.
    case "agent": {
      const msg = { id: block.id, topic_id: "", role: "assistant" as const, content: block.text, created_at: "" };
      return (
        <AssistantBubble
          group={{
            type: "assistant",
            message: msg,
            steps: [],
            iterations: [{ thinking: null, text: block.text, tools: [] }],
            usage: null,
          }}
          streaming={block.streaming}
        />
      );
    }

    case "usage": {
      const cachePercent = block.inputTokens > 0 ? Math.round((block.cachedInputTokens / block.inputTokens) * 100) : 0;
      return (
        <div className="my-2 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 border-y border-border/60 py-1.5 font-mono text-[9.5px] text-muted-foreground" title="Cached input is included in input; reasoning is included in output.">
          <BarChart3 className="size-3 shrink-0 text-[var(--loom-teal)]" />
          <span className="font-semibold text-foreground/75">{formatTokenCount(block.totalTokens)} tokens</span>
          <span>{formatTokenCount(block.inputTokens)} input</span>
          {block.cachedInputTokens > 0 && <span className="text-[var(--loom-teal)]">{cachePercent}% cached</span>}
          <span>{formatTokenCount(block.outputTokens)} output</span>
          {block.reasoningOutputTokens > 0 && <span>{formatTokenCount(block.reasoningOutputTokens)} reasoning</span>}
          {block.calls > 1 && <span>{block.calls} calls</span>}
          {block.model && <span className="ml-auto truncate">{block.model}</span>}
        </div>
      );
    }

    // Reasoning stays visually subordinate to the final answer.
    case "think": {
      const text = typeof block.text === "string" ? block.text : "";
      if (!text.trim()) return null;
      return (
        <details className="group/reason py-1" open={!block.done}>
          <summary className="flex cursor-pointer list-none select-none items-center gap-2 py-1 text-[11px] text-muted-foreground/60 transition-colors hover:text-muted-foreground">
            <span className="text-[10px] transition-transform group-open/reason:rotate-90">▶</span>
            <span className="font-mono">{block.done ? "reasoning" : "reasoning…"}</span>
          </summary>
          <pre className="mt-1 whitespace-pre-wrap border-l-2 border-muted-foreground/15 pl-4 font-sans text-[12.5px] leading-relaxed text-muted-foreground/70">
            {text}
          </pre>
        </details>
      );
    }

    // Command execution uses one frame; request and output are separated by rules.
    case "command": {
      const finished = block.exitCode !== null || block.status === "completed" || block.status === "failed";
      const ok = block.exitCode === 0;
      const hasDescription = Boolean(block.description?.trim());
      const summaryText = hasDescription ? block.description : block.command;
      return (
        <details className="my-2 min-w-0 overflow-hidden rounded-md border border-border bg-card shadow-card">
          <summary className="flex cursor-pointer select-none items-start gap-2 px-3 py-2.5">
            <span className={`min-w-0 flex-1 break-words whitespace-pre-wrap ${hasDescription ? "text-[12px] leading-5 text-foreground/90" : "font-mono text-[12.5px] text-foreground/85"}`}>
              {summaryText}
            </span>
            {finished ? (
              <span
                className={`mt-0.5 shrink-0 rounded-md px-2 py-0.5 text-[10.5px] font-medium ${
                  ok ? "bg-success/10 text-success" : "bg-destructive/10 text-destructive"
                }`}
              >
                exit {block.exitCode ?? "?"}
                {block.durationMs != null ? ` · ${block.durationMs}ms` : ""}
              </span>
            ) : (
              <span className="mt-0.5 flex shrink-0 items-center gap-1.5 rounded-md bg-warning/10 px-2 py-0.5 text-[10.5px] font-medium text-warning">
                <span className="spinner !h-2.5 !w-2.5" />
                running
              </span>
            )}
          </summary>
          <div className="space-y-2 border-t border-border bg-muted/30 px-3.5 py-2.5">
            <div className="space-y-1">
              <div className="font-mono text-[10px] uppercase text-muted-foreground/60">
                request
              </div>
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap border-l-2 border-border px-3 py-1 font-mono text-[12px] text-foreground/85">
                {block.command}
              </pre>
            </div>
            <div className="space-y-1">
              <div className="font-mono text-[10px] uppercase text-muted-foreground/60">
                output
              </div>
              <pre className="max-h-80 overflow-auto whitespace-pre-wrap border-l-2 border-border px-3 py-1 font-mono text-[12px] text-muted-foreground">
                {block.output || "(no output)"}
              </pre>
            </div>
          </div>
        </details>
      );
    }

    // File change — file paths in primary, diff coloured add/del/context.
    case "file":
      return (
        <details className="my-2 overflow-hidden rounded-md border border-border bg-card shadow-card" open>
          <summary className="flex cursor-pointer select-none items-center gap-2 px-3 py-2.5">
            <span className="flex-1 truncate font-mono text-[12.5px] text-foreground">
              {block.changes.map((c) => `${c.kind} ${c.path}`).join(", ") || "file change"}
            </span>
            <span className="shrink-0 rounded-md bg-success/10 px-2 py-0.5 text-[10.5px] font-medium text-success">
              {block.status}
            </span>
          </summary>
          <pre className="max-h-80 overflow-auto border-t border-border bg-muted/40 px-3.5 py-2.5 font-mono text-[12px]">
            {block.changes.map((c, i) => (
              <span key={i}>
                {c.diff
                  ? c.diff.split("\n").map((line, j) => (
                      <span
                        key={j}
                        className={
                          line.startsWith("+")
                            ? "text-add"
                            : line.startsWith("-")
                              ? "text-del"
                              : "text-muted-foreground"
                        }
                      >
                        {line}
                        {"\n"}
                      </span>
                    ))
                  : "(no diff)\n"}
              </span>
            ))}
          </pre>
        </details>
      );

    // Image item — rendered inline from a generated data URI or a hub-served
    // local image path.
    case "image":
      return (
        <div className="my-3 overflow-hidden rounded-md border border-border bg-card shadow-card">
          <img
            src={block.data}
            alt={block.path || "generated"}
            title={block.path}
            className="block h-auto max-h-[70vh] w-full object-contain"
          />
        </div>
      );

    case "artifact":
      return <PublishedArtifactCard artifact={block.artifact} publishedAt={block.ts} />;

    // System line — quiet meta with a mono timestamp.
    case "sys":
      return (
        <div className={`py-0.5 text-[12px] ${sysColor[block.cls]}`}>
          <span className="mr-2 font-mono text-[10.5px] text-muted-foreground/50">{tsShort(block.ts)}</span>
          {block.text}
        </div>
      );

    // Unrecognised item — raw JSON, collapsed.
    case "raw":
      return (
        <details className="my-2 overflow-hidden rounded-md border border-border bg-card">
          <summary className="cursor-pointer select-none px-3 py-2 font-mono text-[12.5px] text-muted-foreground">
            {block.type}
          </summary>
          <pre className="max-h-80 overflow-auto whitespace-pre-wrap border-t border-border bg-muted/40 px-3.5 py-2.5 font-mono text-[12px] text-muted-foreground">
            {block.json}
          </pre>
        </details>
      );
  }
}

function formatTokenCount(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 1 : 2)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(value >= 10_000 ? 1 : 2)}K`;
  return Math.round(value || 0).toLocaleString();
}

function RouteMeta({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid min-w-0 grid-cols-[80px_1fr] gap-2">
      <dt className="uppercase">{label}</dt>
      <dd className="min-w-0 truncate font-mono text-foreground/70" title={value}>{value || "-"}</dd>
    </div>
  );
}

function TopicContextRow({ label, value, markdown = false }: { label: string; value: string; markdown?: boolean }) {
  if (!value) return null;
  return (
    <StructuredContextRow label={label}>
      {markdown ? <MarkdownContent content={value} /> : value}
    </StructuredContextRow>
  );
}

function ExternalThreadContextView({ context }: { context: ExternalThreadContext }) {
  const count = context.messages.length;
  return (
    <details className="group/context mt-3 border-y border-border/70 bg-muted/15" open>
      <summary className="flex cursor-pointer list-none select-none items-center gap-2 py-2 text-[11px] text-muted-foreground hover:text-foreground">
        <span className="text-[9px] transition-transform group-open/context:rotate-90">▶</span>
        <span className="font-mono font-semibold uppercase">Thread context</span>
        <span>{count} earlier {count === 1 ? "message" : "messages"}</span>
        {context.truncated && <span className="ml-auto font-mono text-warning">truncated</span>}
      </summary>
      <div className="border-t border-border/60">
        {context.unavailableReason && (
          <div className="border-b border-warning/30 bg-warning/5 px-3 py-2 text-[11.5px] leading-5 text-warning">
            Context unavailable: {context.unavailableReason}
          </div>
        )}
        {context.messages.map((message, index) => (
          <section
            key={`${message.id}-${index}`}
            className="min-w-0 border-b border-border/60 px-3 py-2.5 last:border-b-0"
          >
            <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
              <span className="shrink-0 font-mono text-[9.5px] font-semibold uppercase text-[var(--loom-teal)]">
                {message.role}
              </span>
              <span className="min-w-0 flex-1 truncate text-[11.5px] font-medium text-foreground/80" title={message.senderId}>
                {message.sender}
              </span>
              {message.occurredAt && (
                <time className="shrink-0 font-mono text-[9.5px] text-muted-foreground" dateTime={message.occurredAt}>
                  {formatContextTime(message.occurredAt)}
                </time>
              )}
            </div>
            {message.body && (
              <div className="mt-1.5 min-w-0 text-[12.5px] leading-5 text-foreground/80">
                <MarkdownContent content={message.body} />
              </div>
            )}
            {message.textTruncated && <div className="mt-1 font-mono text-[9.5px] text-warning">message truncated</div>}
            <ExternalAttachmentRows attachments={message.attachments} compact />
          </section>
        ))}
        {!context.unavailableReason && count === 0 && (
          <div className="px-3 py-2 text-[11.5px] text-muted-foreground">No earlier thread messages were available.</div>
        )}
      </div>
    </details>
  );
}

function ExternalAttachmentRows({ attachments, compact = false }: { attachments: ExternalAttachment[]; compact?: boolean }) {
  if (attachments.length === 0) return null;
  return (
    <div className={`${compact ? "mt-2" : "mt-3"} divide-y divide-border/60 border-y border-border/60`}>
      {attachments.map((attachment, index) => {
        const label = attachment.name || attachment.path || attachment.url || attachment.id || `Attachment ${index + 1}`;
        const content = (
          <>
            <Paperclip className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate text-[11.5px] font-medium">{label}</span>
            {attachment.mimeType && <span className="hidden shrink-0 font-mono text-[9px] text-muted-foreground sm:block">{attachment.mimeType}</span>}
            {attachment.size && <span className="shrink-0 font-mono text-[9px] text-muted-foreground">{formatFileSize(attachment.size)}</span>}
          </>
        );
        return attachment.url ? (
          <a key={`${attachment.id || label}-${index}`} href={attachment.url} target="_blank" rel="noreferrer" className="flex min-w-0 items-center gap-2 py-2 hover:text-foreground">{content}</a>
        ) : (
          <div key={`${attachment.id || label}-${index}`} className="flex min-w-0 items-center gap-2 py-2">{content}</div>
        );
      })}
    </div>
  );
}

function formatContextTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function UserAttachmentRows({ attachments }: { attachments: ExternalAttachment[] }) {
  if (attachments.length === 0) return null;
  return (
    <div className={`${attachments.some((attachment) => attachment.mimeType?.startsWith("image/") && (attachment.url || attachment.path)) ? "mt-2" : ""} space-y-1.5`}>
      {attachments.map((attachment, index) => {
        const href = attachment.url || (attachment.mimeType?.startsWith("image/") && attachment.path ? `/api/images?path=${encodeURIComponent(attachment.path)}` : undefined);
        const label = attachment.name || attachment.id || `Attachment ${index + 1}`;
        if (attachment.mimeType?.startsWith("image/") && href) {
          return (
            <a key={`${attachment.id || label}-${index}`} href={href} target="_blank" rel="noreferrer" className="block overflow-hidden rounded-sm border border-border/80 bg-background/70">
              <img src={href} alt={label} className="max-h-72 w-full object-contain" />
              <span className="flex min-w-0 items-center gap-2 border-t border-border/70 px-2 py-1.5 text-[10.5px] text-muted-foreground">
                <span className="min-w-0 flex-1 truncate">{label}</span>
                {attachment.size !== undefined && <span className="font-mono text-[9px]">{formatFileSize(attachment.size)}</span>}
              </span>
            </a>
          );
        }
        const content = (
          <span className="flex min-w-0 items-center gap-2 rounded-sm border border-border/70 bg-background/60 px-2 py-1.5">
            <Paperclip className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate text-[11px] font-medium">{label}</span>
            {attachment.size !== undefined && <span className="shrink-0 font-mono text-[9px] text-muted-foreground">{formatFileSize(attachment.size)}</span>}
          </span>
        );
        return href ? <a key={`${attachment.id || label}-${index}`} href={href} target="_blank" rel="noreferrer">{content}</a> : <span key={`${attachment.id || label}-${index}`}>{content}</span>;
      })}
    </div>
  );
}

function formatFileSize(raw: string | number) {
  const size = Number(raw);
  if (!Number.isFinite(size) || size < 0) return String(raw);
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

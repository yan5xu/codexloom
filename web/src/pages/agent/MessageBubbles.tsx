/**
 * MessageBubbles — WHO: Rendering components for Agent Chat message stream.
 *
 * WHERE:
 * - Used by: AgentStreamView and design-system message areas
 * - Layout: Inline within ScrollArea
 *
 * WITH:
 * - 组件: MarkdownContent (shared), iteration-based rendering (v3, matches Space AITurnCard)
 * - 数据: ChatMessage, groupMessages algorithm
 * - 共享: shortTime, formatTokens, extractToolBrief from @/lib/format
 *
 * WHY:
 * - Agent Chat uses ChatMessage role-based model (different from Space's event-sourced model)
 * - Iteration-based rendering: completed iterations collapse, intermediate text sunken,
 *   final text prominent with primary border, reasoning collapsible as "Reasoning"
 */
import { useMemo, useState, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { Check, ChevronRight, Copy, Loader2, Wrench } from "lucide-react";
import type { ChatMessage, TokenUsage } from "../../lib/chat/types";
import { MarkdownContent } from "./markdown";

/* ================================================================
   CopyButton — appears on hover, copies text content
   ================================================================ */

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }, [text]);

  return (
    <button
      onClick={handleCopy}
      className={cn(
        "flex size-7 cursor-pointer items-center justify-center rounded-md transition-all duration-150",
        copied
          ? "text-primary bg-primary/10"
          : "text-muted-foreground/40 hover:text-muted-foreground hover:bg-foreground/[0.04]",
      )}
      title={copied ? "Copied" : "Copy"}
    >
      {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
    </button>
  );
}

/* ================================================================
   Utilities
   ================================================================ */

import { shortTime, formatTokens, extractToolBrief } from "@/lib/format";

function extractThinking(raw: string): { thinking: string; content: string } {
  const thinkRegex = /<think>([\s\S]*?)<\/think>/g;
  let thinking = "";
  let match: RegExpExecArray | null;
  while ((match = thinkRegex.exec(raw)) !== null) {
    thinking += (thinking ? "\n\n" : "") + match[1].trim();
  }
  let content = raw
    .replace(/<think>[\s\S]*?<\/think>/g, "")
    .replace(/<system-reminder>[\s\S]*?<\/system-reminder>/g, "")
    .trim();
  return { thinking, content };
}

/* ================================================================
   Iteration type — one LLM API call within a turn
   ================================================================ */

export interface AgentIteration {
  thinking: string | null;
  text: string | null;
  tools: ChatMessage[]; // tool role messages
}

/* ================================================================
   Grouped message types for rendering
   ================================================================ */

export interface UserGroup {
  type: "user";
  message: ChatMessage;
}

/** A single step in the reasoning process (thinking or tool call), in order. */
export type ReasoningStep =
  | { kind: "thinking"; content: string }
  | { kind: "tool"; message: ChatMessage };

export interface AssistantGroup {
  type: "assistant";
  /** The final reply (last assistant message with content). */
  message: ChatMessage;
  /** Ordered sequence of thinking + tool calls, preserving interleaving. */
  steps: ReasoningStep[];
  /** Iteration-based structure (v3) */
  iterations: AgentIteration[];
  /** Token usage */
  usage: TokenUsage | null;
}

export interface StreamGroup {
  type: "stream";
  message: ChatMessage;
}

export type MessageGroup = UserGroup | AssistantGroup | StreamGroup;

export function groupMessages(msgs: ChatMessage[]): MessageGroup[] {
  const groups: MessageGroup[] = [];
  let i = 0;

  while (i < msgs.length) {
    const msg = msgs[i];

    if (msg.role === "user") {
      groups.push({ type: "user", message: msg });
      i++;
      continue;
    }

    // Collect all assistant + tool messages belonging to the same turn
    const turnId = msg.turn_id;
    const turnMsgs: ChatMessage[] = [];

    if (turnId) {
      while (i < msgs.length && msgs[i].turn_id === turnId && msgs[i].role !== "user") {
        turnMsgs.push(msgs[i]);
        i++;
      }
    } else {
      // No turn_id (streaming or legacy)
      turnMsgs.push(msgs[i]);
      i++;
      while (i < msgs.length && msgs[i].role === "tool") {
        turnMsgs.push(msgs[i]);
        i++;
      }
    }

    // Build iterations: each assistant message starts a new iteration
    const iterations: AgentIteration[] = [];
    const steps: ReasoningStep[] = []; // backward compat
    let finalMessage: ChatMessage | undefined;
    let lastUsage: string | undefined;
    let currentIter: AgentIteration | null = null;

    for (const m of turnMsgs) {
      if (m.role === "assistant") {
        // Start a new iteration
        const reasoning = m.reasoning || extractThinking(m.content).thinking;
        const cleanContent = m.reasoning ? m.content : extractThinking(m.content).content;

        currentIter = {
          thinking: reasoning || null,
          text: cleanContent.trim() || null,
          tools: [],
        };
        iterations.push(currentIter);

        // Backward compat: add to flat steps
        if (reasoning) {
          steps.push({ kind: "thinking", content: reasoning });
        }

        // Track the last assistant with actual content as final reply
        if (cleanContent.trim()) {
          finalMessage = { ...m, content: cleanContent, reasoning: undefined };
        }
        if (m.usage) lastUsage = m.usage;
      } else if (m.role === "tool") {
        if (currentIter) {
          currentIter.tools.push(m);
        }
        // Backward compat
        steps.push({ kind: "tool", message: m });
      }
    }

    // If no assistant had content, use the last one
    if (!finalMessage) {
      const lastAssistant = [...turnMsgs].reverse().find((m) => m.role === "assistant");
      finalMessage = lastAssistant || turnMsgs[0];
    }

    let parsedUsage: TokenUsage | null = null;
    if (lastUsage) {
      try { parsedUsage = JSON.parse(lastUsage); } catch {}
      finalMessage = { ...finalMessage!, usage: lastUsage };
    }

    groups.push({ type: "assistant", message: finalMessage!, steps, iterations, usage: parsedUsage });
  }

  return groups;
}

/* ================================================================
   ToolCallItem — single collapsible tool call
   ================================================================ */

function ToolCallItem({ tool, isLast }: { tool: ChatMessage; isLast: boolean }) {
  const { t } = useTranslation();
  const { brief, command } = extractToolBrief(tool.tool_args);
  const isDone = !!tool.content;

  return (
    <details className={cn("group/tool overflow-hidden rounded-md border border-border", !isLast && "mb-1.5")}>
      <summary className="list-none cursor-pointer flex items-center gap-3 px-3 py-2 hover:bg-foreground/[0.03] transition-colors">
        <div className="flex size-5 shrink-0 items-center justify-center rounded-md border border-border">
          {isDone ? (
            <Check className="h-3 w-3 text-success" strokeWidth={3} />
          ) : (
            <Loader2 className="h-3 w-3 animate-spin text-primary" />
          )}
        </div>
        <div className="flex-1 min-w-0">
          {brief && (
            <div className="text-xs text-foreground truncate">{brief}</div>
          )}
          <div className="flex items-center gap-2">
            <Wrench className="w-3 h-3 text-muted-foreground shrink-0" />
            <span className="font-mono text-[11px] font-semibold text-muted-foreground/80 truncate">
              {command || tool.tool_name || "run"}
            </span>
          </div>
        </div>
        <ChevronRight className="size-3.5 text-muted-foreground/60 transition-transform group-open/tool:rotate-90" />
      </summary>

      <div className="space-y-3 border-t border-border bg-muted/20 px-3 py-3">
        {tool.tool_args && (
          <div className="space-y-1">
            <span className="text-[10px] font-medium uppercase text-muted-foreground">{t("agent.input")}</span>
            <pre className="max-h-40 overflow-y-auto whitespace-pre-wrap border-l-2 border-border px-3 py-1 font-mono text-[11px] leading-relaxed">
              {(() => {
                try { return JSON.stringify(JSON.parse(tool.tool_args), null, 2); }
                catch { return tool.tool_args; }
              })()}
            </pre>
          </div>
        )}
        {tool.content && (
          <div className="space-y-1">
            <span className="text-[10px] font-medium uppercase text-muted-foreground">{t("agent.result")}</span>
            <pre className="max-h-80 overflow-y-auto whitespace-pre-wrap border-l-2 border-border px-3 py-1 font-mono text-[11px] leading-relaxed">
              {tool.content}
            </pre>
          </div>
        )}
      </div>
    </details>
  );
}

/* ================================================================
   Iteration summary (for collapsed state)
   ================================================================ */

function agentIterationSummary(iter: AgentIteration): string {
  const parts: string[] = [];
  if (iter.text) {
    const preview = iter.text.slice(0, 50).replace(/[#*\n]/g, "").trim();
    parts.push(`${preview}${iter.text.length > 50 ? "..." : ""}`);
  }
  if (iter.tools.length > 0) {
    const names = iter.tools.map((t) => {
      const { brief } = extractToolBrief(t.tool_args);
      return brief || t.tool_name || "run";
    }).join(", ");
    parts.push(`${iter.tools.length} tool${iter.tools.length > 1 ? "s" : ""} (${names})`);
  }
  if (parts.length === 0 && iter.thinking) {
    const preview = iter.thinking.slice(0, 50).replace(/\n/g, " ").trim();
    parts.push(`${preview}${iter.thinking.length > 50 ? "..." : ""}`);
  }
  return parts.join(" · ");
}

/* ================================================================
   CollapsedIteration — one-line summary, click to expand
   ================================================================ */

function CollapsedIteration({ iteration }: { iteration: AgentIteration }) {
  const [open, setOpen] = useState(false);
  const summary = agentIterationSummary(iteration);
  const hasErrors = iteration.tools.some((t) => t.content?.startsWith("Error:"));

  return (
    <div>
      <button
        onClick={() => setOpen(!open)}
        className="flex w-full cursor-pointer items-center gap-2 rounded-md py-1.5 text-left transition-colors hover:bg-foreground/[0.02]"
      >
        <ChevronRight className={cn("size-3 text-muted-foreground/50 transition-transform", open && "rotate-90")} />
        <span className={cn("text-xs text-muted-foreground/60 truncate", hasErrors && "text-destructive/50")}>
          {summary}
        </span>
      </button>
      {open && (
        <div className="pb-2 pt-1">
          <ExpandedIterationContent iteration={iteration} isFinal={false} isRunning={false} />
        </div>
      )}
    </div>
  );
}

/* ================================================================
   ExpandedIterationContent — reasoning + text + tools
   ================================================================ */

function ExpandedIterationContent({ iteration, isFinal, isRunning }: { iteration: AgentIteration; isFinal: boolean; isRunning: boolean }) {
  const hasText = !!iteration.text;
  const hasTools = iteration.tools.length > 0;

  return (
    <div className="flex flex-col gap-2">
      {/* Reasoning */}
      {iteration.thinking && (
        isRunning ? (
          <div className="text-[12px] text-muted-foreground/60 leading-relaxed pl-4 border-l-2 border-primary/20 whitespace-pre-wrap">
            {iteration.thinking}
          </div>
        ) : (
          <details className="group/reason">
            <summary className="list-none cursor-pointer flex items-center gap-2 py-1 text-[11px] text-muted-foreground/50 hover:text-muted-foreground/70 transition-colors">
              <ChevronRight className="size-3 transition-transform group-open/reason:rotate-90" />
              <span className="font-mono">Reasoning</span>
            </summary>
            <div className="mt-1 pl-4 border-l-2 border-muted-foreground/10 text-[12px] text-muted-foreground/50 leading-relaxed whitespace-pre-wrap">
              {iteration.thinking}
            </div>
          </details>
        )
      )}

      {/* Text — intermediate (sunken muted) vs final (primary border) */}
      {hasText && (
        isFinal && !hasTools ? (
          <div className="border-l-2 border-primary/30 pl-3">
            <MarkdownContent content={iteration.text!} />
          </div>
        ) : (
          <div className="rounded-md border-l-2 border-border bg-muted/20 px-3 py-2">
            <p className="text-[13px] text-muted-foreground/70 leading-relaxed">{iteration.text}</p>
          </div>
        )
      )}

      {/* Tools */}
      {hasTools && (
        <details className="group/tools" open={isRunning}>
          <summary className="-mx-2 flex cursor-pointer list-none items-center gap-2 rounded-md px-2 py-1.5 transition-colors hover:bg-foreground/[0.03]">
            <ChevronRight className="size-3 text-muted-foreground transition-transform group-open/tools:rotate-90" />
            <span className="text-[11px] font-semibold text-muted-foreground">
              {iteration.tools.length} step{iteration.tools.length !== 1 ? "s" : ""}
            </span>
            {isRunning && iteration.tools.some((t) => !t.content) && (
              <span className="size-1.5 animate-pulse rounded-full bg-warning" />
            )}
          </summary>
          <div className="mt-2 ml-1 pl-3 border-l-2 border-border/40 space-y-1.5">
            {iteration.tools.map((tool, j) => (
              <ToolCallItem key={tool.id} tool={tool} isLast={j === iteration.tools.length - 1} />
            ))}
          </div>
        </details>
      )}
    </div>
  );
}

/* ================================================================
   CloudUsageDisplay — subtle cloud credit/token usage
   ================================================================ */

function CloudUsageDisplay({ usage }: { usage: TokenUsage }) {
  const { t } = useTranslation();
  const prompt = usage.prompt_tokens ?? 0;
  const completion = usage.completion_tokens ?? 0;
  const cacheHit = usage.prompt_cache_hit_tokens ?? 0;
  const cacheMiss = usage.prompt_cache_miss_tokens ?? 0;

  const promptDenom = (cacheHit + cacheMiss) || prompt || 1;
  const cachePct = Math.round((cacheHit / promptDenom) * 100);

  const formattedPrompt = formatTokens(prompt).toUpperCase();
  const formattedCompletion = formatTokens(completion).toUpperCase();
  const cacheText = cacheHit > 0 ? t("agent.cachedPct", { pct: cachePct }) : "";

  return (
    <div className="mt-2 text-[10px] text-muted-foreground font-mono">
      {t("agent.tokenUsageCloud", { prompt: formattedPrompt, cache: cacheText, completion: formattedCompletion })}
    </div>
  );
}

/* ================================================================
   MultiIterationBlock — renders all iterations with two-level collapsing
   When 3+ non-final iterations, wrap them in one collapsible summary.
   ================================================================ */

function MultiIterationBlock({ iterations, streaming, usage }: { iterations: AgentIteration[]; streaming: boolean; usage: TokenUsage | null }) {
  const [processOpen, setProcessOpen] = useState(false);

  // Separate final iteration from intermediate ones
  const lastIter = iterations[iterations.length - 1];
  const isFinalReply = lastIter && lastIter.tools.length === 0 && !streaming;
  const intermediateIters = isFinalReply ? iterations.slice(0, -1) : iterations.slice(0, -1);
  const currentIter = iterations[iterations.length - 1];

  // Count total tools across all intermediate iterations
  const totalTools = intermediateIters.reduce((sum, iter) => sum + iter.tools.length, 0);

  // Group-collapse when 2+ intermediate iterations to keep the view compact
  const shouldGroupCollapse = intermediateIters.length >= 2;

  // Build summary for grouped collapse
  const groupSummary = (() => {
    const parts: string[] = [];
    parts.push(`${intermediateIters.length} iterations`);
    if (totalTools > 0) parts.push(`${totalTools} tools`);
    return parts.join(" · ");
  })();

  return (
    <div>
      {shouldGroupCollapse ? (
        /* 3+ intermediate iterations → single collapsible summary */
        <>
          <button
            onClick={() => setProcessOpen(!processOpen)}
            className="mb-1 flex w-full cursor-pointer items-center gap-2 rounded-md py-1.5 text-left transition-colors hover:bg-foreground/[0.02]"
          >
            <ChevronRight className={cn("size-3 text-muted-foreground/50 transition-transform", processOpen && "rotate-90")} />
            <span className="text-xs text-muted-foreground/60">{groupSummary}</span>
          </button>
          {processOpen && (
            <div className="mb-2 pl-1">
              {intermediateIters.map((iter, i) => (
                <div key={i}>
                  {i > 0 && (
                    <div className="my-1">
                      <div className="h-px bg-border/50" />
                    </div>
                  )}
                  <CollapsedIteration iteration={iter} />
                </div>
              ))}
            </div>
          )}
        </>
      ) : (
        /* 1-2 intermediate iterations → show individually */
        intermediateIters.map((iter, i) => (
          <div key={i}>
            {i > 0 && (
              <div className="my-1">
                <div className="h-px bg-border/50" />
              </div>
            )}
            <CollapsedIteration iteration={iter} />
          </div>
        ))
      )}

      {/* Divider before final/current */}
      {intermediateIters.length > 0 && (
        <div className="my-1">
          <div className="h-px bg-border/50" />
        </div>
      )}

      {/* Final / current iteration — always expanded */}
      {currentIter && (
        <div className="py-1">
          {streaming && (
            <div className="flex items-center gap-1.5 mb-2">
              <span className="size-1.5 animate-pulse rounded-full bg-warning" />
              <span className="font-mono text-[10px] text-warning">Processing...</span>
            </div>
          )}
          <ExpandedIterationContent
            iteration={currentIter}
            isFinal={isFinalReply}
            isRunning={streaming}
          />
        </div>
      )}

      {/* Token usage */}
      {usage && (
        <div className="mt-2">
          <CloudUsageDisplay usage={usage} />
        </div>
      )}
    </div>
  );
}

/* ================================================================
   AssistantBubble — renders assistant message with iteration-based structure
   ================================================================ */

export function AssistantBubble({ group, streaming = false }: { group: AssistantGroup; streaming?: boolean }) {
  const { t } = useTranslation();
  const { message, iterations, usage } = group;

  // For streaming messages, content may have inline <think> tags
  const extracted = useMemo(() => extractThinking(message.content), [message.content]);
  const content = message.reasoning ? message.content : extracted.content;

  // Streaming: if no iterations yet, create a synthetic one from the streaming message
  const effectiveIterations = useMemo(() => {
    if (streaming && iterations.length === 0) {
      return [{
        thinking: extracted.thinking || null,
        text: extracted.content || null,
        tools: [],
      }] as AgentIteration[];
    }
    return iterations;
  }, [streaming, iterations, extracted]);

  const hasContent = !!content || streaming;

  // Find final text for copy button
  const finalText = (() => {
    for (let i = effectiveIterations.length - 1; i >= 0; i--) {
      if (effectiveIterations[i].text) return effectiveIterations[i].text;
    }
    return content;
  })();

  // Single iteration with no tools = simple reply (no iteration structure needed)
  const isSimpleReply = effectiveIterations.length <= 1 && effectiveIterations[0]?.tools.length === 0;

  return (
    <div className="group/bubble py-3">
      <div className="flex items-center gap-2 mb-1.5">
        <span className="text-[10px] font-semibold uppercase text-foreground">{t("agent.agent")}</span>
        <span className="text-[10px] font-mono text-muted-foreground/40">
          {streaming ? (
            <span className="flex items-center gap-1.5">
              <span className="size-1.5 animate-pulse rounded-full bg-warning" />
              {t("agent.streaming")}
            </span>
          ) : (
            shortTime(message.created_at)
          )}
        </span>
        {/* Copy button — visible on hover */}
        {finalText && !streaming && (
          <span className="opacity-0 group-hover/bubble:opacity-100 transition-opacity duration-150">
            <CopyButton text={finalText} />
          </span>
        )}
      </div>

      {/* Simple reply: just reasoning + final text (no iteration structure) */}
      {isSimpleReply ? (
        <>
          {effectiveIterations[0]?.thinking && !streaming && (
            <details className="group/reason mb-2">
              <summary className="list-none cursor-pointer flex items-center gap-2 py-1 text-[11px] text-muted-foreground/50 hover:text-muted-foreground/70 transition-colors">
                <ChevronRight className="size-3 transition-transform group-open/reason:rotate-90" />
                <span className="font-mono">Reasoning</span>
              </summary>
              <div className="mt-1 pl-4 border-l-2 border-muted-foreground/10 text-[12px] text-muted-foreground/50 leading-relaxed whitespace-pre-wrap">
                {effectiveIterations[0].thinking}
              </div>
            </details>
          )}
          {streaming && effectiveIterations[0]?.thinking && (
            <div className="mb-2 text-[12px] text-muted-foreground/60 leading-relaxed pl-4 border-l-2 border-primary/20 whitespace-pre-wrap">
              {effectiveIterations[0].thinking}
            </div>
          )}
          {hasContent && (
            <div className="border-l-2 border-primary/30 pl-3">
              <MarkdownContent content={content} streaming={streaming} />
            </div>
          )}
        </>
      ) : (
        /* Multi-iteration rendering */
        <MultiIterationBlock
          iterations={effectiveIterations}
          streaming={streaming}
          usage={usage}
        />
      )}

      {/* Token usage for simple replies */}
      {isSimpleReply && usage && (
        <div className="mt-2">
          <CloudUsageDisplay usage={usage} />
        </div>
      )}
    </div>
  );
}

/* ================================================================
   UserBubble — renders user message
   ================================================================ */

export function UserBubble({ message }: { message: ChatMessage }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-end py-3">
      <div className="mb-1 flex items-center gap-2">
        <span className="text-[10px] font-semibold uppercase text-muted-foreground">{t("agent.you")}</span>
        <span className="text-[10px] font-mono text-muted-foreground/40">{shortTime(message.created_at)}</span>
      </div>
      <div className="max-w-[88%] whitespace-pre-wrap break-words rounded-md border border-border bg-secondary px-3 py-2 text-sm leading-6 sm:max-w-[78%]">
        {message.content}
      </div>
    </div>
  );
}

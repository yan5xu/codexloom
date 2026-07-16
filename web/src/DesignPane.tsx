import {
  Activity,
  AlertTriangle,
  Check,
  ChevronRight,
  CircleDot,
  Info,
  Moon,
  Plus,
  Search,
  Sun,
  SwatchBook,
  Trash2,
  Type,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "./components/ui/button";
import { BrandLockup, BrandMark } from "./components/BrandMark";

type ThemeMode = "light" | "dark";

type ColorToken = {
  name: string;
  role: string;
  foreground?: string;
};

const colorTokens: ColorToken[] = [
  { name: "background", role: "Application canvas" },
  { name: "foreground", role: "Primary text", foreground: "background" },
  { name: "card", role: "Raised working surface" },
  { name: "muted", role: "Quiet grouping surface" },
  { name: "muted-foreground", role: "Secondary text", foreground: "background" },
  { name: "border", role: "Structure and separation" },
  { name: "primary", role: "Graphite command", foreground: "primary-foreground" },
  { name: "selection", role: "Selected navigation and rows" },
  { name: "ring", role: "Keyboard focus and active input", foreground: "background" },
  { name: "success", role: "Healthy or completed state", foreground: "background" },
  { name: "warning", role: "Active or waiting state", foreground: "background" },
  { name: "destructive", role: "Failure or destructive action", foreground: "destructive-foreground" },
];

const chartTokens = ["chart-1", "chart-2", "chart-3", "chart-4", "chart-5"];

const spacingTokens = [
  { name: "space-1", value: "4", use: "Icon gaps, dense offsets" },
  { name: "space-2", value: "8", use: "Control internals" },
  { name: "space-3", value: "12", use: "Dense data groups" },
  { name: "space-4", value: "16", use: "Panel padding" },
  { name: "space-6", value: "24", use: "Section rhythm" },
  { name: "space-8", value: "32", use: "Page breathing room" },
];

const statusSamples = [
  { label: "Running", detail: "Turn in progress", color: "bg-success", tone: "bg-success/10 text-success" },
  { label: "Idle", detail: "Ready for work", color: "bg-muted-foreground/40", tone: "bg-muted text-muted-foreground" },
  { label: "Queued", detail: "Waiting for delivery", color: "bg-warning", tone: "bg-warning/10 text-warning" },
  { label: "Failed", detail: "Needs attention", color: "bg-destructive", tone: "bg-destructive/10 text-destructive" },
];

function SectionHeader({ index, title, description }: { index: string; title: string; description: string }) {
  return (
    <div className="mb-4 grid gap-1 sm:grid-cols-[72px_1fr] sm:gap-4">
      <div className="font-mono text-[10px] text-muted-foreground">{index}</div>
      <div>
        <h2 className="font-sans text-[16px] font-semibold leading-5 text-foreground">{title}</h2>
        <p className="mt-1 max-w-2xl text-[12.5px] leading-5 text-muted-foreground">{description}</p>
      </div>
    </div>
  );
}

function ColorSample({ token, value }: { token: ColorToken; value: string }) {
  const foreground = token.foreground ? `var(--${token.foreground})` : "var(--foreground)";
  return (
    <article className="min-w-0 overflow-hidden rounded-lg border border-border bg-card">
      <div
        className="flex h-20 items-end p-2.5"
        style={{ background: `var(--${token.name})`, color: foreground }}
      >
        <span className="font-mono text-[9px] opacity-70">Aa</span>
      </div>
      <div className="min-w-0 px-3 py-2.5">
        <div className="truncate font-mono text-[11px] font-semibold text-foreground">--{token.name}</div>
        <div className="mt-0.5 truncate text-[10.5px] text-muted-foreground">{token.role}</div>
        <div className="mt-2 truncate font-mono text-[9px] text-muted-foreground/70" title={value}>{value || "computed token"}</div>
      </div>
    </article>
  );
}

function StatusBadge({ sample }: { sample: (typeof statusSamples)[number] }) {
  return (
    <div className="flex min-w-0 items-center gap-3 border-b border-border px-3 py-3 last:border-b-0">
      <span className={`size-2 shrink-0 rounded-full ${sample.color}`} />
      <div className="min-w-0 flex-1">
        <div className="text-[12.5px] font-medium text-foreground">{sample.label}</div>
        <div className="text-[10.5px] text-muted-foreground">{sample.detail}</div>
      </div>
      <span className={`rounded-md px-2 py-1 font-mono text-[9px] ${sample.tone}`}>{sample.label.toLowerCase()}</span>
    </div>
  );
}

export function DesignPane({ embedded = false }: { embedded?: boolean }) {
  const [theme, setTheme] = useState<ThemeMode>(() => document.documentElement.classList.contains("dark") ? "dark" : "light");
  const [tokenValues, setTokenValues] = useState<Record<string, string>>({});

  const refreshTokenValues = () => {
    const styles = getComputedStyle(document.documentElement);
    const names = [...colorTokens.map((token) => token.name), ...chartTokens];
    setTokenValues(Object.fromEntries(names.map((name) => [name, styles.getPropertyValue(`--${name}`).trim()])));
  };

  const applyTheme = (next: ThemeMode) => {
    document.documentElement.classList.toggle("dark", next === "dark");
    localStorage.setItem("codexloom-theme", next);
    setTheme(next);
    window.requestAnimationFrame(refreshTokenValues);
  };

  useEffect(() => {
    refreshTokenValues();
  }, []);

  useEffect(() => {
    const root = (((window as any).codexLoom ||= (window as any).codexHub || {}) as Record<string, any>);
    (window as any).codexHub = root;
    root.design = {
      state: () => ({
        theme,
        colorTokens: colorTokens.length,
        chartTokens: chartTokens.length,
        sections: ["foundation", "color", "type", "space", "components", "patterns"],
      }),
      setTheme: async (next: ThemeMode) => {
        if (next !== "light" && next !== "dark") throw new Error(`Unknown theme: ${next}`);
        applyTheme(next);
        await new Promise((resolve) => window.setTimeout(resolve, 50));
        return root.design.state();
      },
    };
  }, [theme]);

  const resolvedColors = useMemo(() => tokenValues, [tokenValues]);

  return (
    <main className="flex min-w-0 flex-1 flex-col overflow-hidden bg-background">
      {!embedded ? <header className="flex min-h-14 shrink-0 items-center gap-3 border-b border-border bg-card/80 py-2 pl-14 pr-3 md:px-5">
        <SwatchBook className="size-4 shrink-0 text-primary" />
        <div className="min-w-0">
          <h1 className="truncate font-serif text-xl">Design System</h1>
          <div className="hidden font-mono text-[9px] text-muted-foreground sm:block">VI · production tokens · v1</div>
        </div>
        <div className="ml-auto flex shrink-0 items-center rounded-md border border-border bg-background p-0.5">
          <button
            type="button"
            onClick={() => applyTheme("light")}
            title="Preview light tokens"
            aria-label="Preview light tokens"
            className={`flex size-7 items-center justify-center rounded-[4px] ${theme === "light" ? "bg-card text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
          >
            <Sun className="size-3.5" />
          </button>
          <button
            type="button"
            onClick={() => applyTheme("dark")}
            title="Preview dark tokens"
            aria-label="Preview dark tokens"
            className={`flex size-7 items-center justify-center rounded-[4px] ${theme === "dark" ? "bg-card text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
          >
            <Moon className="size-3.5" />
          </button>
        </div>
      </header> : null}

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-6xl px-4 py-6 md:px-6 md:py-8">
          <section data-design-section="foundation" className="pb-8">
            <SectionHeader index="01 / 06" title="Brand foundation" description="Quiet operational software for durable agent organizations. The interface favors continuity, responsibility, and legible system state over spectacle." />
            <div className="grid overflow-hidden rounded-lg border border-border bg-card lg:grid-cols-[1.15fr_1fr]">
              <div className="flex min-h-48 items-center border-b border-border p-6 lg:border-b-0 lg:border-r lg:p-8">
                <BrandLockup />
              </div>
              <div className="grid grid-cols-2">
                {[
                  ["Quiet", "Information leads; decoration recedes."],
                  ["Operational", "State and action remain easy to scan."],
                  ["Continuous", "History and identity stay visible."],
                  ["Accountable", "Ownership and boundaries are explicit."],
                ].map(([title, detail], index) => (
                  <div key={title} className={`p-4 ${index % 2 === 0 ? "border-r border-border" : ""} ${index < 2 ? "border-b border-border" : ""}`}>
                    <div className="text-[12px] font-semibold text-foreground">{title}</div>
                    <div className="mt-1 text-[10.5px] leading-4 text-muted-foreground">{detail}</div>
                  </div>
                ))}
              </div>
            </div>
            <div className="mt-4 grid overflow-hidden rounded-lg border border-border bg-card sm:grid-cols-[1fr_1fr_1fr]">
              <div className="flex min-h-28 items-center justify-center border-b border-border p-5 text-foreground sm:border-b-0 sm:border-r">
                <BrandMark className="size-16" title="CodexLoom full-color mark" />
              </div>
              <div className="flex min-h-28 items-center justify-center border-b border-border bg-foreground p-5 text-background sm:border-b-0 sm:border-r">
                <BrandMark className="size-16" monochrome title="CodexLoom reversed mark" />
              </div>
              <div className="flex min-h-28 items-center justify-center p-5 text-foreground">
                <BrandMark className="size-16" monochrome title="CodexLoom monochrome mark" />
              </div>
            </div>
          </section>

          <section data-design-section="color" className="border-t border-border py-8">
            <SectionHeader index="02 / 06" title="Semantic color" description="Warm paper and system gray carry the interface. Graphite identifies commands; clear blue is reserved for selection and focus. Green marks healthy activity, ochre marks waiting, and brick red marks failure." />
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
              {colorTokens.map((token) => <ColorSample key={token.name} token={token} value={resolvedColors[token.name] || ""} />)}
            </div>
            <div className="mt-4 grid overflow-hidden rounded-lg border border-border sm:grid-cols-[180px_1fr]">
              <div className="border-b border-border bg-card p-4 sm:border-b-0 sm:border-r">
                <div className="font-mono text-[10px] text-muted-foreground">CHART PALETTE</div>
                <div className="mt-1 text-[12px] text-foreground">Use only when data needs categorical distinction.</div>
              </div>
              <div className="grid min-h-20 grid-cols-5">
                {chartTokens.map((token) => (
                  <div key={token} className="flex items-end p-2" style={{ background: `var(--${token})` }}>
                    <span className="font-mono text-[8px] text-white/80">{token.replace("chart-", "0")}</span>
                  </div>
                ))}
              </div>
            </div>
          </section>

          <section data-design-section="type" className="border-t border-border py-8">
            <SectionHeader index="03 / 06" title="Typography" description="Serif type establishes page identity. Sans-serif carries dense interface content. Monospace is reserved for IDs, paths, commands, timestamps, and machine state." />
            <div className="overflow-hidden rounded-lg border border-border bg-card">
              {[
                { token: "display", className: "font-serif text-[32px] leading-tight", text: "Durable agent organizations" },
                { token: "page title", className: "font-serif text-xl leading-6", text: "Messages" },
                { token: "section", className: "font-sans text-[16px] font-semibold", text: "Communication across domains" },
                { token: "body", className: "font-sans text-[13px] leading-5", text: "Each agent remains responsible for a stable domain and continues from prior work." },
                { token: "interface", className: "font-sans text-[12px] font-medium", text: "Send message" },
                { token: "machine", className: "font-mono text-[11px]", text: "agent_4a82 · thread/active · 14:32:08" },
              ].map((sample) => (
                <div key={sample.token} className="grid gap-2 border-b border-border px-4 py-4 last:border-b-0 sm:grid-cols-[120px_1fr] sm:items-baseline">
                  <div className="font-mono text-[9px] text-muted-foreground">{sample.token}</div>
                  <div className={sample.className}>{sample.text}</div>
                </div>
              ))}
            </div>
          </section>

          <section data-design-section="space" className="border-t border-border py-8">
            <SectionHeader index="04 / 06" title="Density, shape, and depth" description="A 4px base rhythm supports dense repeated work. Controls use 6px corners, panels use 8px, and circles are reserved for status, avatars, or genuinely round controls." />
            <div className="grid gap-6 lg:grid-cols-2">
              <div>
                <div className="mb-2 text-[11px] font-semibold text-foreground">Spacing scale</div>
                <div className="overflow-hidden rounded-lg border border-border bg-card">
                  {spacingTokens.map((token) => (
                    <div key={token.name} className="grid grid-cols-[68px_1fr_92px] items-center gap-3 border-b border-border px-3 py-2.5 last:border-b-0">
                      <span className="font-mono text-[9px] text-muted-foreground">{token.value}px</span>
                      <span className="h-2 rounded-[2px] bg-primary/70" style={{ width: `calc(var(--${token.name}) * 3)` }} />
                      <span className="truncate text-right text-[9.5px] text-muted-foreground" title={token.use}>{token.use}</span>
                    </div>
                  ))}
                </div>
              </div>
              <div>
                <div className="mb-2 text-[11px] font-semibold text-foreground">Elevation</div>
                <div className="grid grid-cols-3 gap-3 rounded-lg border border-border bg-muted/30 p-4">
                  <div className="flex h-24 items-end rounded-lg border border-border bg-card p-3 text-[10px] text-muted-foreground">Base</div>
                  <div className="shadow-card flex h-24 items-end rounded-lg border border-border bg-card p-3 text-[10px] text-muted-foreground">Card</div>
                  <div className="shadow-elevated flex h-24 items-end rounded-lg border border-border bg-card p-3 text-[10px] text-muted-foreground">Raised</div>
                </div>
              </div>
            </div>
          </section>

          <section data-design-section="components" className="border-t border-border py-8">
            <SectionHeader index="05 / 06" title="Core components" description="Commands use buttons; modes use segmented controls; binary state uses switches or checkboxes; option sets use selects. Every icon-only action requires a tooltip or accessible label." />
            <div className="grid gap-4 lg:grid-cols-2">
              <div className="overflow-hidden rounded-lg border border-border bg-card">
                <div className="border-b border-border px-4 py-3 text-[11px] font-semibold">Actions</div>
                <div className="flex flex-wrap items-center gap-2 p-4">
                  <Button><Plus />Create agent</Button>
                  <Button variant="outline">Open details</Button>
                  <Button variant="secondary">Queue</Button>
                  <Button variant="ghost">Cancel</Button>
                  <Button variant="destructive"><Trash2 />Delete</Button>
                  <Button variant="outline" size="icon" title="Inspect activity" aria-label="Inspect activity"><Activity /></Button>
                </div>
              </div>
              <div className="overflow-hidden rounded-lg border border-border bg-card">
                <div className="border-b border-border px-4 py-3 text-[11px] font-semibold">Inputs</div>
                <div className="space-y-3 p-4">
                  <label className="block">
                    <span className="mb-1 block text-[10px] font-medium text-muted-foreground">Agent name</span>
                    <div className="flex h-9 items-center gap-2 rounded-md border border-input bg-background px-2.5 focus-within:ring-2 focus-within:ring-ring/25">
                      <Search className="size-3.5 text-muted-foreground" />
                      <input defaultValue="codex-research" className="min-w-0 flex-1 bg-transparent text-[12.5px] outline-none" />
                    </div>
                  </label>
                  <div className="grid grid-cols-2 gap-2">
                    <select defaultValue="gpt-5.6-sol" className="h-9 rounded-md border border-input bg-background px-2 text-[12px] outline-none focus:ring-2 focus:ring-ring/25">
                      <option>gpt-5.6-sol</option>
                      <option>gpt-5.6-terra</option>
                    </select>
                    <select defaultValue="high" className="h-9 rounded-md border border-input bg-background px-2 text-[12px] outline-none focus:ring-2 focus:ring-ring/25">
                      <option>medium</option>
                      <option>high</option>
                      <option>xhigh</option>
                    </select>
                  </div>
                </div>
              </div>
            </div>
            <div className="mt-4 grid gap-4 lg:grid-cols-2">
              <div className="overflow-hidden rounded-lg border border-border bg-card">
                <div className="border-b border-border px-4 py-3 text-[11px] font-semibold">Runtime states</div>
                {statusSamples.map((sample) => <StatusBadge key={sample.label} sample={sample} />)}
              </div>
              <div className="overflow-hidden rounded-lg border border-border bg-card">
                <div className="border-b border-border px-4 py-3 text-[11px] font-semibold">Feedback</div>
                <div className="space-y-2 p-4">
                  <div className="flex gap-2 rounded-md border border-primary/20 bg-primary/5 p-3 text-primary"><Info className="mt-0.5 size-4 shrink-0" /><div><div className="text-[11.5px] font-semibold">Profile updated</div><div className="mt-0.5 text-[10.5px] opacity-80">The new version will be injected before the next safe turn.</div></div></div>
                  <div className="flex gap-2 rounded-md border border-warning/25 bg-warning/5 p-3 text-warning"><AlertTriangle className="mt-0.5 size-4 shrink-0" /><div><div className="text-[11.5px] font-semibold">Restart waiting</div><div className="mt-0.5 text-[10.5px] opacity-80">Two active turns must finish before the service restarts.</div></div></div>
                </div>
              </div>
            </div>
          </section>

          <section data-design-section="patterns" className="border-t border-border pt-8">
            <SectionHeader index="06 / 06" title="Operational patterns" description="Repeated work should read as stable rows and divided regions, not nested cards. Selection, health, and available actions remain visible without changing layout dimensions." />
            <div className="grid overflow-hidden rounded-lg border border-border bg-card lg:grid-cols-[1fr_280px]">
              <div className="border-b border-border lg:border-b-0 lg:border-r">
                {[
                  { name: "codex-hub-dev", domain: "CodexLoom product engineering", state: "running", dot: "bg-success" },
                  { name: "codex-research", domain: "Codex runtime and protocol research", state: "idle", dot: "bg-muted-foreground/40" },
                  { name: "parall-lead", domain: "Parall integration and collaboration", state: "queued", dot: "bg-warning" },
                ].map((agent, index) => (
                  <button key={agent.name} type="button" className={`flex h-14 w-full items-center gap-3 border-b border-border px-4 text-left last:border-b-0 ${index === 0 ? "bg-selection text-selection-foreground" : "hover:bg-muted/35"}`}>
                    <span className={`size-2 rounded-full ${agent.dot}`} />
                    <div className="min-w-0 flex-1"><div className="truncate text-[12.5px] font-semibold">{agent.name}</div><div className="truncate text-[10.5px] text-muted-foreground">{agent.domain}</div></div>
                    <span className="font-mono text-[9px] text-muted-foreground">{agent.state}</span>
                    <ChevronRight className="size-3.5 text-muted-foreground" />
                  </button>
                ))}
              </div>
              <div className="flex min-h-44 flex-col justify-between p-4">
                <div><div className="flex items-center gap-2 text-[11px] font-semibold"><CircleDot className="size-3.5 text-primary" />Selection inspector</div><p className="mt-2 text-[10.5px] leading-4 text-muted-foreground">Use an inspector for precise state and commands. Keep the list available as navigation context.</p></div>
                <div className="mt-4 flex items-center gap-2"><Button size="sm"><Check />Open agent</Button><Button size="sm" variant="outline">Message</Button></div>
              </div>
            </div>
          </section>
        </div>
      </div>
    </main>
  );
}

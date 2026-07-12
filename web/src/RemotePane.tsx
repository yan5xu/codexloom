import {
  Check,
  Clipboard,
  Laptop,
  Link2,
  LoaderCircle,
  Power,
  RefreshCw,
  ShieldCheck,
  Smartphone,
  Trash2,
  Unplug,
} from "lucide-react";
import { QRCodeSVG } from "qrcode.react";
import { useEffect, useRef, useState } from "react";
import { api, type RemoteDevice, type RemotePairing, type RemoteSnapshot } from "./types";

interface Props {
  remote: RemoteSnapshot | null;
  onUpdated: (remote: RemoteSnapshot) => void;
  onError: (message: string) => void;
}

export function RemotePane({ remote, onUpdated, onError }: Props) {
  const [devices, setDevices] = useState<RemoteDevice[]>([]);
  const [working, setWorking] = useState("");
  const [copied, setCopied] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const remoteRef = useRef(remote);
  const devicesRef = useRef(devices);

  useEffect(() => {
    remoteRef.current = remote;
  }, [remote]);

  useEffect(() => {
    devicesRef.current = devices;
  }, [devices]);

  const refresh = async () => {
    const data = await api("GET", "/api/remote");
    onUpdated(data.remote);
    return data.remote as RemoteSnapshot;
  };

  const refreshDevices = async (quiet = false) => {
    try {
      const data = await api("GET", "/api/remote/devices");
      setDevices(data.devices || []);
    } catch (error: any) {
      if (!quiet) onError(error.message);
    }
  };

  useEffect(() => {
    if (remote?.status.state === "connected") refreshDevices(true);
  }, [remote?.status.state, remote?.status.environmentId]);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    const pairing = remote?.pairing;
    if (!pairing || pairing.claimed || pairing.expiresAt * 1000 <= Date.now()) return;
    const timer = window.setInterval(async () => {
      try {
        const data = await api("GET", "/api/remote/pairing");
        const next = { ...(remoteRef.current || remote), pairing: data.pairing } as RemoteSnapshot;
        onUpdated(next);
        if (data.pairing?.claimed) refreshDevices(true);
      } catch {
        // Pairing polling stops naturally at expiry; transient failures retry.
      }
    }, 2500);
    return () => window.clearInterval(timer);
  }, [remote?.pairing?.pairingCode, remote?.pairing?.claimed, remote?.pairing?.expiresAt]);

  const run = async (key: string, task: () => Promise<void>) => {
    if (working) return;
    setWorking(key);
    try {
      await task();
    } catch (error: any) {
      onError(error.message);
      await refresh().catch(() => {});
    } finally {
      setWorking("");
    }
  };

  const enable = () => run("enable", async () => {
    const data = await api("POST", "/api/remote/enable");
    onUpdated(data.remote);
  });

  const disable = () => run("disable", async () => {
    const data = await api("POST", "/api/remote/disable");
    onUpdated(data.remote);
    setDevices([]);
  });

  const pair = () => run("pair", async () => {
    const data = await api("POST", "/api/remote/pairing");
    const snapshot = await refresh();
    onUpdated({ ...snapshot, pairing: data.pairing });
  });

  const revoke = (device: RemoteDevice) => run(`revoke:${device.clientId}`, async () => {
    await api("DELETE", `/api/remote/devices/${encodeURIComponent(device.clientId)}`);
    await refreshDevices();
  });

  const copyCode = async () => {
    const code = remote?.pairing?.manualPairingCode;
    if (!code) return;
    await navigator.clipboard.writeText(code);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  };

  const state = remote?.status.state || "disabled";
  const enabled = !!remote?.config.enabled;
  const pairing = remote?.pairing;
  const expiresIn = pairing ? Math.max(0, Math.ceil((pairing.expiresAt * 1000 - now) / 1000)) : 0;
  const pairingActive = !!pairing && !pairing.claimed && expiresIn > 0;

  useEffect(() => {
    const automationState = () => ({
      view: "remote",
      state: remoteRef.current?.status.state || "loading",
      enabled: !!remoteRef.current?.config.enabled,
      hostName: remoteRef.current?.status.serverName || remoteRef.current?.status.systemHostname || "",
      serverName: remoteRef.current?.status.serverName || "",
      environmentId: remoteRef.current?.status.environmentId || "",
      pairingActive: !!remoteRef.current?.pairing && !remoteRef.current?.pairing?.claimed,
      pairingClaimed: !!remoteRef.current?.pairing?.claimed,
      devicesCount: devicesRef.current.length,
    });
    const automation = {
      ...(window.codexLoom || window.codexHub || {}),
      remote: {
        state: automationState,
        refresh: async () => {
          await refresh();
          return waitForRemoteState(automationState);
        },
        enable: async () => {
          const data = await api("POST", "/api/remote/enable");
          onUpdated(data.remote);
          return waitForRemoteState(automationState);
        },
        disable: async () => {
          const data = await api("POST", "/api/remote/disable");
          onUpdated(data.remote);
          return waitForRemoteState(automationState);
        },
        pair: async () => {
          await pair();
          return waitForRemoteState(automationState);
        },
      },
    };
    window.codexLoom = automation;
    window.codexHub = automation;
  }, [onUpdated, remote]);

  return (
    <main className="w-full min-w-0 max-w-full flex-1 overflow-x-hidden overflow-y-auto bg-background">
      <header className="border-b border-border bg-card/80 py-4 pl-16 pr-5 md:px-8">
        <div className="mx-auto flex max-w-5xl items-center justify-between gap-4">
          <div className="min-w-0">
            <p className="font-mono text-[10px] uppercase text-muted-foreground">Connected host</p>
            <h1 className="mt-1 truncate font-serif text-2xl text-foreground">Remote</h1>
          </div>
          <StatusBadge state={state} />
        </div>
      </header>

      <div className="mx-auto w-full max-w-5xl">
        <section className="border-b border-border px-5 py-6 md:px-8">
          <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_280px]">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <Laptop className="size-4 text-primary" />
                <h2 className="text-sm font-semibold text-foreground">Host identity</h2>
              </div>
              <div className="mt-4 flex h-10 min-w-0 items-center rounded-lg border border-border bg-card px-3 font-mono text-sm text-foreground">
                <span className="truncate">{remote?.status.serverName || remote?.status.systemHostname || "-"}</span>
              </div>
              <dl className="mt-4 grid min-w-0 gap-x-6 gap-y-3 text-sm sm:grid-cols-2">
                <IdentityRow label="System hostname" value={remote?.status.systemHostname || "-"} />
                <IdentityRow label="Remote server" value={remote?.status.serverName || "-"} />
                <IdentityRow label="Environment" value={remote?.status.environmentId || "Not enrolled"} />
                <IdentityRow label="Installation" value={remote?.status.installationId || "-"} />
              </dl>
              {remote?.status.lastError && (
                <div className="mt-4 rounded-lg border border-destructive/25 bg-destructive/8 px-3 py-2 text-sm text-destructive">
                  {remote.status.lastError}
                </div>
              )}
            </div>

            <div className="flex items-start justify-start lg:justify-end">
              {enabled ? (
                <button
                  onClick={disable}
                  disabled={!!working}
                  className="flex h-10 w-full items-center justify-center gap-2 rounded-lg border border-destructive/30 bg-destructive/8 px-4 text-sm font-medium text-destructive hover:bg-destructive/12 disabled:opacity-50 lg:w-auto"
                >
                  {working === "disable" ? <LoaderCircle className="size-4 animate-spin" /> : <Unplug className="size-4" />}
                  Disable Remote
                </button>
              ) : (
                <button
                  onClick={enable}
                  disabled={!!working}
                  className="flex h-10 w-full items-center justify-center gap-2 rounded-lg bg-primary px-4 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50 lg:w-auto"
                >
                  {working === "enable" ? <LoaderCircle className="size-4 animate-spin" /> : <Power className="size-4" />}
                  Enable Remote
                </button>
              )}
            </div>
          </div>
        </section>

        <section className="border-b border-border px-5 py-6 md:px-8">
          <div className="flex flex-col items-stretch gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-center gap-2">
              <Link2 className="size-4 text-primary" />
              <h2 className="text-sm font-semibold text-foreground">Pair device</h2>
            </div>
            <button
              onClick={pair}
              disabled={!enabled || !remote?.status.environmentId || !!working}
              className="flex h-9 w-full items-center justify-center gap-2 rounded-lg border border-border bg-card px-3 text-sm font-medium hover:border-primary/50 disabled:cursor-not-allowed disabled:opacity-45 sm:w-auto"
            >
              {working === "pair" ? <LoaderCircle className="size-4 animate-spin" /> : <Smartphone className="size-4" />}
              New pairing
            </button>
          </div>

          {pairing && (
            <div className="mt-5 grid items-center gap-6 md:grid-cols-[180px_minmax(0,1fr)]">
              <div className="flex size-[180px] items-center justify-center rounded-lg border border-border bg-white p-3">
                <QRCodeSVG value={pairing.pairingCode} size={154} level="M" />
              </div>
              <div className="min-w-0">
                <div className="flex items-center gap-2 text-sm font-medium">
                  {pairing.claimed ? (
                    <><ShieldCheck className="size-4 text-success" /> Paired</>
                  ) : pairingActive ? (
                    <><LoaderCircle className="size-4 animate-spin text-warning" /> Waiting · {formatCountdown(expiresIn)}</>
                  ) : (
                    <><Unplug className="size-4 text-muted-foreground" /> Expired</>
                  )}
                </div>
                <button
                  onClick={copyCode}
                  disabled={!pairing.manualPairingCode}
                  title="Copy manual pairing code"
                  className="mt-4 flex max-w-full items-center gap-3 rounded-lg border border-border bg-card px-3 py-2 text-left hover:border-primary/50 disabled:opacity-50"
                >
                  <span className="min-w-0 break-all font-mono text-lg font-semibold text-foreground">
                    {pairing.manualPairingCode || "No manual code"}
                  </span>
                  {copied ? <Check className="size-4 shrink-0 text-success" /> : <Clipboard className="size-4 shrink-0 text-muted-foreground" />}
                </button>
                <div className="mt-3 truncate font-mono text-[11px] text-muted-foreground" title={pairing.environmentId}>
                  {pairing.environmentId}
                </div>
              </div>
            </div>
          )}
        </section>

        <section className="px-5 py-6 md:px-8">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2">
              <ShieldCheck className="size-4 text-primary" />
              <h2 className="text-sm font-semibold text-foreground">Paired devices</h2>
              <span className="font-mono text-xs text-muted-foreground">{devices.length}</span>
            </div>
            <button
              onClick={() => refreshDevices()}
              disabled={state !== "connected" || !!working}
              title="Refresh devices"
              className="flex size-9 items-center justify-center rounded-lg border border-border bg-card hover:border-primary/50 disabled:opacity-45"
            >
              <RefreshCw className="size-4" />
            </button>
          </div>

          <div className="mt-4 divide-y divide-border border-y border-border">
            {devices.map((device) => (
              <div key={device.clientId} className="flex min-w-0 items-center gap-3 py-3">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-secondary text-muted-foreground">
                  {device.deviceType === "mobile" ? <Smartphone className="size-4" /> : <Laptop className="size-4" />}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium text-foreground">
                    {device.displayName || device.deviceModel || device.clientId}
                  </div>
                  <div className="mt-0.5 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    <span>{[device.platform, device.osVersion].filter(Boolean).join(" ") || "Unknown platform"}</span>
                    {device.appVersion && <span>App {device.appVersion}</span>}
                    {device.lastSeenAt && <span>{formatLastSeen(device.lastSeenAt)}</span>}
                  </div>
                </div>
                <button
                  onClick={() => revoke(device)}
                  disabled={!!working}
                  title="Revoke device"
                  className="flex size-9 shrink-0 items-center justify-center rounded-lg text-muted-foreground hover:bg-destructive/8 hover:text-destructive disabled:opacity-50"
                >
                  {working === `revoke:${device.clientId}` ? <LoaderCircle className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
                </button>
              </div>
            ))}
            {devices.length === 0 && (
              <div className="py-8 text-center text-sm text-muted-foreground">No paired devices</div>
            )}
          </div>
        </section>
      </div>
    </main>
  );
}

function StatusBadge({ state }: { state: string }) {
  const active = state === "connected";
  const pending = state === "starting" || state === "connecting";
  const failed = state === "error" || state === "errored";
  return (
    <div className={`flex h-8 shrink-0 items-center gap-2 rounded-lg border px-3 text-xs font-medium ${
      active
        ? "border-success/25 bg-success/8 text-success"
        : pending
          ? "border-warning/25 bg-warning/8 text-warning"
          : failed
            ? "border-destructive/25 bg-destructive/8 text-destructive"
            : "border-border bg-secondary text-muted-foreground"
    }`}>
      <span className={`size-2 rounded-full ${active ? "bg-success" : pending ? "animate-pulse bg-warning" : failed ? "bg-destructive" : "bg-muted-foreground/40"}`} />
      {stateLabel(state)}
    </div>
  );
}

function IdentityRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 truncate font-mono text-xs text-foreground" title={value}>{value}</dd>
    </div>
  );
}

function stateLabel(state: string) {
  if (state === "connected") return "Connected";
  if (state === "starting") return "Starting";
  if (state === "connecting") return "Connecting";
  if (state === "error" || state === "errored") return "Error";
  return "Disabled";
}

function formatCountdown(seconds: number) {
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return `${minutes}:${String(rest).padStart(2, "0")}`;
}

function formatLastSeen(value: number) {
  const date = new Date(value * 1000);
  if (Number.isNaN(date.getTime())) return "";
  return `Seen ${date.toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}`;
}

function waitForRemoteState(state: () => unknown) {
  return new Promise((resolve) => window.setTimeout(() => resolve(state()), 100));
}

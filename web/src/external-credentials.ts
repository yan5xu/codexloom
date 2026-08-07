import type { AgentAddress, PlatformConnection } from "./types";

export type CredentialSourceKind = "managed" | "keychain" | "environment" | "missing" | "unknown";

export type CredentialSourcePresentation = {
  kind: CredentialSourceKind;
  label: string;
  detail: string;
  tone: string;
};

export function credentialSourceKind(value = ""): CredentialSourceKind {
  const ref = value.trim();
  if (!ref) return "missing";
  if (ref.startsWith("managed:") && ref.length > "managed:".length) return "managed";
  if (ref.startsWith("keychain:") && ref.length > "keychain:".length) return "keychain";
  if (ref.startsWith("env:") && ref.length > "env:".length) return "environment";
  return "unknown";
}

export function isLegacyCredentialRef(value = "") {
  const kind = credentialSourceKind(value);
  return kind === "keychain" || kind === "environment";
}

export function credentialSourcePresentation(value = ""): CredentialSourcePresentation {
  switch (credentialSourceKind(value)) {
    case "managed":
      return {
        kind: "managed",
        label: "CodexLoom managed credential",
        detail: "Hub-issued opaque reference. Its managed credential material is not shown here or included in ordinary backups.",
        tone: "border-success bg-success/5 text-success",
      };
    case "keychain":
      return {
        kind: "keychain",
        label: "Legacy Keychain credential",
        detail: "Compatibility source. The secret remains hidden. Automatic and manual restart are unavailable until this Connection is migrated to a managed credential.",
        tone: "border-warning bg-warning/5 text-warning",
      };
    case "environment":
      return {
        kind: "environment",
        label: "Legacy environment credential",
        detail: "Compatibility source. The environment value remains hidden. Automatic and manual restart are unavailable until this Connection is migrated to a managed credential.",
        tone: "border-warning bg-warning/5 text-warning",
      };
    case "missing":
      return {
        kind: "missing",
        label: "No credential reference",
        detail: "Complete onboarding before this connection can run.",
        tone: "border-border bg-muted/30 text-muted-foreground",
      };
    default:
      return {
        kind: "unknown",
        label: "Unknown credential source",
        detail: "Recovery is disabled because this reference is not part of the supported public contract.",
        tone: "border-destructive bg-destructive/5 text-destructive",
      };
  }
}

// This is browser eligibility for showing terminal repair guidance, not proof
// that provider/socket preflight passed. The explicit-token CLI performs those
// protected checks again before any repair side effect.
export function canOfferParallRepairCLI(connection: PlatformConnection, address?: AgentAddress) {
  const source = credentialSourceKind(connection.credentialRef);
  if (source !== "managed") return false;
  if (connection.provider !== "parall" || connection.archivedAt || !connection.enabled) return false;
  if (connection.status !== "disconnected" && connection.status !== "degraded") return false;
  if (!address || address.connectionId !== connection.id || address.archivedAt || address.deletedAt || !address.enabled) return false;
  const externalID = externalIdentityID(address.externalIdentity);
  return Boolean(connection.accountRef && address.agentId && externalID);
}

export function gatewayCommand(connection: PlatformConnection, address: AgentAddress, identity: { slackAppID?: string } = {}) {
  const credentialRef = connection.credentialRef?.trim() || "";
  const source = credentialSourceKind(credentialRef);
  if (
    (source !== "keychain" && source !== "environment")
    || !connection.id
    || !connection.enabled
    || connection.archivedAt
    || !address.id
    || !address.enabled
    || address.archivedAt
    || address.deletedAt
    || address.connectionId !== connection.id
  ) return "";
  if (connection.provider === "lark") {
    const appID = (connection.accountRef || "").trim();
    if (!appID) return "";
    if (source === "keychain" && credentialRef !== `keychain:com.codexloom.feishu.${appID}`) return "";
    return `bin/loom-feishu-gateway --connection ${shellArg(connection.id)} --address ${shellArg(address.id)} --app-id ${shellArg(appID)} --credential-ref ${shellArg(credentialRef)}`;
  }
  if (connection.provider === "slack") {
    const app = identity.slackAppID?.trim() || "";
    const bot = externalIdentityID(address.externalIdentity).trim();
    const team = (connection.accountRef || "").trim();
    if (source !== "keychain" || !app || !bot || !team || credentialRef !== `keychain:com.codexloom.slack.${app}`) return "";
    return `bin/loom-slack-gateway --connection ${shellArg(connection.id)} --address ${shellArg(address.id)} --app-id ${shellArg(app)} --bot-user-id ${shellArg(bot)} --team-id ${shellArg(team)} --credential-ref ${shellArg(credentialRef)}`;
  }
  if (connection.provider === "parall") {
    const org = (connection.accountRef || "").trim();
    const agent = externalIdentityID(address.externalIdentity).trim();
    if (!org || !agent) return "";
    if (source === "keychain" && credentialRef !== `keychain:com.codexloom.parall.agent.${org}.${agent}`) return "";
    return `bin/loom-parall-gateway --connection ${shellArg(connection.id)} --address ${shellArg(address.id)} --org-id ${shellArg(org)} --agent-id ${shellArg(agent)} --credential-ref ${shellArg(credentialRef)}`;
  }
  return "";
}

export function externalIdentityID(value: string) {
  const index = value.lastIndexOf("/");
  return index >= 0 ? value.slice(index + 1) : value;
}

function shellArg(value: string) {
  return `'${value.replaceAll("'", `'\\''`)}'`;
}

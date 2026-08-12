import type { Agent } from "./types";

const agentNameCollator = new Intl.Collator("en", {
  numeric: true,
  sensitivity: "base",
  usage: "sort",
});

const agentIDCollator = new Intl.Collator("en", {
  numeric: true,
  sensitivity: "variant",
  usage: "sort",
});

export function compareAgentDirectoryEntries(left: Agent, right: Agent) {
  const byName = agentNameCollator.compare(left.name, right.name);
  if (byName !== 0) return byName;
  return agentIDCollator.compare(left.id, right.id);
}

export function sortAgentDirectory(agents: Agent[]) {
  return [...agents].sort(compareAgentDirectoryEntries);
}

function searchableAgentText(agent: Agent) {
  return [agent.name, agent.displayName || ""].join(" ").normalize("NFKC").toLocaleLowerCase("en-US");
}

export function filterAgentDirectory(agents: Agent[], query: string) {
  const terms = query
    .normalize("NFKC")
    .toLocaleLowerCase("en-US")
    .trim()
    .split(/\s+/)
    .filter(Boolean);
  const sorted = sortAgentDirectory(agents);
  if (terms.length === 0) return sorted;
  return sorted.filter((agent) => {
    const text = searchableAgentText(agent);
    return terms.every((term) => text.includes(term));
  });
}

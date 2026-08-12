export interface AgentLabelSource {
  name: string;
  displayName?: string;
}

export function agentLabel(agent: AgentLabelSource): string {
  return agent.displayName?.trim() || agent.name;
}

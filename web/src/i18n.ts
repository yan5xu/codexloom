import i18n from "i18next";
import { initReactI18next } from "react-i18next";

export const LANGUAGE_STORAGE_KEY = "codexloom-language";
export type AppLanguage = "en" | "zh-CN";

export function normalizeLanguage(value?: string | null): AppLanguage {
  return value?.toLowerCase().startsWith("en") ? "en" : "zh-CN";
}

function browserStorage(): Storage | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage || null;
  } catch {
    return null;
  }
}

function initialLanguage(): AppLanguage {
  const stored = browserStorage()?.getItem(LANGUAGE_STORAGE_KEY);
  // CodexLoom reviews Owner-facing product language in Simplified Chinese
  // first, so a fresh installation opens in that canonical language.
  return stored ? normalizeLanguage(stored) : "zh-CN";
}

i18n.use(initReactI18next).init({
  lng: initialLanguage(),
  fallbackLng: "en",
  supportedLngs: ["en", "zh-CN"],
  interpolation: { escapeValue: false },
  resources: {
    en: {
      translation: {
        agent: {
          agent: "Agent",
          you: "You",
          streaming: "streaming",
          input: "Input",
          result: "Result",
          cachedPct: " ({{pct}}% cached)",
          tokenUsageCloud: "Token: {{prompt}} prompt{{cache}} + {{completion}} output",
          promptPlaceholder: "Send a task to {{name}}…",
        },
        shell: {
          needsYou: "Needs You",
          topics: "Topics",
          overview: "Overview",
          team: "Team",
          external: "External",
          agents: "Agents",
          newAgent: "New agent",
          settings: "Settings",
          switchLanguage: "切换到中文",
          empty: "Select or create an agent to begin.",
          createTitle: "Create agent",
          createDescription: "Create a long-lived domain agent backed by a Codex Thread.",
          agentName: "Agent name",
          displayName: "Display name",
          displayNamePlaceholder: "Short drama adapter",
          internalName: "Internal identifier",
          internalNameHelp: "Used by APIs and CLI; only letters, numbers, hyphens, and underscores.",
          workingDirectory: "Working directory",
          domain: "Domain",
          optional: "optional",
          domainPlaceholder: "The enduring subject this Agent will maintain",
          creating: "Creating",
          create: "Create agent",
        },
      },
    },
    "zh-CN": {
      translation: {
        agent: {
          agent: "Agent",
          you: "你",
          streaming: "生成中",
          input: "输入",
          result: "结果",
          cachedPct: "（{{pct}}% 已缓存）",
          tokenUsageCloud: "Token：{{prompt}} 输入{{cache}} + {{completion}} 输出",
          promptPlaceholder: "向 {{name}} 发送任务…",
        },
        shell: {
          needsYou: "需要你决定",
          topics: "协作事项",
          overview: "概览",
          team: "团队",
          external: "外部连接",
          agents: "Agents",
          newAgent: "新建 Agent",
          settings: "设置",
          switchLanguage: "Switch to English",
          empty: "选择或创建一个 Agent 开始工作。",
          createTitle: "创建 Agent",
          createDescription: "创建一个由 Codex Thread 支撑的长期领域 Agent。",
          agentName: "Agent 名称",
          displayName: "显示名称",
          displayNamePlaceholder: "短篇改编",
          internalName: "内部标识",
          internalNameHelp: "供 API 和命令行使用，仅支持英文字母、数字、连字符和下划线。",
          workingDirectory: "工作目录",
          domain: "领域职责",
          optional: "可选",
          domainPlaceholder: "这个 Agent 将长期负责的领域",
          creating: "创建中",
          create: "创建 Agent",
        },
      },
    },
  },
});

function applyDocumentLanguage(language: string) {
  if (typeof document === "undefined") return;
  document.documentElement.lang = normalizeLanguage(language);
}

applyDocumentLanguage(i18n.resolvedLanguage || i18n.language);
i18n.on("languageChanged", (language) => {
  const normalized = normalizeLanguage(language);
  browserStorage()?.setItem(LANGUAGE_STORAGE_KEY, normalized);
  applyDocumentLanguage(normalized);
});

export default i18n;

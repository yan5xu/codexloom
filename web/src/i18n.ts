// Minimal i18n for the ported MessageBubbles (English only). Provides just the
// keys those components reference; CodexLoom is currently single-locale.
import i18n from "i18next";
import { initReactI18next } from "react-i18next";

i18n.use(initReactI18next).init({
  lng: "en",
  fallbackLng: "en",
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
      },
    },
  },
});

export default i18n;

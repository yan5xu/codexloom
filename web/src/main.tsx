import "./i18n";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "./index.css";
import App from "./App";

function enableIPadComposerLayout() {
  const isIPad = /iPad/i.test(navigator.userAgent)
    || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  if (!isIPad) return;

  const root = document.documentElement;
  root.classList.add("loom-ipad");
  const update = () => {
    const composerFocused = document.activeElement?.matches('textarea[aria-label="task message"]') || false;
    root.classList.toggle("loom-composer-focused", composerFocused);
  };

  document.addEventListener("focusin", update);
  document.addEventListener("focusout", () => window.setTimeout(update, 0));
}

enableIPadComposerLayout();

function reloadAfterStaleChunk() {
  const key = "codexloom-stale-chunk-reload";
  const lastReload = Number(sessionStorage.getItem(key) || "0");
  if (Date.now() - lastReload < 10_000) return;
  sessionStorage.setItem(key, String(Date.now()));
  window.location.reload();
}

window.addEventListener("vite:preloadError", (event) => {
  event.preventDefault();
  reloadAfterStaleChunk();
});

window.addEventListener("unhandledrejection", (event) => {
  const message = String(event.reason?.message || event.reason || "");
  if (!/Failed to fetch dynamically imported module|Importing a module script failed/i.test(message)) return;
  event.preventDefault();
  reloadAfterStaleChunk();
});

if (localStorage.getItem("codexloom-theme") === "dark") {
  document.documentElement.classList.add("dark");
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      refetchOnWindowFocus: false,
      retry: 1,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);

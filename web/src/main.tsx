import "./i18n";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "./index.css";
import App from "./App";
import { isChunkLoadError, requestWorkspaceReload } from "./workspace-recovery";
import { installUiLocalization } from "./ui-localization";

function enableAppleComposerLayout() {
  const isIPad = /iPad/i.test(navigator.userAgent)
    || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  const isIPhone = /iPhone|iPod/i.test(navigator.userAgent);
  if (!isIPad && !isIPhone) return;

  const root = document.documentElement;
  root.classList.add(isIPad ? "loom-ipad" : "loom-iphone");
  const update = () => {
    const composerFocused = document.activeElement?.matches('textarea[aria-label="task message"]') || false;
    root.classList.toggle("loom-composer-focused", composerFocused);
  };

  document.addEventListener("focusin", update);
  document.addEventListener("focusout", () => window.setTimeout(update, 0));
}

enableAppleComposerLayout();
installUiLocalization();

window.addEventListener("vite:preloadError", (event) => {
  event.preventDefault();
  requestWorkspaceReload();
});

window.addEventListener("unhandledrejection", (event) => {
  if (!isChunkLoadError(event.reason)) return;
  event.preventDefault();
  requestWorkspaceReload();
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

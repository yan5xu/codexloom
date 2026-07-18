import "./i18n";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "./index.css";
import App from "./App";

function syncVisualViewportHeight() {
  const update = () => {
    const viewport = window.visualViewport;
    const height = Math.round(viewport?.height || window.innerHeight);
    const offsetTop = Math.round(viewport?.offsetTop || 0);
    document.documentElement.style.setProperty("--loom-viewport-height", `${height}px`);
    document.documentElement.style.setProperty("--loom-viewport-offset-top", `${offsetTop}px`);
  };

  update();
  window.addEventListener("resize", update);
  window.addEventListener("orientationchange", update);
  window.visualViewport?.addEventListener("resize", update);
  window.visualViewport?.addEventListener("scroll", update);
}

syncVisualViewportHeight();

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

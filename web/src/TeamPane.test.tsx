import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { resetGlobalEventsForTests } from "./global-events";
import { TeamPane } from "./TeamPane";

class NoopResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function memoryStorage(): Storage {
  const values = new Map<string, string>();
  return {
    get length() { return values.size; },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => Array.from(values.keys())[index] ?? null,
    removeItem: (key) => values.delete(key),
    setItem: (key, value) => values.set(key, String(value)),
  };
}

afterEach(() => {
  cleanup();
  resetGlobalEventsForTests();
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "#team");
  delete window.codexLoom;
  delete window.codexHub;
});

describe("TeamPane collaboration groups", () => {
  it.each([
    ["legacy null", null],
    ["missing", undefined],
    ["normal empty array", []],
  ])("renders and edits a member-only group with %s relationship ids", async (_, relationshipIds) => {
    vi.stubGlobal("ResizeObserver", NoopResizeObserver);
    vi.stubGlobal("localStorage", memoryStorage());
    window.history.replaceState(null, "", "#team?view=collaboration");
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = typeof input === "string" ? input : input instanceof URL ? `${input.pathname}${input.search}` : input.url;
      const body = path.startsWith("/api/team/activity")
        ? { observedLinks: [] }
        : {
            team: {
              agents: [],
              organizationLinks: [],
              collaborationLinks: [],
              collaborationGroups: [{
                id: "cgrp-members",
                name: "Member-only",
                description: "A group with members and no included edges.",
                status: "active",
                memberAgentIds: ["agent-member"],
                relationshipIds,
                version: 1,
                createdAt: "now",
                updatedAt: "now",
              }],
              observedLinks: [],
              explicitLinks: [],
            },
          };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    const onError = vi.fn();

    render(
      <TeamPane
        onError={onError}
        onMessageAgent={vi.fn()}
        onScheduleAgent={vi.fn()}
        onOpenMessages={vi.fn()}
      />,
    );

    expect(await screen.findByText("1 members · 0 included collaboration edges")).toBeVisible();

    fireEvent.change(screen.getByLabelText("Collaboration group"), { target: { value: "cgrp-members" } });
    const groupHeading = await screen.findByRole("heading", { name: "Member-only" });
    expect(groupHeading).toBeVisible();
    expect(groupHeading.closest("aside")).toHaveClass("lg:min-h-0");
    expect(groupHeading.closest("aside")).not.toHaveClass("min-h-0");
    expect(screen.getByText("No collaboration edge is included in this group.")).toBeVisible();
    expect(screen.getByText("Included collaboration edges")).toHaveTextContent(/Included collaboration edges\s*0/);

    fireEvent.click(screen.getByTitle("Edit collaboration group"));
    expect(screen.getByText("Edit group")).toBeVisible();
    expect(screen.getByText("Declared edges")).toHaveTextContent(/Declared edges\s*0/);
    expect(screen.getByText("Members")).toHaveTextContent(/Members\s*1/);
    await waitFor(() => expect(onError).not.toHaveBeenCalled());
  });
});

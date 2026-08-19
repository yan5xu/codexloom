import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { resetGlobalEventsForTests } from "./global-events";
import { InboxPane } from "./InboxPane";
import type { Agent } from "./types";

const agent = { id: "agent-1", name: "research" } as Agent;

afterEach(() => {
	cleanup();
	resetGlobalEventsForTests();
	vi.unstubAllGlobals();
});

describe("Inbox accessibility", () => {
	it("names compose controls and exposes the expanded state", async () => {
		mockInboxAPI();
		render(<InboxPane agents={[agent]} onError={vi.fn()} />);

		const compose = screen.getByRole("button", { name: "New outbound message" });
		expect(compose).toHaveAttribute("aria-expanded", "false");
		fireEvent.click(compose);

		expect(compose).toHaveAttribute("aria-expanded", "true");
		expect(compose).toHaveAttribute("aria-controls", "outbound-compose");
		expect(screen.getByRole("combobox", { name: "Sending agent" })).toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Authorized destination" })).toBeInTheDocument();
		expect(screen.getByRole("textbox", { name: "Message" })).toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Filter by source" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Refresh Inbox" })).toBeInTheDocument();
	});

	it("submits an outbound message through the compose form", async () => {
		const fetch_mock = mockInboxAPI();
		vi.stubGlobal("crypto", { randomUUID: () => "request-1" });
		render(<InboxPane agents={[agent]} onError={vi.fn()} />);

		fireEvent.click(screen.getByRole("button", { name: "New outbound message" }));
		fireEvent.change(screen.getByRole("combobox", { name: "Sending agent" }), {
			target: { value: "research" },
		});
		await screen.findByRole("option", { name: "Research room" });
		fireEvent.change(screen.getByRole("combobox", { name: "Authorized destination" }), {
			target: { value: "membership-1" },
		});
		fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
			target: { value: "Status update" },
		});
		fireEvent.submit(screen.getByRole("form", { name: "New outbound message" }));

		await waitFor(() => {
			expect(fetch_mock).toHaveBeenCalledWith(
				"/api/integrations/send",
				expect.objectContaining({ method: "POST" }),
			);
		});
	});
});

function mockInboxAPI() {
	return vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
		const path = String(input);
		const payloads: Record<string, unknown> = {
			"/api/inbox": { entries: [] },
			"/api/outbox": { items: [] },
			"/api/integrations/addresses": {
				addresses: [{ id: "address-1", agentId: "agent-1", enabled: true }],
			},
			"/api/integrations/conversations": {
				memberships: [{
					id: "membership-1",
					addressId: "address-1",
					conversationId: "conversation-1",
					displayName: "Research room",
					enabled: true,
					outboundPolicy: "proactive",
				}],
			},
		};
		return new Response(JSON.stringify(payloads[path] || {}), {
			status: 200,
			headers: { "Content-Type": "application/json" },
		});
	});
}

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { GlobalErrorToast } from "./App";

describe("global error feedback", () => {
	it("keeps an assertive live region mounted for error messages", () => {
		const { rerender } = render(<GlobalErrorToast message={null} />);
		const alert = screen.getByRole("alert");

		expect(alert).toHaveAttribute("aria-live", "assertive");
		expect(alert).toHaveAttribute("aria-atomic", "true");
		expect(alert).toHaveClass("sr-only");

		rerender(<GlobalErrorToast message="Unable to save" />);
		expect(alert).toHaveTextContent("Unable to save");
		expect(alert).not.toHaveClass("sr-only");
	});
});

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MarkdownContent } from "./markdown";
import { encodePlantUMLBytes, plantUMLImageURL, PLANTUML_SERVER_URL } from "./plantuml";

afterEach(cleanup);

describe("PlantUML URL encoding", () => {
  it("uses the PlantUML alphabet without logging or persisting source", () => {
    expect(encodePlantUMLBytes(Uint8Array.from([0, 1, 2, 61, 62, 63, 255]))).toMatch(/^[0-9A-Za-z_-]+$/);
  });

  it("builds an official PlantUML Server URL", async () => {
    await expect(plantUMLImageURL("@startuml\nAlice -> Bob: hello\n@enduml")).resolves.toMatch(new RegExp(`^${PLANTUML_SERVER_URL}`));
  });
});

describe("MarkdownContent PlantUML renderer", () => {
  it("renders plantuml and puml fences, while ordinary code remains code", async () => {
    render(
      <MarkdownContent
        content={'```plantuml\n@startuml\nAlice -> Bob: hello\n@enduml\n```\n\n```puml\n@startuml\nBob -> Alice: reply\n@enduml\n```\n\n```text\nplantuml\n```'}
      />,
    );

    expect(document.querySelectorAll("[data-plantuml-block]")).toHaveLength(2);
    expect(screen.getAllByText("Loading PlantUML diagram…")).toHaveLength(2);
    expect(screen.getByText("plantuml")).toBeInTheDocument();
    await waitFor(() => expect(document.querySelectorAll("[data-plantuml-block]")).toHaveLength(2));
  });

  it("keeps incomplete streamed fences as source code", () => {
    const { container } = render(<MarkdownContent content={'```plantuml\n@startuml\nAlice -> Bob'} streaming />);
    expect(document.querySelector("[data-plantuml-block]")).not.toBeInTheDocument();
    expect(container.querySelector("code")?.textContent).toContain("Alice -> Bob");
  });

  it("shows retry and source fallback when the Server image fails", async () => {
    render(<MarkdownContent content={'```plantuml\n@startuml\nAlice -> Bob: retry\n@enduml\n```'} />);
    const image = await screen.findByRole("img", { name: "PlantUML diagram" });
    fireEvent.error(image);

    expect(await screen.findByRole("alert")).toHaveTextContent("could not be loaded");
    expect(screen.getByRole("button", { name: "Retry PlantUML diagram" })).toBeInTheDocument();
    fireEvent.click(screen.getByText("Show PlantUML source"));
    expect(screen.getByRole("figure").querySelector("pre")?.textContent).toBe("@startuml\nAlice -> Bob: retry\n@enduml");
  });
});

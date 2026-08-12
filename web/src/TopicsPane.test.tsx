import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TopicCurrentArtifacts, TopicEvidenceAnchor, TopicScope } from "./TopicsPane";

const artifact = (id: string, createdAt: string) => ({
  id,
  agentId: "maker",
  name: `${id}.png`,
  mimeType: "image/png",
  size: 1024,
  sha256: id,
  path: "",
  url: `/api/topics/tpc_1/artifacts/${id}`,
  createdAt,
  publishedAt: createdAt,
});

describe("TopicCurrentArtifacts", () => {
  it("shows recent linked artifacts with preview directly in the Current view", () => {
    const onViewAll = vi.fn();
    const links = Array.from({ length: 7 }, (_, index) => ({
      type: "artifact",
      id: `art_${index}`,
      relation: "evidence",
      label: `Evidence ${index}`,
      linkedBy: "lead",
      createdAt: `2026-07-29T08:0${index}:00Z`,
    }));
    const artifacts = new Map(links.map((link) => [link.id, artifact(link.id, link.createdAt)]));

    render(
      <TopicCurrentArtifacts
        links={links}
        artifacts={artifacts}
        onLink={vi.fn()}
        onViewAll={onViewAll}
      />,
    );

    expect(screen.getByRole("region", { name: "Topic artifacts" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /Preview Evidence/ })).toHaveLength(6);
    expect(screen.getByRole("button", { name: "Preview Evidence 6" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Preview Evidence 0" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "View all 7 in History" }));
    expect(onViewAll).toHaveBeenCalledOnce();
  });
});

describe("TopicScope", () => {
  it("keeps the durable purpose and completion boundary visible without disclosure controls", () => {
    const { container } = render(
      <TopicScope
        purpose="Coordinate the bounded launch across long-lived Agents."
        completionBoundary="The Responsible Agent publishes the verified result."
      />,
    );

    expect(screen.getByRole("region", { name: "Topic scope" })).toBeInTheDocument();
    expect(screen.getByText("Coordinate the bounded launch across long-lived Agents.")).toBeVisible();
    expect(screen.getByText("The Responsible Agent publishes the verified result.")).toBeVisible();
    expect(container.querySelector("details")).toBeNull();
  });
});

describe("TopicEvidenceAnchor", () => {
  it("opens the shared artifact preview for a resolved Topic artifact", () => {
    render(
      <TopicEvidenceAnchor
        link={{
          type: "artifact",
          id: "art_image",
          relation: "evidence",
          label: "Capacity evidence",
          linkedBy: "lead",
          createdAt: "2026-07-29T08:00:00Z",
        }}
        artifact={{
          id: "art_image",
          agentId: "maker",
          name: "capacity.png",
          mimeType: "image/png",
          size: 1024,
          sha256: "abc",
          path: "",
          url: "/api/topics/tpc_1/artifacts/art_image",
          createdAt: "2026-07-29T08:00:00Z",
          publishedAt: "2026-07-29T08:01:00Z",
        }}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Preview Capacity evidence" }));
    expect(screen.getByRole("img", { name: "Capacity evidence" })).toHaveAttribute(
      "src",
      "/api/topics/tpc_1/artifacts/art_image?preview=1",
    );
    expect(screen.getByRole("link", { name: "Download Capacity evidence" })).toHaveAttribute(
      "href",
      "/api/topics/tpc_1/artifacts/art_image?download=1",
    );
  });

  it("keeps unresolved artifact links visible as plain evidence anchors", () => {
    render(
      <TopicEvidenceAnchor
        link={{
          type: "artifact",
          id: "art_missing",
          relation: "evidence",
          label: "Missing evidence",
          linkedBy: "lead",
          createdAt: "2026-07-29T08:00:00Z",
        }}
      />,
    );

    expect(screen.getByText("Missing evidence")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Preview/ })).not.toBeInTheDocument();
  });
});

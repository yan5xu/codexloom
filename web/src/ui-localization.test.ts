import { afterEach, describe, expect, it } from "vitest";
import { localizeUi, translateUiText } from "./ui-localization";

afterEach(() => {
  document.body.innerHTML = "";
});

describe("UI localization", () => {
  it("translates exact labels in both directions", () => {
    expect(translateUiText("Overview", "zh-CN")).toBe("概览");
    expect(translateUiText("概览", "en")).toBe("Overview");
    expect(translateUiText("  Create agent\n", "zh-CN")).toBe("  创建 Agent\n");
  });

  it("translates supported dynamic UI labels", () => {
    expect(translateUiText("3 agents", "zh-CN")).toBe("3 个 Agent");
    expect(translateUiText("2 ready or unavailable", "zh-CN")).toBe("2 个就绪或不可用");
    expect(translateUiText("1 active Agents · 12m execution · 34 tokens · 5 Turns", "zh-CN")).toBe(
      "1 个活跃 Agent · 执行 12 分钟 · 34 Tokens · 5 Turns",
    );
    expect(translateUiText("Send a task to research…", "zh-CN")).toBe("向 research 发送任务…");
    expect(translateUiText("向 research 发送任务…", "en")).toBe("Send a task to research…");
    expect(translateUiText("1 responsible · 2 participants", "zh-CN")).toBe("1 个负责人 · 2 个参与者");
    expect(translateUiText("brief v3 · Topic v7", "zh-CN")).toBe("简报 v3 · 协作事项 v7");
    expect(translateUiText("Continue this Topic with research…", "zh-CN")).toBe("与 research 继续此协作事项…");
    expect(translateUiText("Archive research", "zh-CN")).toBe("归档 research");
    expect(translateUiText("Close research tab", "zh-CN")).toBe("关闭 research 标签页");
  });

  it("covers the collaboration Topic workflow in Chinese", () => {
    expect(translateUiText("For you", "zh-CN")).toBe("待你处理");
    expect(translateUiText("waiting", "zh-CN")).toBe("等待中");
    expect(translateUiText("Topic detail", "zh-CN")).toBe("协作事项详情");
    expect(translateUiText("The Responsible Agent has not recorded a next step.", "zh-CN")).toBe("负责人 Agent 尚未记录下一步。");
    expect(translateUiText("Send Topic input to responsible Agent", "zh-CN")).toBe("向负责人 Agent 发送协作事项输入");
    expect(translateUiText(" · brief v", "zh-CN")).toBe(" · 简报 v");
    expect(translateUiText("1 responsible · ", "zh-CN")).toBe("1 个负责人 · ");
    expect(translateUiText(" participants", "zh-CN")).toBe(" 个参与者");
  });

  it("localizes labels and attributes while preserving authored content", () => {
    document.body.innerHTML = `
      <main>
        <h1>External</h1>
        <button title="Create agent" aria-label="Create agent">New agent</button>
        <input placeholder="The enduring subject this Agent will maintain" />
        <div data-i18n-preserve><p>Overview</p></div>
        <pre>Result</pre>
      </main>
    `;

    localizeUi(document.body, "zh-CN");

    expect(document.querySelector("h1")?.textContent).toBe("外部连接");
    const button = document.querySelector("button");
    expect(button?.textContent).toBe("新建 Agent");
    expect(button?.title).toBe("创建 Agent");
    expect(button?.getAttribute("aria-label")).toBe("创建 Agent");
    expect(document.querySelector("input")?.placeholder).toBe("这个 Agent 将长期负责的领域");
    expect(document.querySelector("[data-i18n-preserve]")?.textContent).toBe("Overview");
    expect(document.querySelector("pre")?.textContent).toBe("Result");
  });
});

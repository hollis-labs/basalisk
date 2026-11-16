import { createHub } from "@hollis-labs/ade-core"
import { type Envelope, parseCall } from "@hollis-labs/genui"
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { StrictMode } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { genuiTransport, loadGenui } from "../../../api/genui"
import { DeskContext } from "../../../desktop/context"
import type { DeskAPI } from "../../../desktop/registry"
import { admitGenui } from "../binding"
import { genuiAdeSurface } from "../surface"
import { GenuiWindow } from "../window"

vi.mock("../../../ade", () => ({ hub: createHub() }))
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function envelope(id: string, pattern: "form" | "approval" | "list_detail"): Envelope {
  const choice = {
    label: "Review changes",
    callFunction: {
      "@call": "genui_action" as const,
      args: {
        genui_action: {
          id: `action_${id}`,
          subject: { kind: "preferences", id: "account" },
          basis: { revision: "1" },
        },
        input: {},
      },
    },
  }
  return {
    v: 1,
    id,
    type: "genui.declarative-surface",
    typeVersion: "1.0.0",
    presentation: "inline",
    data: {
      version: "1.0",
      createSurface: {
        surfaceId: id,
        catalogId: "urn:hollis-labs:genui:catalog:1",
        components: [
          pattern === "form"
            ? {
                id: "root",
                component: "GenuiForm",
                props: {
                  title: "Edit preferences",
                  state: "ready",
                  fields: [
                    { key: "theme", label: "Theme", type: "text", required: true },
                    { key: "timezone", label: "Timezone", type: "text", required: true },
                  ],
                  submit: choice,
                },
              }
            : pattern === "approval"
              ? {
                  id: "root",
                  component: "GenuiApproval",
                  props: {
                    title: "Review preferences",
                    state: "ready",
                    changes: [{ label: "Theme", before: "dark", after: "light" }],
                    choices: [{ ...choice, label: "Apply reviewed change" }],
                  },
                }
              : {
                  id: "root",
                  component: "GenuiListDetail",
                  props: {
                    title: "Stored preferences",
                    state: "ready",
                    items: [{ id: "revision-2", title: "Revision 2" }],
                    selectedId: "revision-2",
                    detail: { title: "Revision 2", body: "Theme light; timezone UTC." },
                  },
                },
        ],
        dataModel: pattern === "form" ? { draft: { theme: "dark", timezone: "UTC" } } : {},
        metadata: {
          extensions: {
            genui_surface: { contract_version: "1" },
            genui_pattern: { id: pattern, version: "1.0.0" },
          },
        },
      },
    },
  }
}

const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } })
const native = (call: string, value: unknown) =>
  json({
    version: "1.0",
    agentFunctionResponse: {
      functionCallId: parseCall(call).callAgentFunction.functionCallId,
      value,
    },
  })

function mount(id = "form") {
  const setArgs = vi.fn()
  const desk = { setArgs } as unknown as DeskAPI
  const view = render(
    <StrictMode>
      <DeskContext.Provider value={desk}>
        <GenuiWindow winId="workspace" args={{ surfaceId: id }} />
      </DeskContext.Provider>
    </StrictMode>,
  )
  return { ...view, setArgs }
}

describe("Desktop GenUI adoption with synthetic HTTP", () => {
  it("uses raw native messages through form, immutable review, acknowledgment and stored result", async () => {
    const surfaces = new Map([["form", envelope("form", "form")]])
    const submissions: ReturnType<typeof parseCall>[] = []
    const confirmations: string[] = []
    const calls: string[] = []
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string, init?: RequestInit) => {
        calls.push(`${init?.method} ${input}`)
        expect(init?.credentials).toBe("same-origin")
        expect(new Headers(init?.headers).get("X-Requested-With")).toBe("XMLHttpRequest")
        if (input === "/api/genui/surfaces")
          return json({
            surfaces: [...surfaces].map(([id, item]) => ({
              id,
              title: item.data.createSurface.components[0].props.title,
            })),
          })
        if (input.startsWith("/api/genui/surfaces/"))
          return json(surfaces.get(input.split("/").at(-1) ?? ""))
        if (input === "/api/genui/confirm") {
          confirmations.push(String(init?.body))
          return json({ confirmation: "opaque-server-proof" })
        }
        if (input === "/api/genui/actions") {
          const raw = String(init?.body),
            call = parseCall(raw)
          submissions.push(call)
          if (call.callAgentFunction.surfaceId === "form") {
            surfaces.set("approval", envelope("approval", "approval"))
            return native(raw, { status: "ok", result: { surfaceId: "approval" } })
          }
          if (!call.callAgentFunction.callFunction.args.genui_action.confirmation)
            return native(raw, {
              status: "needs_confirmation",
              confirm: {
                what: "Apply reviewed preferences",
                scope: ["preferences:account"],
                ttlMs: 60000,
              },
            })
          surfaces.set("result", envelope("result", "list_detail"))
          return native(raw, { status: "ok", result: { surfaceId: "result" } })
        }
        throw new Error(`Unexpected test route ${input}`)
      }),
    )
    const { setArgs } = mount()
    const theme = await screen.findByRole("textbox", { name: "Theme (required)" })
    fireEvent.change(theme, { target: { value: "light" } })
    fireEvent.click(screen.getByRole("button", { name: "Review changes" }))
    await screen.findByRole("button", { name: "Apply reviewed change" })
    expect(submissions[0].callAgentFunction.callFunction.args.input).toEqual({
      draft: { theme: "light", timezone: "UTC" },
    })
    expect(confirmations).toEqual([])
    fireEvent.click(screen.getByRole("button", { name: "Apply reviewed change" }))
    await screen.findByRole("region", { name: "Confirmation required" })
    expect(submissions).toHaveLength(2)
    fireEvent.click(screen.getByRole("button", { name: "Continue to host confirmation" }))
    await screen.findByRole("heading", { name: "Stored preferences" })
    expect(confirmations).toHaveLength(1)
    const before = submissions[1].callAgentFunction.callFunction,
      after = submissions[2].callAgentFunction.callFunction
    expect(after).toEqual({
      ...before,
      args: {
        ...before.args,
        genui_action: { ...before.args.genui_action, confirmation: "opaque-server-proof" },
      },
    })
    expect(submissions[1].callAgentFunction.functionCallId).not.toBe(
      submissions[2].callAgentFunction.functionCallId,
    )
    expect(screen.getByText("Theme light; timezone UTC.")).toBeDefined()
    expect(setArgs.mock.calls.every(([, value]) => Object.keys(value).join() === "surfaceId")).toBe(
      true,
    )
    expect(JSON.stringify(setArgs.mock.calls)).not.toContain("opaque-server-proof")
    expect(calls.filter((call) => call.startsWith("POST /api/genui/actions"))).toHaveLength(3)
  })

  it("recovers before ready controls, keeps unknown nonterminal, and never resubmits on reconnect", async () => {
    let finish!: (value: Response) => void
    const posts = vi.fn()
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string, init?: RequestInit) => {
        if (init?.method === "POST") {
          posts()
          throw new Error("No mutations expected")
        }
        if (input === "/api/genui/surfaces")
          return json({
            surfaces: [{ id: "approval", title: "Review", actionId: "action_approval" }],
          })
        if (input === "/api/genui/surfaces/approval") return json(envelope("approval", "approval"))
        if (input.startsWith("/api/genui/actions/")) {
          const id = new URL(input, "https://example.test").searchParams.get("functionCallId")
          return new Promise<Response>((resolve) => {
            finish = () =>
              resolve(
                json({
                  response: {
                    version: "1.0",
                    agentFunctionResponse: {
                      functionCallId: id,
                      value: { status: "future-pending" },
                    },
                  },
                }),
              )
          })
        }
        throw new Error("Unexpected test route")
      }),
    )
    const view = mount("approval")
    await waitFor(() => expect(finish).toBeTypeOf("function"))
    expect(screen.queryByRole("button", { name: "Apply reviewed change" })).toBeNull()
    finish(json({}))
    await screen.findByText(
      "The result is not yet understood. Check the host status before taking further action.",
    )
    expect(
      (screen.getByRole("button", { name: "Apply reviewed change" }) as HTMLButtonElement).disabled,
    ).toBe(true)
    expect(posts).not.toHaveBeenCalled()
    view.unmount()
    expect(posts).not.toHaveBeenCalled()
  })

  it("rejects altered raw surface JSON before lossy parsing and does not convert HTTP503 into success", async () => {
    const surface = JSON.stringify(envelope("approval", "approval"))
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(surface.replace('"v":1,', '"v":1,"v":1,'))),
    )
    await expect(loadGenui("approval", new AbortController().signal)).rejects.toThrow()
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        json(
          {
            version: "1.0",
            agentFunctionResponse: {
              functionCallId: "rpc",
              error: { code: "unavailable", message: "Uncertain outcome" },
            },
          },
          503,
        ),
      ),
    )
    const raw = await genuiTransport.submit("{}", new AbortController().signal)
    expect(JSON.parse(raw).agentFunctionResponse.error.code).toBe("unavailable")
    expect(() => admitGenui(surface.replace('"v":1,', '"v":1e0,'))).toThrow()
  })

  it("refuses a discovery action that is not offered instead of rendering ready controls", async () => {
    const mutations = vi.fn()
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string, init?: RequestInit) => {
        if (init?.method === "POST") mutations()
        if (input === "/api/genui/surfaces")
          return json({
            surfaces: [{ id: "approval", title: "Review", actionId: "different-action" }],
          })
        if (input === "/api/genui/surfaces/approval") return json(envelope("approval", "approval"))
        throw new Error("Unexpected test route")
      }),
    )
    mount("approval")
    await screen.findByText(
      "This review could not be loaded or verified. Refresh the host state before acting.",
    )
    expect(screen.queryByRole("button", { name: "Apply reviewed change" })).toBeNull()
    expect(mutations).not.toHaveBeenCalled()
  })

  it("notifies session loss and never treats missing confirmation as an execution proof", async () => {
    const unauthorized = vi.fn()
    window.addEventListener("basalisk:unauthorized", unauthorized)
    try {
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => json({ code: "unauthorized" }, 401)),
      )
      await expect(loadGenui("approval", new AbortController().signal)).rejects.toThrow()
      expect(unauthorized).toHaveBeenCalledTimes(1)
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => json({})),
      )
      await expect(
        genuiTransport.confirm?.(
          {
            surfaceId: "approval",
            actionId: "action_approval",
            functionCallId: "rpc",
            call: "{}",
            challenge: { what: "Review", scope: ["preferences"], ttlMs: 1000 },
          },
          new AbortController().signal,
        ),
      ).rejects.toThrow()
    } finally {
      window.removeEventListener("basalisk:unauthorized", unauthorized)
    }
  })

  it("ADE exposes only retained-view navigation and focus; no approval path", async () => {
    const select = vi.fn(),
      root = document.createElement("section")
    root.tabIndex = -1
    document.body.append(root)
    const surface = genuiAdeSurface({
      winId: "workspace",
      entries: [{ id: "approval", title: "Review" }],
      selectedId: "",
      phase: "confirmation",
      root,
      select,
    })
    const ctx = { actor: "agent" as const, signal: new AbortController().signal }
    expect((await surface.act?.("approve", {}, ctx))?.outcome).toBe("rejected")
    expect((await surface.act?.("confirm", {}, ctx))?.outcome).toBe("rejected")
    expect((await surface.act?.("select_surface", { id: "forged" }, ctx))?.outcome).toBe("rejected")
    expect(select).not.toHaveBeenCalled()
    const cancelled = new AbortController()
    cancelled.abort()
    expect(
      (
        await surface.act?.(
          "select_surface",
          { id: "approval" },
          { actor: "agent", signal: cancelled.signal },
        )
      )?.outcome,
    ).toBe("rejected")
    expect(select).not.toHaveBeenCalled()
    expect((await surface.act?.("select_surface", { id: "approval" }, ctx))?.outcome).toBe(
      "applied",
    )
    expect(select).toHaveBeenCalledWith("approval")
    await surface.act?.("focus", {}, ctx)
    expect(document.activeElement).toBe(root)
    root.remove()
  })
})

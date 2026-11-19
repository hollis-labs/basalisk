import type { SurfaceAdapter } from "@hollis-labs/ade-core"
import type { GenuiEntry } from "../../api/genui"

export interface GenuiAdeState {
  winId: string
  entries: GenuiEntry[]
  selectedId: string
  phase: string
  root: HTMLElement | null
  select(id: string): void
}

/** ADE can inspect and navigate this workspace. It cannot submit or acknowledge actions. */
export function genuiAdeSurface(state: GenuiAdeState): SurfaceAdapter {
  const find = (id: string) =>
    Array.from(state.root?.querySelectorAll<HTMLElement>("[data-genui-id]") ?? []).find(
      (element) => element.dataset.genuiId === id,
    )
  return {
    info: { id: `genui:${state.winId}`, app: "genui", title: "GenUI workspace" },
    observe: () => ({
      summary: `Reviewed preference surfaces. Current view: ${state.phase}. Selection and focus are presentation only; actions require the owner's authenticated review.`,
      targets: state.entries.map((entry) => ({
        kind: "surface",
        id: entry.id,
        label: entry.title,
        state: { selected: entry.id === state.selectedId },
      })),
      actions: [
        {
          name: "select_surface",
          level: "act",
          description: "Open an existing reviewed surface. Does not submit or confirm any action.",
          input_schema: {
            type: "object",
            properties: { id: { type: "string" } },
            required: ["id"],
            additionalProperties: false,
          },
        },
        {
          name: "focus",
          level: "act",
          description: "Focus the workspace for the owner without activating a control.",
          input_schema: { type: "object", properties: {}, additionalProperties: false },
        },
      ],
      checks: [
        {
          name: "selected",
          description: "This surface is selected in the workspace.",
          args_schema: {
            type: "object",
            properties: { id: { type: "string" } },
            required: ["id"],
            additionalProperties: false,
          },
        },
      ],
    }),
    act: (action, input, context) => {
      if (context.signal.aborted)
        return {
          outcome: "rejected",
          code: "unavailable",
          reason: "This presentation request was cancelled.",
        }
      if (action === "focus" && Object.keys(input).length === 0) {
        state.root?.focus()
        return { outcome: "applied" }
      }
      if (
        action === "select_surface" &&
        Object.keys(input).length === 1 &&
        typeof input.id === "string" &&
        state.entries.some((entry) => entry.id === input.id)
      ) {
        state.select(input.id)
        return { outcome: "applied" }
      }
      return {
        outcome: "rejected",
        code: "invalid",
        reason:
          "ADE only selects retained surfaces or focuses this workspace. It cannot approve, confirm, or edit preferences.",
      }
    },
    check: (name, args) => name === "selected" && args.id === state.selectedId,
    resolve: (anchor) => {
      const box =
        anchor.target?.kind === "surface"
          ? find(anchor.target.id)?.getBoundingClientRect()
          : undefined
      return box && box.width > 0 ? { x: box.left, y: box.top, w: box.width, h: box.height } : null
    },
    bounds: () => {
      const box = state.root?.getBoundingClientRect()
      return box ? { x: box.left, y: box.top, w: box.width, h: box.height } : null
    },
    describe: (anchor) =>
      state.entries.find((entry) => entry.id === anchor.target?.id)?.title ?? "GenUI workspace",
  }
}

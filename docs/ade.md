# ADE on the desktop

Basalisk uses the shared ADE packages (`@hollis-labs/ade-core`, `@hollis-labs/ade-react`):
the same ones the chess tutor example and Folio's ADE preset use. They give the owner and
the agent one way to point at the same thing, and a guide that follows what really happened.

- `frontend/src/ade/index.tsx` holds one hub for the whole window, mounts what the agent
  points at (rings, spotlight, arrows) and the guide bar over the desktop, and offers the
  hub's tools to the agent through the desktop's own tool list (`registerAgentToolSource`
  in `agent/tools.ts`). The tools are `ade_look`, `ade_point`, `ade_clear`, `ade_mark`,
  `ade_guide_start`, `ade_guide_status`, `ade_guide_stop`, and one per action of each open
  surface (`diagram_select`). They exist only while a surface is open.
- A window becomes a surface with `useSurface(() => adapter, hub)`. The **Diagram** window is
  the first (`apps/diagram/surface.ts`): its nodes and edges are parts; the owner's
  selection is what "this" means to the agent; the agent can highlight a node, draw an arrow
  between two, or walk through the diagram with a guide that waits for the owner to select
  a node. Editing a diagram stays with `collab_write`.
- The agent's conversation is still Basalisk's own (`agent/session.ts`). The shared
  `@hollis-labs/ade-agent` client is that code taken out of the desktop; moving onto it is a
  later step.

Until the packages are published they are installed from `frontend/vendor/*.tgz`, built
from the ADE repository with `npm pack`.

Checked in a browser against a stand-in Caster: the tools are offered; the agent sees the
diagram's parts and the owner's selection; its highlight sits on the node it names; the
guide bar shows; a guide step that waits for a selection moves on when the owner makes it;
an action on a part that is not there is refused.

Run once with the model, in a throwaway instance: with a node selected and "what does this
connect to, point at them, then walk me through the diagram", the agent looked, named the
selected node and its three neighbours, outlined them on the canvas and started a six-step
guide. Known gap: the diagram's own inspector opens over the right of the canvas when a node
is selected, and can cover a node the agent is pointing at.

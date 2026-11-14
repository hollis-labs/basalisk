import { request } from "./account"

/** The Work engine (internal/work, docs/work.md): workspaces of typed items in Caster. */

export interface WorkField {
  key: string
  label?: string
  type: "text" | "number" | "date" | "enum" | "ref" | "refs" | "url" | "bool"
  options?: string[]
}

export interface WorkKind {
  kind: string
  label: string
  plural?: string
  icon?: string
  stages: string[]
  fields: WorkField[]
  sections: string[]
  children: string[]
  color?: string
  rev: number
  problem?: string
}

export interface WorkView {
  id: string
  view: "board" | "calendar" | "outline" | "canvas" | "flow"
  title: string
  kinds: string[]
  filter?: Record<string, unknown>
  group_by?: string
  date_field?: string
  root?: string
  rel?: string[]
  layout?: Record<string, unknown>
  rev: number
  problem?: string
}

export interface WorkLink {
  rel: string
  to: string
}

export interface WorkMaterial {
  ref: string
  caption?: string
}

export interface WorkPublished {
  at: string
  where: string
  url?: string
  note?: string
}

export interface WorkCard {
  id: string
  kind: string
  title: string
  stage: string
  fields: Record<string, unknown>
  parent?: string
  children: string[]
  links: WorkLink[]
  material: WorkMaterial[]
  published: WorkPublished[]
  task?: string
  tags: string[]
  cut?: boolean
  excerpt?: string
  words: number
  rev: number
  updated: string
  problem?: string
}

export interface WorkNote {
  id: string
  item: string
  item_rev?: number
  kind: string
  section?: string
  quote?: string
  replace?: string
  text?: string
  created_at: string
}

export interface WorkDetail extends WorkCard {
  ref: string
  sections: Record<string, string>
  order: string[]
  lead?: string
  notes: WorkNote[]
}

export interface Workspace {
  slug: string
  title: string
  app: string
  preset: string
  ns: string
  owners: string[]
  created?: string
  body: string
  items: number
  rev: number
  updated: string
  problem?: string
}

export interface WorkPreset {
  id: string
  app: string
  label: string
  kinds: WorkKind[]
  views: WorkView[]
}

export interface WorkSpace {
  workspace: Workspace
  kinds: WorkKind[]
  items: WorkCard[]
  views: WorkView[]
}

export interface WorkRevision {
  rev: number
  at: string
  title: string
}

export interface WorkEdit {
  base_rev: number
  title?: string
  fields?: Record<string, unknown>
  sections?: Record<string, string>
  tags?: string[]
}

const enc = encodeURIComponent

export const work = {
  list: (app: string, refresh = false) =>
    request<{ workspaces: Workspace[]; presets: WorkPreset[]; problem?: string }>(
      `work?app=${enc(app)}${refresh ? "&refresh=1" : ""}`,
    ),
  create: (workspace: string, title: string, preset: string, about: string) =>
    request<{ workspace: string }>("work", "POST", { workspace, title, preset, about }),
  open: (ws: string, refresh = false) =>
    request<{ space: WorkSpace; relations: string[]; problem?: string }>(
      `work/${enc(ws)}${refresh ? "?refresh=1" : ""}`,
    ),
  item: (ws: string, id: string) => request<WorkDetail>(`work/${enc(ws)}/items/${enc(id)}`),
  make: (
    ws: string,
    item: {
      kind: string
      title?: string
      stage?: string
      parent?: string
      fields?: Record<string, unknown>
    },
  ) => request<{ id: string }>(`work/${enc(ws)}/items`, "POST", item),
  edit: (ws: string, id: string, edit: WorkEdit) =>
    request<{ rev: number }>(`work/${enc(ws)}/items/${enc(id)}`, "PUT", edit),
  move: (ws: string, id: string, key: string, value: string) =>
    request<{ rev: number }>(`work/${enc(ws)}/items/${enc(id)}/move`, "PUT", { key, value }),
  cut: (ws: string, id: string) => request(`work/${enc(ws)}/items/${enc(id)}/cut`, "POST"),
  restore: (ws: string, id: string) => request(`work/${enc(ws)}/items/${enc(id)}/restore`, "POST"),
  dropNote: (ws: string, id: string) => request(`work/${enc(ws)}/notes/${enc(id)}`, "DELETE"),
  saveView: (ws: string, id: string, set: Record<string, unknown>) =>
    request<{ id: string; rev: number }>(`work/${enc(ws)}/views/${enc(id)}`, "PUT", set),
  newView: (ws: string, set: Record<string, unknown>) =>
    request<{ id: string; rev: number }>(`work/${enc(ws)}/views`, "POST", set),
  log: (ws: string, name: string) =>
    request<{ revisions: WorkRevision[] }>(`work/${enc(ws)}/log?name=${enc(name)}`),
  at: (ws: string, name: string, rev: number) =>
    request<{
      name: string
      rev: number
      content: string
      title?: string
      stage?: string
      sections?: Record<string, string>
    }>(`work/${enc(ws)}/at?name=${enc(name)}&rev=${rev}`),
}

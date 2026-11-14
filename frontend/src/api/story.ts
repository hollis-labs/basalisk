import { request } from "./account"

/** A script on the board (internal/story Card). */
export interface StoryCard {
  key: string
  project: string
  script: string
  title: string
  stage: string
  round: number
  length_s?: number
  platform?: string
  owners?: string[]
  task?: string
  scenes: number
  duration_s: number
  draft_notes: number
  next?: string
  updated: string
  rev: number
  problem?: string
}

export interface StoryFrame {
  ref: string
  caption?: string
}

export interface StoryScene {
  id: string
  name: string
  rev: number
  title: string
  status: string
  duration_s: number
  sections: Record<string, string>
  frames: StoryFrame[]
  words: number
  notes: number
  missing?: boolean
  problem?: string
  updated?: string
}

export interface StoryScript extends StoryCard {
  body: string
  scene_list: StoryScene[]
  unused: StoryScene[]
}

export interface StoryNote {
  id: string
  key: string
  scene?: string
  scene_rev?: number
  kind: string
  section?: string
  quote?: string
  replace?: string
  text?: string
  created_at: string
}

export interface StoryRevision {
  rev: number
  at: string
  title: string
}

export interface SceneEdit {
  base_rev: number
  title?: string
  duration_s?: number
  sections?: Record<string, string>
}

const enc = encodeURIComponent

export const story = {
  board: (refresh = false) =>
    request<{ scripts: StoryCard[]; stages: string[]; problem?: string }>(
      `story${refresh ? "?refresh=1" : ""}`,
    ),
  script: (key: string, refresh = false) =>
    request<{ script: StoryScript; drafts: StoryNote[]; sections: string[]; problem?: string }>(
      `story/scripts/${enc(key)}${refresh ? "?refresh=1" : ""}`,
    ),
  create: (project: string, script: string, title: string) =>
    request<{ key: string }>("story/scripts", "POST", { project, script, title }),
  stage: (key: string, stage: string) =>
    request(`story/scripts/${enc(key)}/stage`, "PUT", { stage }),
  order: (key: string, scenes: string[]) =>
    request(`story/scripts/${enc(key)}/order`, "PUT", { scenes }),
  addScene: (key: string, opts: { after?: string; from?: string; title?: string }) =>
    request<{ id: string }>(`story/scripts/${enc(key)}/scenes`, "POST", opts),
  editScene: (key: string, id: string, edit: SceneEdit) =>
    request<{ rev: number }>(`story/scripts/${enc(key)}/scenes/${enc(id)}`, "PUT", edit),
  cut: (key: string, id: string) =>
    request(`story/scripts/${enc(key)}/scenes/${enc(id)}/cut`, "POST"),
  restore: (key: string, id: string) =>
    request(`story/scripts/${enc(key)}/scenes/${enc(id)}/restore`, "POST"),
  log: (name: string) => request<{ revisions: StoryRevision[] }>(`story/log?name=${enc(name)}`),
  item: (name: string, rev: number) =>
    request<{ name: string; rev: number; content: string }>(
      `story/item?name=${enc(name)}&rev=${rev}`,
    ),
}

/** Seconds as m:ss. */
export function clock(s: number): string {
  const t = Math.max(0, Math.round(s))
  return `${Math.floor(t / 60)}:${String(t % 60).padStart(2, "0")}`
}

/** Spoken length of narration at 150 words a minute, in seconds. */
export const spoken = (words: number) => (words * 60) / 150

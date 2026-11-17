/**
 * The questions the agent has asked the owner (internal/agent/questions.go), as the page
 * knows them: the sets that wait, read from the server, with each answer held on the server
 * as it is given so the cards are the same after the panel is closed or the page reloads.
 * Nothing reaches the agent until a set is sent.
 */
import { useSyncExternalStore } from "react"
import { ApiError } from "../api/account"
import type { DeskAPI } from "../desktop/registry"
import { agentApi, type Question, type QuestionSet } from "./api"
import { type AgentSession, CLIENT, setOnQuestions } from "./session"
import { snapshot, toolSpecs } from "./tools"

let sets: QuestionSet[] = []
const listeners = new Set<() => void>()
const tell = () => {
  for (const fn of listeners) fn()
}
let timer = 0
let reading = false
let again = false

/** Read the open sets now. A reading asked for while one is under way follows it. */
export async function refreshQuestions() {
  if (reading) {
    again = true
    return
  }
  reading = true
  try {
    const next = await agentApi.questions()
    if (JSON.stringify(next) !== JSON.stringify(sets)) {
      sets = next
      tell()
    }
  } catch {
    // No agent, or not signed in: the next reading will do.
  } finally {
    reading = false
    window.clearTimeout(timer)
    if (listeners.size) timer = window.setTimeout(() => void refreshQuestions(), 20_000)
    if (again) {
      again = false
      void refreshQuestions()
    }
  }
}

setOnQuestions(() => void refreshQuestions())

const subscribe = (fn: () => void) => {
  if (!listeners.size) void refreshQuestions()
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
    if (!listeners.size) window.clearTimeout(timer)
  }
}

/** Every set that waits on the owner. */
export const useQuestions = () => useSyncExternalStore(subscribe, () => sets)

/** The sets one thread asked: the running thread's (task null) or a task's. */
export const setsOf = (all: QuestionSet[], task: string | null) =>
  all.filter((s) => (s.task ?? "") === (task ?? ""))

const put = (next: QuestionSet) => {
  sets = sets.map((s) => (s.id === next.id ? next : s))
  tell()
}

/** Answers on their way to the server, so a set is not sent ahead of its last answer. */
let holding: Promise<unknown> = Promise.resolve()

/** Hold an answer on a card. The card shows it at once; the server's word replaces it. */
export function holdAnswer(
  set: string,
  q: Question,
  answer: { options: string[]; text: string },
  skip = false,
): Promise<string> {
  const done = holding.then(() => hold(set, q, answer, skip))
  holding = done.catch(() => undefined)
  return done
}

async function hold(
  set: string,
  q: Question,
  answer: { options: string[]; text: string },
  skip = false,
): Promise<string> {
  const now = sets.find((s) => s.id === set)
  if (!now) return ""
  const empty = !answer.options.length && !answer.text.trim()
  put({
    ...now,
    questions: now.questions.map((it) =>
      it.id === q.id
        ? {
            ...it,
            answer: skip ? { options: [], text: "" } : answer,
            state: skip ? "skipped" : empty ? "open" : "answered",
          }
        : it,
    ),
  })
  try {
    put(await agentApi.answerQuestion(set, q.id, { ...answer, skip }))
    return ""
  } catch (err) {
    void refreshQuestions()
    return err instanceof ApiError ? err.message : "The answer could not be kept."
  }
}

/** Send a set's answers together. The thread that asked starts on them. */
export async function submitSet(
  desk: DeskAPI,
  session: AgentSession,
  set: string,
  note: string,
): Promise<string> {
  try {
    await holding
    await agentApi.submitQuestions(set, {
      note,
      client_id: CLIENT,
      context: await snapshot(desk),
      tools: toolSpecs(),
    })
    sets = sets.filter((s) => s.id !== set)
    tell()
    // The thread has a new message and a run under way: read it and follow.
    await session.reload()
    return ""
  } catch (err) {
    void refreshQuestions()
    return err instanceof ApiError ? err.message : "The answers could not be sent."
  }
}

/** Put a set away unanswered. */
export async function dismissSet(set: string) {
  await agentApi.dismissQuestions(set).catch(() => undefined)
  sets = sets.filter((s) => s.id !== set)
  tell()
}

/** How many cards in these sets have no answer and were not skipped. */
export const waiting = (list: QuestionSet[]) =>
  list.reduce((n, s) => n + s.questions.filter((q) => q.state === "open").length, 0)

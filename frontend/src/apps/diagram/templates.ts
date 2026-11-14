/**
 * Starting points for a new diagram. Written without places (the layout does it when the
 * diagram opens), each shows the conventions for its kind of picture: lanes for stages or
 * places, steps along the main path, edge styles the legend explains.
 */
import type { Diagram } from "./model"

export interface Template {
  id: string
  label: string
  about: string
  diagram: Omit<Diagram, "diagram">
}

export const TEMPLATES: Template[] = [
  {
    id: "blank",
    label: "Blank",
    about: "An empty canvas.",
    diagram: { nodes: [], edges: [], groups: [] },
  },
  {
    id: "architecture",
    label: "Architecture",
    about: "Where things run and how they talk: clients, services, data, outside.",
    diagram: {
      legend: { flow: "request", data: "reads and writes", async: "events" },
      groups: [
        { id: "clients", label: "Clients", subtitle: "What people use" },
        { id: "services", label: "Services", subtitle: "What does the work", tone: "blue" },
        { id: "data", label: "Data", subtitle: "Where it is kept" },
      ],
      nodes: [
        {
          id: "web",
          label: "Web app",
          subtitle: "browser",
          kind: "client",
          group: "clients",
          x: 0,
          y: 0,
        },
        {
          id: "mobile",
          label: "Mobile app",
          subtitle: "iOS · Android",
          kind: "client",
          group: "clients",
          x: 0,
          y: 0,
        },
        {
          id: "api",
          label: "API",
          subtitle: "HTTP · auth",
          kind: "service",
          group: "services",
          highlight: true,
          tone: "blue",
          x: 0,
          y: 0,
        },
        {
          id: "worker",
          label: "Worker",
          subtitle: "background jobs",
          kind: "service",
          group: "services",
          x: 0,
          y: 0,
        },
        {
          id: "queue",
          label: "Queue",
          subtitle: "jobs",
          kind: "queue",
          group: "services",
          x: 0,
          y: 0,
        },
        {
          id: "db",
          label: "Database",
          subtitle: "Postgres",
          kind: "store",
          group: "data",
          x: 0,
          y: 0,
        },
        {
          id: "files",
          label: "Object store",
          subtitle: "uploads",
          kind: "store",
          group: "data",
          x: 0,
          y: 0,
        },
        { id: "mail", label: "Email provider", kind: "external", x: 0, y: 0 },
      ],
      edges: [
        { id: "web-api", from: "web", to: "api", label: "https" },
        { id: "mobile-api", from: "mobile", to: "api", label: "https" },
        { id: "api-db", from: "api", to: "db", style: "data" },
        { id: "api-queue", from: "api", to: "queue", label: "enqueue", style: "async" },
        { id: "queue-worker", from: "queue", to: "worker", style: "async" },
        { id: "worker-db", from: "worker", to: "db", style: "data" },
        { id: "worker-files", from: "worker", to: "files", style: "data" },
        { id: "worker-mail", from: "worker", to: "mail", label: "send" },
      ],
    },
  },
  {
    id: "pipeline",
    label: "Pipeline",
    about: "Numbered stages with a human gate and loops back: delivery, review, release.",
    diagram: {
      legend: {
        flow: "automated",
        human: "human decision",
        repair: "repair loop",
        feedback: "feedback",
      },
      groups: [
        { id: "intake", label: "Intake", subtitle: "What starts it" },
        { id: "work", label: "Work", subtitle: "What is done, and redone", tone: "blue" },
        { id: "release", label: "Release", subtitle: "Checks, approval and shipping" },
      ],
      nodes: [
        {
          id: "trigger",
          label: "Trigger",
          subtitle: "schedule or event",
          kind: "start",
          group: "intake",
          x: 0,
          y: 0,
        },
        {
          id: "plan",
          label: "Plan",
          subtitle: "pick the work",
          step: 1,
          tone: "blue",
          group: "work",
          x: 0,
          y: 0,
        },
        {
          id: "do",
          label: "Do the work",
          step: 2,
          tone: "blue",
          items: ["Batch 1", "Batch 2"],
          group: "work",
          x: 0,
          y: 0,
        },
        {
          id: "check",
          label: "Checks",
          subtitle: "tests + lint",
          step: 3,
          group: "release",
          x: 0,
          y: 0,
        },
        {
          id: "review",
          label: "Review",
          subtitle: "a person approves",
          step: 4,
          tone: "amber",
          highlight: true,
          group: "release",
          x: 0,
          y: 0,
        },
        { id: "ship", label: "Ship", step: 5, group: "release", x: 0, y: 0 },
        {
          id: "fix",
          label: "Fix",
          subtitle: "what failed",
          tone: "red",
          tag: "retry",
          group: "work",
          x: 0,
          y: 0,
        },
      ],
      edges: [
        { id: "t-p", from: "trigger", to: "plan" },
        { id: "p-d", from: "plan", to: "do" },
        { id: "d-c", from: "do", to: "check" },
        { id: "c-r", from: "check", to: "review" },
        { id: "r-s", from: "review", to: "ship", label: "approved", style: "human" },
        { id: "c-f", from: "check", to: "fix", label: "fails", style: "repair" },
        { id: "f-d", from: "fix", to: "do", style: "repair" },
        { id: "r-d", from: "review", to: "do", label: "changes", style: "human" },
        { id: "s-p", from: "ship", to: "plan", label: "report", style: "feedback" },
      ],
    },
  },
  {
    id: "decision",
    label: "Decision flow",
    about: "Questions and their answers: start, decisions, outcomes.",
    diagram: {
      groups: [],
      nodes: [
        { id: "start", label: "Request comes in", kind: "start", x: 0, y: 0 },
        { id: "known", label: "Known user?", kind: "decision", x: 0, y: 0 },
        { id: "signin", label: "Ask to sign in", kind: "step", x: 0, y: 0 },
        { id: "allowed", label: "Allowed?", kind: "decision", x: 0, y: 0 },
        { id: "serve", label: "Serve it", kind: "step", tone: "green", x: 0, y: 0 },
        { id: "deny", label: "Refuse, say why", kind: "step", tone: "red", x: 0, y: 0 },
        { id: "end", label: "Done", kind: "end", x: 0, y: 0 },
      ],
      edges: [
        { id: "s-k", from: "start", to: "known" },
        { id: "k-a", from: "known", to: "allowed", label: "yes" },
        { id: "k-s", from: "known", to: "signin", label: "no", style: "muted" },
        { id: "s-k2", from: "signin", to: "known", style: "feedback" },
        { id: "a-v", from: "allowed", to: "serve", label: "yes" },
        { id: "a-d", from: "allowed", to: "deny", label: "no", style: "repair" },
        { id: "v-e", from: "serve", to: "end" },
        { id: "d-e", from: "deny", to: "end" },
      ],
    },
  },
  {
    id: "dataflow",
    label: "Data flow",
    about: "Where data comes from, what happens to it, where it ends up.",
    diagram: {
      groups: [
        { id: "sources", label: "Sources", subtitle: "What produces data" },
        { id: "processing", label: "Processing", subtitle: "Clean, join, decide", tone: "violet" },
        { id: "outputs", label: "Outputs", subtitle: "Who uses it" },
      ],
      nodes: [
        {
          id: "events",
          label: "App events",
          icon: "zap",
          tone: "teal",
          group: "sources",
          x: 0,
          y: 0,
        },
        { id: "db", label: "Database", kind: "store", group: "sources", x: 0, y: 0 },
        { id: "api", label: "Partner API", kind: "external", group: "sources", x: 0, y: 0 },
        {
          id: "ingest",
          label: "Ingest",
          subtitle: "queue + schema",
          kind: "queue",
          group: "processing",
          x: 0,
          y: 0,
        },
        {
          id: "transform",
          label: "Transform",
          subtitle: "joins, lookups",
          icon: "layers",
          group: "processing",
          highlight: true,
          tone: "violet",
          x: 0,
          y: 0,
        },
        { id: "warehouse", label: "Warehouse", kind: "store", group: "outputs", x: 0, y: 0 },
        { id: "dash", label: "Dashboards", icon: "monitor", group: "outputs", x: 0, y: 0 },
      ],
      edges: [
        { id: "e-i", from: "events", to: "ingest", style: "data" },
        { id: "d-i", from: "db", to: "ingest", style: "data" },
        { id: "a-i", from: "api", to: "ingest", style: "async" },
        { id: "i-t", from: "ingest", to: "transform" },
        { id: "t-w", from: "transform", to: "warehouse", style: "data" },
        { id: "w-d", from: "warehouse", to: "dash" },
      ],
    },
  },
]

/** A template's JSON as a new diagram's body: no places, so it is laid out when it opens. */
export function templateBody(t: Template): string {
  const strip = <T extends { x?: number; y?: number }>(n: T) => {
    const { x: _x, y: _y, ...rest } = n
    return rest
  }
  const d = { diagram: "v1", ...t.diagram, nodes: t.diagram.nodes.map(strip) }
  return `${JSON.stringify(d, null, 1)}\n`
}

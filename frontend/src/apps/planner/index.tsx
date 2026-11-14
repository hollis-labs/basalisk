/**
 * Planner: the strategy level above Torque: goals, plans in phases, milestones, features,
 * ideas, explorations, tasks, decisions and risks (docs/work.md). The Work engine's window
 * with the Planner preset; a task can be sent to Torque.
 */
import { Target } from "lucide-react"
import { registerApp } from "../../desktop/registry"
import { WorkWindow } from "../../work/window"

registerApp({
  id: "planner",
  title: "Planner",
  icon: Target,
  menu: true,
  size: [1180, 720],
  bare: true,
  render: () => <WorkWindow app="planner" appTitle="Planner" />,
})

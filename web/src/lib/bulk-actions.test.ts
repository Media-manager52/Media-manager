import { describe, expect, it } from "vitest"

import type { APIWorkflow, APIWorkflowAction } from "@/api/client"
import { bulkChoices, runBulk } from "@/lib/bulk-actions"

const action = (name: string, extra: Partial<APIWorkflowAction> = {}): APIWorkflowAction =>
  ({ action: name, operation_id: name, method: "POST", path: `/api/v1/workflows/x/${name}`, effect: "provider", available: true, ...extra }) as APIWorkflowAction
const workflow = (id: string, actions: APIWorkflowAction[]): APIWorkflow => ({ id, title: `Titel ${id}`, available_actions: actions }) as APIWorkflow

describe("which actions may run for many cases at once", () => {
  it("offers an action only when at least two cases share it", () => {
    const choices = bulkChoices([workflow("a", [action("open_next_round")]), workflow("b", [action("open_next_round"), action("search")]), workflow("c", [])])
    expect(choices.map((choice) => [choice.action, choice.steps.map((step) => step.workflow.id)])).toEqual([["open_next_round", ["a", "b"]]])
  })

  it("never offers cancel, a destructive action, one that needs confirmation or one the server refuses", () => {
    const pair = (item: APIWorkflowAction) => [workflow("a", [item]), workflow("b", [item])]
    expect(bulkChoices(pair(action("cancel", { effect: "write" })))).toEqual([])
    expect(bulkChoices(pair(action("remove", { effect: "destructive" })))).toEqual([])
    expect(bulkChoices(pair(action("search", { confirmation: "Testfilmen" } as Partial<APIWorkflowAction>)))).toEqual([])
    expect(bulkChoices(pair(action("search", { available: false, reason: "busy" })))).toEqual([])
    expect(bulkChoices(pair(action("search", { effect: "read" })))).toEqual([])
    expect(bulkChoices(pair(action("reconcile_altmount", { effect: "write" })))).toHaveLength(1)
  })

  it("counts only the cases where the action is available", () => {
    const choices = bulkChoices([workflow("a", [action("search")]), workflow("b", [action("search", { available: false })]), workflow("c", [action("search")])])
    expect(choices[0].steps.map((step) => step.workflow.id)).toEqual(["a", "c"])
  })

  it("lists the choices in the usual order", () => {
    const both = [action("reconcile_altmount", { effect: "write" }), action("search")]
    expect(bulkChoices([workflow("a", both), workflow("b", both)]).map((choice) => choice.action)).toEqual(["search", "reconcile_altmount"])
  })
})

describe("running many cases", () => {
  it("runs one at a time, keeps going after a failure and reports both", async () => {
    const steps = ["a", "b", "c"].map((id) => ({ workflow: workflow(id, []), action: action("search") }))
    const order: string[] = []
    let active = 0
    const progress: number[] = []
    const result = await runBulk(steps, async (step) => {
      expect(active).toBe(0)
      active++
      await Promise.resolve()
      order.push(step.workflow.id)
      active--
      if (step.workflow.id === "b") throw new Error("nej")
    }, (done) => progress.push(done))
    expect(order).toEqual(["a", "b", "c"])
    expect(progress).toEqual([1, 2, 3])
    expect(result.succeeded.map((step) => step.workflow.id)).toEqual(["a", "c"])
    expect(result.failed.map((step) => [step.workflow.id, (step.error as Error).message])).toEqual([["b", "nej"]])
  })
})

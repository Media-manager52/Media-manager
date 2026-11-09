import type { APIWorkflow, APIWorkflowAction } from "@/api/client"

/** The order owner actions are offered in, most useful first. */
export const actionOrder = ["search", "open_next_round", "open_round_from_known", "discover", "reconcile_altmount", "reconfirm_plex", "retry_access_rejected_release", "cancel"]

/** One click handles at most this many cases, so indexer limits are not spent in one go. */
export const bulkLimit = 25

export type BulkStep = { workflow: APIWorkflow; action: APIWorkflowAction }
export type BulkChoice = { action: string; steps: BulkStep[] }

/**
 * An action may run for many cases at once only when the server offers it
 * now, it needs no typed confirmation and it does not end or delete anything.
 */
function bulkSafe(action: APIWorkflowAction): boolean {
  return action.available && !action.confirmation && action.action !== "cancel" && (action.effect === "write" || action.effect === "provider")
}

/** The actions at least two of the cases share, each with the cases that offer it. */
export function bulkChoices(workflows: APIWorkflow[]): BulkChoice[] {
  const byAction = new Map<string, BulkStep[]>()
  for (const workflow of workflows) {
    for (const action of workflow.available_actions ?? []) {
      if (!bulkSafe(action)) continue
      byAction.set(action.action, [...(byAction.get(action.action) ?? []), { workflow, action }])
    }
  }
  return [...byAction.entries()]
    .filter(([, steps]) => steps.length >= 2)
    .map(([action, steps]) => ({ action, steps }))
    .sort((a, b) => (actionOrder.indexOf(a.action) + 1 || 99) - (actionOrder.indexOf(b.action) + 1 || 99))
}

export type BulkResult = { succeeded: BulkStep[]; failed: Array<BulkStep & { error: unknown }> }

/**
 * Runs the steps one at a time and keeps going after a failure, so the result
 * says exactly which cases were started and which were not.
 */
export async function runBulk(steps: BulkStep[], run: (step: BulkStep) => Promise<unknown>, onProgress: (done: number) => void = () => {}): Promise<BulkResult> {
  const result: BulkResult = { succeeded: [], failed: [] }
  for (const [index, step] of steps.entries()) {
    try {
      await run(step)
      result.succeeded.push(step)
    } catch (error) {
      result.failed.push({ ...step, error })
    }
    onProgress(index + 1)
  }
  return result
}

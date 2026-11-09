import { useQueryClient } from "@tanstack/react-query"
import { useState } from "react"

import { runWorkflowAction, type APIWorkflow } from "@/api/client"
import { Button, Dialog } from "@/components/ui"
import { formatNumber } from "@/format"
import { actionLabel, errorMessage } from "@/i18n"
import { bulkChoices, bulkLimit, runBulk, type BulkChoice, type BulkResult } from "@/lib/bulk-actions"

/**
 * One button per action that several cases in a group share. It sends the same
 * request the single-case button sends, one case at a time, and then says how
 * many were started and which were not.
 */
export function BulkActions({ workflows }: { workflows: APIWorkflow[] }) {
  const [pending, setPending] = useState<BulkChoice | null>(null)
  const choices = bulkChoices(workflows)
  if (choices.length === 0) return null
  return (
    <>
      <div className="flex flex-wrap gap-2">
        {choices.map((choice) => (
          <Button key={choice.action} className="h-10 px-3 text-xs sm:h-8" title={actionLabel(choice.action).help} onClick={() => setPending(choice)}>
            {actionLabel(choice.action).label} for {choice.steps.length > bulkLimit ? `de første ${bulkLimit}` : `alle ${formatNumber(choice.steps.length)}`}
          </Button>
        ))}
      </div>
      {pending ? <BulkDialog choice={pending} onClose={() => setPending(null)} /> : null}
    </>
  )
}

function BulkDialog({ choice, onClose }: { choice: BulkChoice; onClose: () => void }) {
  const queryClient = useQueryClient()
  const steps = choice.steps.slice(0, bulkLimit)
  const { label, help } = actionLabel(choice.action)
  const [done, setDone] = useState(0)
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<BulkResult | null>(null)
  const start = async () => {
    setRunning(true)
    const outcome = await runBulk(steps, (step) => runWorkflowAction(step.action), setDone)
    setResult(outcome)
    setRunning(false)
    await queryClient.invalidateQueries()
  }
  return (
    <Dialog
      open
      onClose={() => { if (!running) onClose() }}
      title={`${label} for ${formatNumber(steps.length)} sager`}
      footer={result
        ? <Button variant="primary" onClick={onClose}>Luk</Button>
        : <><Button variant="ghost" disabled={running} onClick={onClose}>Fortryd</Button><Button variant="primary" busy={running} onClick={start}>{label} for {formatNumber(steps.length)}</Button></>}
    >
      {result ? (
        <div role="status">
          <p>{label}: sat i gang for {formatNumber(result.succeeded.length)} af {formatNumber(steps.length)} sager.</p>
          {result.failed.length > 0 ? (
            <>
              <p className="mt-3 font-medium text-bad">{formatNumber(result.failed.length)} kunne ikke startes:</p>
              <ul className="mt-1 max-h-48 space-y-1 overflow-y-auto text-sm text-text-2">
                {result.failed.map((entry) => <li key={entry.workflow.id}><span className="font-medium text-text">{entry.workflow.title}:</span> {errorMessage(entry.error)}</li>)}
              </ul>
            </>
          ) : null}
        </div>
      ) : (
        <>
          <p>{help}</p>
          <p className="mt-2">Det sker for {formatNumber(steps.length)} sager, én ad gangen.{choice.steps.length > steps.length ? ` Gruppen har ${formatNumber(choice.steps.length)}, så tryk igen bagefter for de næste.` : ""}</p>
          {steps[0].action.effect === "provider" ? <p className="mt-2 text-text-3">Hver sag kontakter en ekstern tjeneste og tæller med i dagens grænser.</p> : null}
          {running ? <p role="status" className="mt-3 text-sm text-text-2">{formatNumber(done)} af {formatNumber(steps.length)} er behandlet…</p> : null}
        </>
      )}
    </Dialog>
  )
}

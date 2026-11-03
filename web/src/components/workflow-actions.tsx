import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"

import { runWorkflowAction, type APIWorkflow, type APIWorkflowAction } from "@/api/client"
import { useToast } from "@/components/toast"
import { Button, Dialog } from "@/components/ui"
import { actionLabel } from "@/i18n"

const order = ["search", "open_next_round", "open_round_from_known", "discover", "reconcile_altmount", "reconfirm_plex", "retry_access_rejected_release", "cancel"]

/**
 * Renders the owner actions the server lists for a workflow. The server
 * decides what applies; the UI only asks for confirmation and sends exactly
 * the described request. Unavailable actions are hidden unless `showUnavailable`.
 */
export function WorkflowActions({ workflow, showUnavailable = false, compact = false }: { workflow: APIWorkflow; showUnavailable?: boolean; compact?: boolean }) {
  const actions = [...(workflow.available_actions ?? [])]
    .filter((action) => action.available || showUnavailable)
    .sort((a, b) => (order.indexOf(a.action) + 1 || 99) - (order.indexOf(b.action) + 1 || 99))
  const [pending, setPending] = useState<APIWorkflowAction | null>(null)
  if (actions.length === 0) return null
  return (
    <>
      <div className="flex flex-wrap gap-2">
        {actions.map((action) => {
          const { label } = actionLabel(action.action)
          return (
            <Button
              key={action.action}
              variant={action.action === "cancel" ? "ghost" : action.available && actions[0] === action ? "primary" : "secondary"}
              disabled={!action.available}
              title={action.available ? actionLabel(action.action).help : action.reason}
              onClick={() => setPending(action)}
              className={compact ? "h-8 px-3 text-xs" : undefined}
            >
              {label}
            </Button>
          )
        })}
      </div>
      {showUnavailable ? (
        <ul className="mt-3 space-y-1 text-xs text-text-3">
          {actions.filter((action) => !action.available && action.reason).map((action) => (
            <li key={action.action}><span className="font-medium text-text-2">{actionLabel(action.action).label}:</span> {action.reason}</li>
          ))}
        </ul>
      ) : null}
      {pending ? <ConfirmAction action={pending} workflow={workflow} onClose={() => setPending(null)} /> : null}
    </>
  )
}

function ConfirmAction({ action, workflow, onClose }: { action: APIWorkflowAction; workflow: APIWorkflow; onClose: () => void }) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const [phrase, setPhrase] = useState("")
  const { label, help } = actionLabel(action.action)
  const mutation = useMutation({
    mutationFn: () => runWorkflowAction(action, action.confirmation ? { [confirmationField(action)]: phrase } : undefined),
    onSuccess: async () => {
      toast("good", `${label}: sat i gang for ${workflow.title}.`)
      onClose()
      await queryClient.invalidateQueries()
    },
    onError: (error) => toast("bad", error instanceof Error ? error.message : String(error)),
  })
  const needsPhrase = Boolean(action.confirmation)
  return (
    <Dialog
      open
      onClose={onClose}
      title={`${label} – ${workflow.title}`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>Fortryd</Button>
          <Button
            variant={action.action === "cancel" || action.effect === "destructive" ? "danger" : "primary"}
            busy={mutation.isPending}
            disabled={needsPhrase && phrase !== action.confirmation}
            onClick={() => mutation.mutate()}
          >
            {label}
          </Button>
        </>
      }
    >
      <p>{help}</p>
      {action.effect === "provider" ? <p className="mt-2 text-text-3">Dette kontakter en ekstern tjeneste og tæller med i dagens grænser.</p> : null}
      {action.note ? <p className="mt-2 text-text-3">{action.note}</p> : null}
      {needsPhrase ? (
        <label className="mt-4 block">
          <span className="text-text">Skriv <code className="rounded bg-surface-3 px-1 py-0.5 text-xs">{action.confirmation}</code> for at bekræfte</span>
          <input value={phrase} onChange={(event) => setPhrase(event.target.value)} className="mt-2 h-9 w-full rounded-lg border border-line bg-surface px-3 text-text" autoComplete="off" />
        </label>
      ) : null}
    </Dialog>
  )
}

function confirmationField(action: APIWorkflowAction): string {
  return action.action === "search" ? "adopted_replacement_confirmation" : "confirmation"
}

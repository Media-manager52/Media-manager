import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"
import { Link } from "react-router"

import { requestFileReplacement, type APIManagedFile } from "@/api/client"
import { Button, Dialog } from "@/components/ui"
import { errorMessage, replacementOutcomeText } from "@/i18n"

/** Owner-requested repair applies only to a current managed AltMount publication. */
export function ReplaceFile({ file }: { file: APIManagedFile }) {
  const [open, setOpen] = useState(false)
  const queryClient = useQueryClient()
  const mutation = useMutation({
    mutationFn: () => requestFileReplacement(file),
    onSuccess: async () => {
      setOpen(false)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["media", file.media_id] }),
        queryClient.invalidateQueries({ queryKey: ["repairs"] }),
      ])
    },
  })
  if (file.origin !== "altmount" || file.state !== "current" || !file.plex_confirmed || !file.source_relative) return null
  const close = () => { if (!mutation.isPending) setOpen(false) }
  return (
    <div className="mt-2 pl-5">
      <Button variant="ghost" onClick={() => { mutation.reset(); setOpen(true) }}>Erstat dårlig fil</Button>
      {mutation.data ? <p role="status" className="mt-2 text-sm text-text-2">{replacementOutcomeText(mutation.data.outcome)} <Link to="/system/reparationer" className="underline">Se status</Link></p> : null}
      {open ? (
        <Dialog open onClose={close} title="Erstat dårlig fil?" footer={<><Button variant="ghost" disabled={mutation.isPending} onClick={close}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} onClick={() => mutation.mutate()}>Søg efter erstatning</Button></>}>
          <div className="space-y-3">
            <p>Brug dette, hvis den konkrete fil afspiller dårligt. Media Manager søger efter en anden udgivelse med samme kvalitets- og sprogkrav.</p>
            <p className="break-all font-medium">{file.root_name}: {file.destination_relative}</p>
            <p>Den nuværende fil beholdes, indtil en erstatning er kontrolleret og bekræftet i Plex. Det kan bruge et downloadforsøg.</p>
            <p>Vil du kun have højere kvalitet, skal du bruge Find bedre udgave i stedet.</p>
            {mutation.error ? <p role="alert" className="text-sm text-bad">{errorMessage(mutation.error)}</p> : null}
          </div>
        </Dialog>
      ) : null}
    </div>
  )
}

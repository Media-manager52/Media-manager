import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { FolderInput, Trash2 } from "lucide-react"
import { useState } from "react"
import { useNavigate } from "react-router"

import { ApiError, loadLibraryRoots, recycleMedia, relocateMedia, type APIMediaDetail } from "@/api/client"
import { useToast } from "@/components/toast"
import { Badge, Button, cx, Dialog } from "@/components/ui"
import { errorMessage } from "@/i18n"

/** Danish text for the refusals a move or a delete can meet. */
function actionError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === "workflow_in_flight") return "Noget af titlen hentes eller udgives lige nu. Prøv igen, når det er færdigt."
    if (error.code === "nothing_to_move") return "Titlen ligger allerede i den mappe."
    if (error.code === "library_root_disabled") return "Mappen er slået fra. Vælg en anden."
    if (error.code === "media_removed") return "Titlen er allerede slettet."
    if (error.code === "recycle_failed" && /adopted/i.test(error.message)) return "Titlen har filer, Media Manager har overtaget fra dit gamle bibliotek. De skal afklares, før titlen kan slettes."
  }
  return errorMessage(error)
}

/** Where the title lives now: the roots of its published files and open work. */
function currentRoots(detail: APIMediaDetail): { ids: Set<string>; editions: Set<string> } {
  const ids = new Set<string>()
  const editions = new Set<string>()
  for (const workflow of detail.workflows) {
    if (workflow.state === "cancelled" || !workflow.root_id) continue
    const published = detail.files.some((file) => file.state === "current" && file.variant === workflow.variant)
    if (workflow.terminal_at && !published) continue
    ids.add(workflow.root_id)
    editions.add(workflow.edition ?? "standard")
  }
  return { ids, editions }
}

export function TitleActions({ detail }: { detail: APIMediaDetail }) {
  const [dialog, setDialog] = useState<"move" | "delete" | null>(null)
  return (
    <>
      <Button variant="secondary" className="h-8 text-xs" onClick={() => setDialog("move")}><FolderInput aria-hidden className="size-3.5" />Flyt</Button>
      <Button variant="ghost" className="h-8 text-xs text-bad" onClick={() => setDialog("delete")}><Trash2 aria-hidden className="size-3.5" />Slet</Button>
      {dialog === "move" ? <MoveDialog detail={detail} onClose={() => setDialog(null)} /> : null}
      {dialog === "delete" ? <DeleteDialog detail={detail} onClose={() => setDialog(null)} /> : null}
    </>
  )
}

function MoveDialog({ detail, onClose }: { detail: APIMediaDetail; onClose: () => void }) {
  const { media } = detail
  const queryClient = useQueryClient()
  const toast = useToast()
  const roots = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const [choice, setChoice] = useState("")
  const here = currentRoots(detail)
  const options = (roots.data ?? [])
    .filter((root) => root.enabled && root.media_kind === media.kind && here.editions.has(root.edition ?? "standard"))
    .sort((a, b) => a.name.localeCompare(b.name, "da"))
  const now = options.filter((root) => here.ids.has(root.id)).map((root) => root.name)
  const target = options.find((root) => root.id === choice)
  const mutation = useMutation({
    mutationFn: () => relocateMedia(media.id, choice),
    onSuccess: async () => {
      toast("good", `${media.title} er flyttet til ${target?.name ?? "den nye mappe"}. Plex opdaterer om lidt.`)
      onClose()
      await queryClient.invalidateQueries()
    },
  })
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Flyt ${media.title}`}
      footer={<><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!choice || here.ids.has(choice)} onClick={() => mutation.mutate()}>Flyt</Button></>}
    >
      <div className="space-y-3">
        <p>Vælg det bibliotek i Plex, titlen skal ligge i.{media.kind === "series" ? " Alle sæsoner flyttes, også dem der stadig ventes på." : ""}</p>
        {now.length ? <p className="text-text-3">Ligger nu i: {now.join(", ")}</p> : null}
        {here.ids.size === 0 ? <p className="text-warn">Titlen har ingen filer eller ventende arbejde, der kan flyttes.</p> : null}
        <div role="radiogroup" aria-label="Bibliotek" className="max-h-72 space-y-1 overflow-y-auto">
          {options.map((root) => {
            const current = here.ids.has(root.id)
            return (
              <label key={root.id} className={cx("flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-2", choice === root.id ? "border-accent bg-accent-soft" : "border-line", current && "cursor-default opacity-60")}>
                <input type="radio" name="move-root" value={root.id} checked={choice === root.id} disabled={current} onChange={() => setChoice(root.id)} className="size-4" />
                <span className="min-w-0 flex-1">
                  <span className="font-medium text-text">{root.name}</span>
                  <span className="block break-all text-xs text-text-3">{root.plex_visible_root || root.destination_root}</span>
                </span>
                {root.edition === "4k" ? <Badge tone="info">4K</Badge> : null}
                {current ? <Badge>Her nu</Badge> : null}
              </label>
            )
          })}
        </div>
        <p className="text-xs text-text-3">Kun links i biblioteket flyttes. Selve filen i AltMount bliver ikke rørt.</p>
        {mutation.error ? <p role="alert" className="text-sm text-bad">{actionError(mutation.error)}</p> : null}
      </div>
    </Dialog>
  )
}

function DeleteDialog({ detail, onClose }: { detail: APIMediaDetail; onClose: () => void }) {
  const { media } = detail
  const queryClient = useQueryClient()
  const toast = useToast()
  const navigate = useNavigate()
  const inPlex = detail.files.some((file) => file.state === "current")
  const open = detail.workflows.some((workflow) => !workflow.terminal_at)
  const mutation = useMutation({
    mutationFn: () => recycleMedia(media.id, true),
    onSuccess: async () => {
      toast("good", inPlex ? `${media.title} er lagt i papirkurven. Du kan gendanne den under System → Papirkurv i 30 dage.` : `${media.title} er fjernet.`)
      onClose()
      await queryClient.invalidateQueries()
      navigate("/bibliotek")
    },
  })
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Slet ${media.title}?`}
      footer={<><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="danger" busy={mutation.isPending} onClick={() => mutation.mutate()}>Slet titel</Button></>}
    >
      <div className="space-y-3">
        {inPlex ? (
          <p>Titlen forsvinder fra Plex og fra Media Manager. Linkene lægges i papirkurven i 30 dage, så du kan fortryde under System → Papirkurv.</p>
        ) : (
          <p>Titlen er ikke i Plex, så den fjernes kun fra Media Manager. Du kan tilføje den igen senere.</p>
        )}
        {open ? <p>{media.kind === "series" ? "Søgninger og overvågning af nye afsnit stoppes." : "Søgningen efter titlen stoppes."}</p> : null}
        <p className="text-xs text-text-3">Selve filen i AltMount bliver ikke slettet.</p>
        {mutation.error ? <p role="alert" className="text-sm text-bad">{actionError(mutation.error)}</p> : null}
      </div>
    </Dialog>
  )
}

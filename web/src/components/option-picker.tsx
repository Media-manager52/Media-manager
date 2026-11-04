import { Check, ChevronsUpDown, X } from "lucide-react"
import { useEffect, useId, useRef, useState } from "react"

import { Badge, cx } from "@/components/ui"
import type { PickerOption } from "@/lib/pickers"

/**
 * A field that opens a list of described choices: a sheet from the bottom on a
 * phone, a centred panel on a larger screen. Each choice shows its name, one
 * line saying what it means and small badges, which a native select cannot.
 */
export function OptionPicker({ label, value, options, onChange, placeholder = "Vælg", required, className }: {
  label: string
  value: string
  options: PickerOption[]
  onChange: (value: string) => void
  placeholder?: string
  required?: boolean
  className?: string
}) {
  const [open, setOpen] = useState(false)
  const dialog = useRef<HTMLDialogElement>(null)
  const id = useId()
  const selected = options.find((option) => option.value === value)
  useEffect(() => {
    const element = dialog.current
    if (!element) return
    if (open && !element.open) {
      element.showModal()
      const current = element.querySelector<HTMLButtonElement>('[aria-checked="true"]') ?? element.querySelector<HTMLButtonElement>('[role="radio"]')
      current?.focus()
    }
    if (!open && element.open) element.close()
  }, [open])
  const choose = (next: string) => {
    onChange(next)
    setOpen(false)
  }
  const groups: Array<{ name?: string; items: PickerOption[] }> = []
  for (const option of options) {
    const last = groups[groups.length - 1]
    if (last && last.name === option.group) last.items.push(option)
    else groups.push({ name: option.group, items: [option] })
  }
  const missing = required && !selected
  return (
    <div className={cx("text-sm", className)}>
      <span id={`${id}-label`} className="mb-1 block font-medium">{label}</span>
      <button
        type="button"
        aria-haspopup="dialog"
        aria-labelledby={`${id}-label ${id}-value`}
        onClick={() => setOpen(true)}
        className="flex min-h-11 w-full items-center gap-3 rounded-xl border border-line bg-surface px-3 py-2 text-left transition-colors hover:border-text-3 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <span id={`${id}-value`} className="min-w-0 flex-1">
          <span className={cx("flex items-center gap-2 font-medium", missing ? "text-text-3" : "text-text")}>
            <span className="truncate">{selected?.label ?? placeholder}</span>
            {selected?.badges?.map((badge) => <Badge key={badge} tone={badge === "4K" ? "info" : "neutral"} className="shrink-0">{badge}</Badge>)}
          </span>
          {selected?.summary ?? selected?.description ? <span className="mt-0.5 block truncate text-xs text-text-3">{selected?.summary ?? selected?.description}</span> : null}
        </span>
        <ChevronsUpDown aria-hidden className="size-4 shrink-0 text-text-3" />
      </button>
      <dialog
        ref={dialog}
        onClose={() => setOpen(false)}
        onCancel={() => setOpen(false)}
        onClick={(event) => { if (event.target === dialog.current) setOpen(false) }}
        aria-labelledby={`${id}-title`}
        className="mb-0 mt-auto max-h-[85dvh] w-full max-w-none rounded-t-2xl border border-line bg-surface p-0 text-text shadow-card backdrop:bg-black/50 sm:m-auto sm:max-h-[80vh] sm:w-[min(30rem,calc(100vw-2rem))] sm:rounded-2xl"
      >
        <div className="flex max-h-[inherit] flex-col">
          <div className="flex items-center justify-between gap-3 border-b border-line px-5 py-4">
            <span aria-hidden className="absolute left-1/2 top-2 h-1 w-10 -translate-x-1/2 rounded-full bg-line sm:hidden" />
            <h2 id={`${id}-title`} className="text-base font-semibold">{label}</h2>
            <button type="button" onClick={() => setOpen(false)} aria-label="Luk" className="rounded-md p-1 text-text-3 hover:text-text">
              <X className="size-4" />
            </button>
          </div>
          <div role="radiogroup" aria-labelledby={`${id}-title`} className="overflow-y-auto overscroll-contain px-3 py-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]">
            {groups.map((group, index) => (
              <div key={group.name ?? `group-${index}`} className={cx(index > 0 && "mt-3")}>
                {group.name ? <div className="px-2 pb-1 text-xs font-semibold uppercase tracking-wide text-text-3">{group.name}</div> : null}
                <ul className="space-y-1">
                  {group.items.map((option) => {
                    const checked = option.value === value
                    return (
                      <li key={option.value || "automatic"}>
                        <button
                          type="button"
                          role="radio"
                          aria-checked={checked}
                          onClick={() => choose(option.value)}
                          className={cx(
                            "flex w-full items-start gap-3 rounded-xl border px-3 py-2.5 text-left transition-colors focus-visible:outline-2 focus-visible:outline-accent",
                            checked ? "border-accent bg-accent-soft" : "border-transparent hover:bg-surface-2",
                          )}
                        >
                          <span className="min-w-0 flex-1">
                            <span className="flex flex-wrap items-center gap-2 font-medium text-text">
                              <span className="break-words">{option.label}</span>
                              {option.badges?.map((badge) => <Badge key={badge} tone={badge === "4K" ? "info" : "neutral"}>{badge}</Badge>)}
                            </span>
                            {option.description ? <span className="mt-0.5 block break-words text-xs text-text-2">{option.description}</span> : null}
                          </span>
                          <Check aria-hidden className={cx("mt-0.5 size-4 shrink-0 text-accent", checked ? "opacity-100" : "opacity-0")} />
                        </button>
                      </li>
                    )
                  })}
                </ul>
              </div>
            ))}
          </div>
        </div>
      </dialog>
    </div>
  )
}

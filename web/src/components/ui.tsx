import { AlertTriangle, Loader2, Search, X } from "lucide-react"
import { useEffect, useId, useRef, type ButtonHTMLAttributes, type ReactNode } from "react"
import { Link } from "react-router"

import type { Tone } from "@/lib/tone"

export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ")
}

const toneClasses: Record<Tone, string> = {
  good: "bg-good-soft text-good",
  warn: "bg-warn-soft text-warn",
  bad: "bg-bad-soft text-bad",
  info: "bg-info-soft text-info",
  neutral: "bg-surface-3 text-text-2",
}

export function Badge({ tone = "neutral", children, className }: { tone?: Tone; children: ReactNode; className?: string }) {
  return (
    <span className={cx("inline-flex max-w-full items-center gap-1 truncate rounded-full px-2 py-0.5 text-xs font-medium", toneClasses[tone], className)}>
      {children}
    </span>
  )
}

export function Dot({ tone }: { tone: Tone }) {
  const color = { good: "bg-good", warn: "bg-warn", bad: "bg-bad", info: "bg-info", neutral: "bg-text-3" }[tone]
  return <span aria-hidden className={cx("inline-block size-2 shrink-0 rounded-full", color)} />
}

type ButtonVariant = "primary" | "secondary" | "ghost" | "danger"
const buttonClasses: Record<ButtonVariant, string> = {
  primary: "bg-accent text-[#1b1b1b] hover:bg-accent-strong shadow-sm",
  secondary: "bg-surface border border-line text-text hover:bg-surface-2",
  ghost: "text-text-2 hover:bg-surface-2 hover:text-text",
  danger: "bg-bad-soft text-bad hover:brightness-95",
}

export function Button({ variant = "secondary", busy, children, className, disabled, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant; busy?: boolean }) {
  return (
    <button
      type="button"
      {...rest}
      disabled={disabled || busy}
      className={cx(
        "inline-flex h-9 items-center justify-center gap-2 rounded-lg px-3.5 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50",
        buttonClasses[variant],
        className,
      )}
    >
      {busy ? <Loader2 aria-hidden className="size-4 animate-spin" /> : null}
      {children}
    </button>
  )
}

export function Card({ children, className, as: Tag = "section" }: { children: ReactNode; className?: string; as?: "section" | "div" | "article" | "li" }) {
  return <Tag className={cx("rounded-2xl border border-line bg-surface shadow-card", className)}>{children}</Tag>
}

export function CardHeader({ title, description, action }: { title: ReactNode; description?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-3 border-b border-line px-5 py-4">
      <div className="min-w-0">
        <h2 className="text-base font-semibold">{title}</h2>
        {description ? <p className="mt-0.5 text-sm text-text-2">{description}</p> : null}
      </div>
      {action}
    </div>
  )
}

export function PageHeader({ title, description, actions, eyebrow }: { title: string; description?: ReactNode; actions?: ReactNode; eyebrow?: ReactNode }) {
  useEffect(() => {
    document.title = `${title} · Media Manager`
  }, [title])
  return (
    <header className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0">
        {eyebrow ? <div className="mb-1 text-sm text-text-3">{eyebrow}</div> : null}
        <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">{title}</h1>
        {description ? <p className="mt-1 max-w-2xl text-text-2">{description}</p> : null}
      </div>
      {actions ? <div className="flex w-full flex-wrap gap-2 sm:w-auto">{actions}</div> : null}
    </header>
  )
}

export function Stat({ label, value, tone, to, hint }: { label: string; value: ReactNode; tone?: Tone; to?: string; hint?: ReactNode }) {
  const body = (
    <>
      <div className="flex items-center gap-2 text-sm text-text-2">
        {tone ? <Dot tone={tone} /> : null}
        {label}
      </div>
      <div className="tabular mt-2 text-3xl font-semibold tracking-tight">{value}</div>
      {hint ? <div className="mt-1 text-xs text-text-3">{hint}</div> : null}
    </>
  )
  const classes = "block rounded-2xl border border-line bg-surface p-4 shadow-card"
  return to ? <Link to={to} className={cx(classes, "transition-colors hover:border-text-3")}>{body}</Link> : <div className={classes}>{body}</div>
}

export function Spinner({ label = "Henter…" }: { label?: string }) {
  return (
    <div role="status" className="flex items-center gap-2 py-10 text-sm text-text-2">
      <Loader2 aria-hidden className="size-4 animate-spin" />
      {label}
    </div>
  )
}

export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden className={cx("animate-pulse rounded-lg bg-surface-3", className)} />
}

export function ErrorBox({ error, retry }: { error: unknown; retry?: () => void }) {
  const message = error instanceof Error ? error.message : String(error)
  const unauthorized = typeof error === "object" && error !== null && "status" in error && (error as { status: number }).status === 401
  return (
    <div role="alert" className="flex flex-wrap items-start gap-3 rounded-xl border border-bad/30 bg-bad-soft p-4 text-sm text-bad">
      <AlertTriangle aria-hidden className="mt-0.5 size-4 shrink-0" />
      <div className="min-w-0 flex-1">
        <div className="font-medium">{unauthorized ? "Du er ikke logget ind" : "Kunne ikke hente data"}</div>
        <div className="mt-0.5 break-words opacity-90">{unauthorized ? "Log ind igen via Cosmos, og genindlæs siden." : message}</div>
      </div>
      {retry ? <Button variant="ghost" onClick={retry} className="h-8 text-bad">Prøv igen</Button> : null}
    </div>
  )
}

export function Empty({ title, children, icon }: { title: string; children?: ReactNode; icon?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-6 py-14 text-center">
      {icon ? <div className="text-text-3">{icon}</div> : null}
      <div className="font-medium">{title}</div>
      {children ? <div className="max-w-md text-sm text-text-2">{children}</div> : null}
    </div>
  )
}

export function SearchField({ value, onChange, placeholder, label }: { value: string; onChange: (value: string) => void; placeholder: string; label: string }) {
  const id = useId()
  return (
    <div className="relative w-full sm:w-72 sm:flex-none">
      <label htmlFor={id} className="sr-only">{label}</label>
      <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-text-3" />
      <input
        id={id}
        type="search"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        className="h-9 w-full rounded-lg border border-line bg-surface pl-9 pr-3 text-sm placeholder:text-text-3"
      />
    </div>
  )
}

export function Segmented<T extends string>({ value, options, onChange, label }: { value: T; options: Array<{ value: T; label: string; count?: number }>; onChange: (value: T) => void; label: string }) {
  return (
    <div role="radiogroup" aria-label={label} className="flex max-w-full gap-1 overflow-x-auto rounded-xl bg-surface-2 p-1">
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          role="radio"
          aria-checked={value === option.value}
          onClick={() => onChange(option.value)}
          className={cx(
            "flex h-8 shrink-0 items-center gap-1.5 rounded-lg px-3 text-sm font-medium transition-colors",
            value === option.value ? "bg-surface text-text shadow-sm" : "text-text-2 hover:text-text",
          )}
        >
          {option.label}
          {option.count !== undefined ? <span className="tabular text-xs text-text-3">{option.count}</span> : null}
        </button>
      ))}
    </div>
  )
}

/** Accessible modal built on <dialog>, used for confirmations. */
export function Dialog({ open, onClose, title, children, footer }: { open: boolean; onClose: () => void; title: string; children: ReactNode; footer?: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    if (!open && dialog.open) dialog.close()
  }, [open])
  return (
    <dialog
      ref={ref}
      onClose={onClose}
      onCancel={onClose}
      className="m-auto w-[min(32rem,calc(100vw-2rem))] rounded-2xl border border-line bg-surface p-0 text-text shadow-card backdrop:bg-black/50"
    >
      <div className="flex items-start justify-between gap-3 border-b border-line px-5 py-4">
        <h2 className="text-base font-semibold">{title}</h2>
        <button type="button" onClick={onClose} aria-label="Luk" className="rounded-md p-1 text-text-3 hover:text-text">
          <X className="size-4" />
        </button>
      </div>
      <div className="px-5 py-4 text-sm text-text-2">{children}</div>
      {footer ? <div className="flex flex-wrap justify-end gap-2 border-t border-line px-5 py-3">{footer}</div> : null}
    </dialog>
  )
}

export function KeyValue({ items }: { items: Array<[string, ReactNode]> }) {
  return (
    <dl className="grid grid-cols-[minmax(7rem,auto)_1fr] gap-x-4 gap-y-2 text-sm">
      {items.map(([key, value]) => (
        <div key={key} className="contents">
          <dt className="text-text-3">{key}</dt>
          <dd className="min-w-0 break-words">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

export function Progress({ value, max, tone = "info", label }: { value: number; max: number; tone?: Tone; label: string }) {
  const percent = max > 0 ? Math.min(100, Math.max(0, (value / max) * 100)) : 0
  const color = { good: "bg-good", warn: "bg-warn", bad: "bg-bad", info: "bg-info", neutral: "bg-text-3" }[tone]
  return (
    <div role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={max} aria-valuenow={value} className="h-1.5 w-full overflow-hidden rounded-full bg-surface-3">
      <div className={cx("h-full rounded-full", color)} style={{ width: `${percent}%` }} />
    </div>
  )
}

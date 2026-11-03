import { CheckCircle2, AlertTriangle } from "lucide-react"
import { createContext, useCallback, useContext, useState, type ReactNode } from "react"

type Toast = { id: number; tone: "good" | "bad"; message: string }
const ToastContext = createContext<(tone: Toast["tone"], message: string) => void>(() => {})

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const push = useCallback((tone: Toast["tone"], message: string) => {
    const id = Date.now() + Math.random()
    setToasts((current) => [...current.slice(-2), { id, tone, message }])
    window.setTimeout(() => setToasts((current) => current.filter((toast) => toast.id !== id)), tone === "bad" ? 9000 : 5000)
  }, [])
  return (
    <ToastContext.Provider value={push}>
      {children}
      <div aria-live="polite" className="pointer-events-none fixed inset-x-0 bottom-20 z-50 flex flex-col items-center gap-2 px-4 lg:bottom-6">
        {toasts.map((toast) => (
          <div key={toast.id} role="status" className="pointer-events-auto flex max-w-md items-start gap-2 rounded-xl border border-line bg-surface px-4 py-3 text-sm shadow-card">
            {toast.tone === "good" ? <CheckCircle2 aria-hidden className="mt-0.5 size-4 shrink-0 text-good" /> : <AlertTriangle aria-hidden className="mt-0.5 size-4 shrink-0 text-bad" />}
            <span className="break-words">{toast.message}</span>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

export function useToast() {
  return useContext(ToastContext)
}

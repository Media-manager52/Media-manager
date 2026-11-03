import { Link } from "react-router"

import { Empty, PageHeader } from "@/components/ui"

export function NotFoundPage() {
  return (
    <>
      <PageHeader title="Siden findes ikke" />
      <Empty title="Der er ingen side på denne adresse.">
        <Link to="/" className="text-accent-strong underline-offset-2 hover:underline">Gå til oversigten</Link>
      </Empty>
    </>
  )
}

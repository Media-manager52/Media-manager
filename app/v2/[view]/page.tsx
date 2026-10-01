import { notFound } from "next/navigation"
import { MediaManagerV2, type V2View } from "@/components/media-manager/v2-app"

export default async function V2Page({ params }: { params: Promise<{ view: string }> }) {
  const { view } = await params
  if (!["library", "requests", "attention"].includes(view)) notFound()
  return <MediaManagerV2 view={view as V2View} />
}

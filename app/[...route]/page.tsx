import { MediaManagerApp } from "@/components/media-manager/app-shell"
import { isKnownRoute } from "@/lib/media-manager-routes"
import { notFound } from "next/navigation"

export default async function RoutedPage({ params }: { params: Promise<{ route: string[] }> }) {
  const { route } = await params
  const path = `/${route.join("/")}`
  if (!isKnownRoute(path)) notFound()
  return <MediaManagerApp path={path} />
}

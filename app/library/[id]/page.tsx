import { notFound } from "next/navigation"
import { AppTitlePage } from "@/components/media-manager/app/title"

export const metadata = { title: "Title" }

export default async function TitlePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params
  if (!/^[0-9a-f-]{36}$/i.test(id)) notFound()
  return <AppTitlePage id={id} />
}

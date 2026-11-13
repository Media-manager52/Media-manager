import { fieldClass } from "@/components/setup-forms"

/**
 * How to make a Cosmos API token, and the field to paste it in. The token is
 * kept only in the form that uses it and sent for one request.
 */
export function CosmosTokenField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return (
    <>
      <ol className="list-decimal pl-5 text-text-2">
        <li>Åbn Cosmos → Settings → fanen "API", og tryk "New Token". Lad "Read Only" være slået fra, og vælg gerne udløb "7 days".</li>
        <li>Indsæt nøglen her. Den bruges kun nu og bliver ikke gemt.</li>
      </ol>
      <label className="block">
        <span className="font-medium">Cosmos API-nøgle</span>
        <input type="password" autoComplete="off" value={value} onChange={(event) => onChange(event.target.value)} className={`${fieldClass} font-mono`} />
      </label>
    </>
  )
}

import { stateLabel } from "@/i18n"

const storageFull = (limit: string) => `Grænsen på ${limit} gemte fejlede downloads er nået. Ryd op i lageret, før flere udgivelser kan prøves.`

/**
 * Danish versions of the reasons and notes the server gives for workflow
 * actions. The API keeps its English text for agents; anything not listed is
 * shown as the server wrote it, so no detail is lost. An empty string means
 * the note only repeats the button's own help text.
 */
const patterns: Array<[RegExp, (match: RegExpMatchArray) => string]> = [
  [/^The workflow is finished\.$/, () => "Arbejdet er afsluttet."],
  [/^An AltMount job is (?:still )?active(?:; wait for it to settle)?\.$/, () => "AltMount arbejder stadig på et job. Vent, til det er færdigt."],
  [/^The retained failed-output limit \((\d+)\) is reached\b.*$/, (m) => storageFull(m[1])],
  [/^Search does not apply in state ([a-z_]+)\.$/, (m) => `Søgning gælder ikke, mens arbejdet har status "${stateLabel(m[1])}".`],
  [/^This replaces an adopted file\..*$/, () => "Dette erstatter en fil, der lå i biblioteket, før Media Manager overtog det."],
  [/^Discovery is only for waiting workflows\.$/, () => "Gælder kun arbejde, der venter."],
  [/^Discovery already found a new eligible release; open the next round instead\.$/, () => "Der er allerede fundet en ny egnet udgivelse. Brug \"Start ny runde\" i stedet."],
  [/^No exhausted round is waiting for discovery\.$/, () => "Der er ingen opbrugt runde, der venter på nye udgivelser."],
  [/^Only waiting workflows can open a new round\.$/, () => "Kun arbejde, der venter, kan starte en ny runde."],
  [/^No newly discovered eligible release is ready yet; paced discovery keeps looking\.$/, () => "Der er ingen ny egnet udgivelse endnu. Media Manager leder videre i roligt tempo."],
  [/^Only waiting workflows with an exhausted round can open a round from known releases\.$/, () => "Kun arbejde, der venter med en opbrugt runde, kan prøve kendte udgivelser."],
  [/^Only for needs_attention with an AltMount or inspection attention code\.$/, () => "Kun når arbejdet kræver handling på grund af AltMount eller filkontrollen."],
  [/^Only after a Plex confirmation timed out \(attention_code plex_timeout\)\.$/, () => "Kun når Plex ikke nåede at bekræfte filen i tide."],
  [/^Workflows that are acquiring, publishing, published or finished cannot be cancelled\.$/, () => "Arbejde, der henter, udgiver, er udgivet eller er afsluttet, kan ikke annulleres."],
  [/^Evidence-only rescan: finds new releases, never grabs\.$/, () => ""],
  [/^Refused with no_known_eligible_release when no stored release is both eligible and untried\.$/, () => "Afvises, hvis alle kendte egnede udgivelser allerede er prøvet."],
  [/^Retries (.+) from (.+) after the provider access problem was fixed\.$/, (m) => `Prøver ${m[1]} fra ${m[2]} igen, nu hvor adgangsproblemet hos indexeren er løst.`],
  [/^The round is exhausted; scheduled discovery is waiting for a newly observed eligible release\.$/, () => "Runden er opbrugt. Media Manager venter på, at en ny egnet udgivelse dukker op."],
  [/^A new eligible release is ready; the owner must start the next acquisition round\.$/, () => "En ny egnet udgivelse er klar. Tryk \"Start ny runde\" for at prøve den."],
  [/^The acquisition round is exhausted\.$/, () => "Runden er opbrugt."],
  [/^Workflow is already in progress or terminal\.$/, () => "Arbejdet er allerede i gang eller afsluttet."],
  [/^Resolve the rejected AltMount submission; a different release is not permitted\.$/, () => "AltMount afviste afsendelsen. Afklar den først; en anden udgivelse må ikke prøves imens."],
  [/^Recover the current AltMount job instead of searching for another release\.$/, () => "Afklar det nuværende AltMount-job i stedet for at søge efter en anden udgivelse."],
  [/^Owner recovery is required before another search can start\.$/, () => "Du skal afklare arbejdet, før der kan søges igen."],
  [/^Publication has started; recover or verify the existing links instead of searching again\.$/, () => "Udgivelsen til Plex er begyndt. Afklar eller kontrollér de eksisterende links i stedet for at søge igen."],
  [/^This acquisition round used all available slots; failed output remains retained for owner review\.$/, () => "Runden har brugt alle forsøg. De fejlede downloads er gemt, så du kan se på dem."],
]

export function actionReasonText(text: string | undefined | null): string {
  if (!text) return ""
  const trimmed = text.trim()
  for (const [pattern, render] of patterns) {
    const match = trimmed.match(pattern)
    if (match) return render(match)
  }
  return trimmed
}

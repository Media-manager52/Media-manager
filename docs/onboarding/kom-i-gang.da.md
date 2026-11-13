# Kom i gang med Media Manager

Media Manager finder film og serier, lader AltMount hente dem og viser dem i
Plex efter kontrol. Plex skal være installeret og virke i forvejen. AltMount
installeres sammen med Media Manager fra Cosmos-butikken og er forbundet fra
start (du kan også bruge din egen AltMount).
En indexer finder udgivelser; login til dine news-servere (Usenet-udbydere)
skriver du under **Indstillinger → Usenet-servere**. TMDB leverer
titler og oplysninger. Seerr er valgfri og kan levere familiens anmodninger.

## Installation og forbindelser

1. Få serveren installeret efter [Cosmos-vejledningen](../getting-started.md)
   eller [den manuelle vejledning](../operations/manual-install.md). Brug de
   dokumenterede udgivelsesbilleder, vedvarende datamapper og beskyttet adgang.
   En ny installation skal have sin egen database og sine egne mapper. Den må
   ikke overskrive en eksisterende installation.
2. Åbn **Kom i gang**. Grønne punkter betyder gemt opsætning. De beviser ikke,
   at alle forbindelser eller et helt download virker.
3. Under **Indstillinger → Forbindelser** vælger du **Log ind med Plex**,
   godkender hos Plex og vælger din server. Brug **Test forbindelse**, hvis du
   senere ændrer en forbindelse.
4. Installerede du AltMount sammen med Media Manager, er den allerede
   tilføjet. Har du ikke Plex endnu, så slå **Install Plex too** til ved
   installationen; så ser Plex alt fra start, og **Brug anbefalet opsætning**
   opretter selv Plex-bibliotekerne "Film" og "Serier". Ellers skal Plex kunne
   se AltMounts filer og Media Managers
   biblioteksmapper: brug **Lad Media Manager rette Plex** under **Brug
   anbefalet opsætning** (det kræver en API-nøgle fra Cosmos, som ikke bliver
   gemt), eller giv selv Plex mappen til AltMounts filer som `/mnt` (kun læse,
   med `rslave`, se [Cosmos-vejledningen](../../deploy/cosmos/README.md#altmount)).
   Uden `rslave` mister Plex drevet, hver gang AltMount genstarter, indtil Plex
   selv genstartes. Med din egen AltMount: tilføj den under
   **Forbindelser**. Adressen skal kunne nås fra Media Manager. Angiv den mappe, hvor Media Manager kan læse AltMounts færdige
   filer. Test forbindelsen. Plex skal kunne læse de samme filer, også selv om
   mapperne har forskellige navne i de to tjenester.
   Skriv derefter login til din Usenet-udbyder under **Usenet-servere**:
   adresse, port (oftest 563 med kryptering), brugernavn, adgangskode og antal
   forbindelser. **Test login** prøver det, før det gemmes. Adgangskoden gemmes
   kun i AltMount, ikke i Media Manager.
5. Tilføj TMDB og dens **API Read Access Token** under **Forbindelser**. Brug
   den lange læseadgangsnøgle fra TMDB, og test forbindelsen. Del ikke nøglen i
   en chat, et skærmbillede eller et kodelager.
6. Under **Kilder** vælger du **Tilføj indexer**, gemmer dens adgangsnøgle og
   godkender forbindelsen. En gemt indexer er først klar til søgning, når den
   er godkendt. Bevar bevidst deaktiverede kilder deaktiveret.
7. Gå tilbage til **Kom i gang**, og brug **Vis forslag** under **Brug anbefalet
   opsætning**. Læs forslaget, før du vælger **Opret det**. Biblioteker og
   profiler, som allerede findes, bevares. Manglende delte mapper skal afklares
   efter installationsvejledningen; gæt ikke på nye mapper til eksisterende filer.
8. Kontrollér **Indstillinger → Biblioteker** og **Kvalitetsprofiler**. Den
   normale danske profil kræver fulde danske undertekster fra udgivelsen eller
   dansk tale. Rigtige forced-tekster oversætter kun dele af dialogen og tæller
   ikke. Er et spor kun mærket forced, læser Media Manager et lille udsnit for at
   se, om det er en fuld tekst. Børneindhold
   kræver dansk tale. En 4K-udgave hentes ved siden af den almindelige udgave.

## Afprøv den første titel

Vælg én aftalt titel under **Tilføj titel**. Ved en serie skal du kontrollere
sæsonvalget og det viste omfang, før du tilføjer. Følg arbejdet på titlens side.
AltMount færdig betyder ikke, at titlen er færdig i Plex: fil, sprog, afsnit,
biblioteksplacering og den konkrete Plex-identitet skal først være bekræftet.

Kontrollér derefter titlen i Plex, og afprøv afspilning og eventuelle danske
undertekster. Fejl skal kunne ses og undersøges. Slet ikke den gamle fil for at
tvinge en erstatning igennem. En erstatning skal være kontrolleret og bekræftet
i Plex, før den gamle udgave kan afløses.

## Overtag et bibliotek, du allerede har

Har du allerede film og serier i Plex, så åbn **Indstillinger → Overtag
bibliotek**. Start med en lille undermappe.

1. **Find filer** læser kun mappen og ændrer ingenting.
2. Vælg titel, navn og sæson for hver fil, og tryk **Godkend** eller **Spring
   over**. Højst 25 filer ad gangen.
3. Læs listen over, hvad der vil ske, og tryk **Lås planen**.
4. Først når du har skrevet bekræftelsen og trykket **Overtag nu**, bliver filerne
   flyttet. Plex skal bekræfte hver fil, før den tæller som overtaget.

## AltMount og Plex

**System → AltMount** viser AltMounts drev, dine Usenet-servere (forbindelser,
fejl og hvor meget af kvoten der er brugt) og hvad der afspilles lige nu. Du
behøver ikke åbne AltMounts egen side.

Media Manager tjekker også, at Plex kan se AltMounts filer. Står der **Plex ser
AltMount** med rødt på oversigten, har Plex mistet drevet, typisk efter en
genstart af AltMount. Brug **Lad Media Manager rette Plex** på System → Status:
lav en API-nøgle i Cosmos (Settings → API → New Token, ikke "Read Only"),
indsæt den, se hvad der bliver ændret, og tryk **Ret Plex**. Plex får mappen med
`rslave`, så det ikke sker igen, og genstarter én gang. Nøglen bliver ikke gemt
(se [Cosmos-vejledningen](../../deploy/cosmos/README.md#altmount)).

Det samme kort står under **Brug anbefalet opsætning** på Kom i gang, når Plex
ikke kan se AltMounts filer eller Media Managers biblioteksmapper, eller mangler
et film- eller seriebibliotek. Så giver Media Manager Plex mapperne og lægger
dem i dine Plex-biblioteker (eller opretter "Film" og "Serier").

### Har du allerede den normale AltMount?

Så kan du blive ved med at bruge den: Media Manager virker med den normale
AltMount (0.4.0). Står der **Udgave: Den normale AltMount** på System →
AltMount, kan du skifte til Media Managers udgave med **Skift til Media
Managers AltMount**. Det er den samme AltMount med rettelser (en forkert del
fra én server hentes fra den næste, gamle RAR-pakker følges bedre, og drevet
tåler fejl bedre).

Kun programmet bliver skiftet. Dine indstillinger, din database, dine filer,
mapper og drevet bliver, hvor de er. Før den nye udgave starter første gang,
kopierer den databasen og indstillingerne til mappen
`before-media-manager-<tid>` i AltMounts config-mappe. Kommer den ikke op inden
for to minutter, sætter Media Manager din gamle AltMount tilbage. Du bruger en
API-nøgle fra Cosmos som ved **Ret Plex**, og den bliver ikke gemt. En AltMount,
der er nyere end Media Managers udgave, bliver ikke skiftet.

## Backup og gendannelse

**System → Backup** viser krypterede databasebackups. **Database kontrolleret**
betyder, at backupjobbet har prøvegendannet databasen i en isoleret database med
den registrerede skemaversion. Det er ikke en afprøvning af hele serveren.

**Ekstern kopiering er sat op** beskriver indstillingerne. Kun en gennemført
kopi med registreret bekræftelse tæller som dokumentation for en ekstern kopi.
Siden viser de registrerede resultater; den undersøger ikke løbende, om en gammel
kopi stadig findes hos lagertjenesten.

To forskellige nøgler skal sikres separat, uden for serveren:

- **Backup-nøglen** (`BACKUP_ENCRYPTION_KEY` eller filen udpeget af
  `BACKUP_ENCRYPTION_KEY_FILE`) åbner den krypterede backupfil. Den kan være
  oprettet automatisk; Backup-siden kan vise filens placering.
- **Nøglen til gemte forbindelser** (`SECRET_ENCRYPTION_KEY` eller filen udpeget
  af `SECRET_ENCRYPTION_KEY_FILE`) åbner de adgangsoplysninger, der er gemt krypteret i
  databasen. Bevar den oprindelige nøgle; en ny nøgle åbner ikke gamle værdier.

Gem nøglerne i sikker nøgleopbevaring. De må ikke lægges i GitHub, heller ikke
i en kodebackup. En kodebackup indeholder ikke automatisk database, miljøfiler,
mappeopsætning, medier eller nøgler.

En fuld gendannelsesprøve kræver en separat installation: gendan koden og de
nødvendige indstillinger, åbn backupfilen med den oprindelige backup-nøgle,
gendan til en tom testdatabase og brug den oprindelige forbindelsesnøgle. Verificér
versioner, forbindelser, mapper og adgang til de nødvendige filer. Isolér testen
fra produktionens køer, udbyderhandlinger og publicering. En fuld prøve er først
dokumenteret, når dens faktiske trin og resultater er registreret.

Lad en teknisk ansvarlig følge [gendannelsesproceduren](../operations/runbook.md)
og [nøglevejledningen](../operations/oldboys.md). Gendannelse over produktionen,
genstarter og udrulning kræver ejerens særskilte godkendelse. Backups og medier
må aldrig slettes automatisk som led i denne vejledning.

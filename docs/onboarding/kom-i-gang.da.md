# Kom i gang med Media Manager

Media Manager finder film og serier, lader AltMount hente dem og viser dem i
Plex efter kontrol. Plex og AltMount skal være installeret og virke i forvejen.
En indexer finder udgivelser; Usenet-adgangen sættes op i AltMount. TMDB leverer
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
4. Tilføj AltMount under **Forbindelser**. Adressen skal kunne nås fra Media
   Manager. Angiv den mappe, hvor Media Manager kan læse AltMounts færdige
   filer. Test forbindelsen. Plex skal kunne læse de samme filer, også selv om
   mapperne har forskellige navne i de to tjenester.
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

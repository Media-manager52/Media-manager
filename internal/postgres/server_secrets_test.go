package postgres

import "testing"

func TestServerOnlySecretReference(t *testing.T) {
	for _, reference := range []string{"OWNER_TOKEN", " owner_token ", "PLAYER_TOKEN", "DATABASE_URL", "BACKUP_ENCRYPTION_KEY", "BACKUP_ENCRYPTION_KEY_FILE", "SECRET_ENCRYPTION_KEY", "OLDBOYS_CREDENTIAL_ENCRYPTION_KEY", "SERVARR_API_KEY", "SEERR_WEBHOOK_SECRET", "POSTGRES_PASSWORD"} {
		if !ServerOnlySecretReference(reference) {
			t.Fatalf("%q is a server credential", reference)
		}
	}
	for _, reference := range []string{"PLEX_TOKEN", "SEERR_API_KEY", "ALTMOUNT_API_KEY", "TMDB_ACCESS_TOKEN", "NEWZNAB_EXAMPLE_API_KEY"} {
		if ServerOnlySecretReference(reference) {
			t.Fatalf("%q is an ordinary connection secret", reference)
		}
	}
}

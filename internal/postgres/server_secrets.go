package postgres

import "strings"

// serverOnlySecrets are the server's own credentials. A connection may never
// name one as its secret reference: a connection check sends the resolved
// value to the connection's own address, so naming OWNER_TOKEN or a backup
// key would hand it to whatever host the connection points at.
var serverOnlySecrets = map[string]bool{
	"OWNER_TOKEN":                    true,
	"PLAYER_TOKEN":                   true,
	"DATABASE_URL":                   true,
	"PGPASSWORD":                     true,
	"POSTGRES_PASSWORD":              true,
	"SERVARR_API_KEY":                true,
	"SEERR_WEBHOOK_SECRET":           true,
	"BACKUP_OFFSITE_WEBDAV_USERNAME": true,
	"BACKUP_OFFSITE_WEBDAV_PASSWORD": true,
}

// ServerOnlySecretReference reports whether an environment secret reference
// names one of the server's own credentials (or its _FILE form). Every
// encryption key counts, whatever its exact name.
func ServerOnlySecretReference(reference string) bool {
	name := strings.ToUpper(strings.TrimSpace(reference))
	name = strings.TrimSuffix(name, "_FILE")
	return serverOnlySecrets[name] || strings.Contains(name, "ENCRYPTION_KEY")
}

// serverOnlySecretMessage refuses a connection that names a server credential.
const serverOnlySecretMessage = "invalid integration: this secret reference names one of the server's own credentials; give the connection its own secret"

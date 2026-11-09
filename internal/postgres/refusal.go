package postgres

import "github.com/media-manager/media-manager/internal/postgres/dbcore"

// ActionRefusal is a guard refusal an owner or agent can act on. See dbcore.
type ActionRefusal = dbcore.ActionRefusal

func refuse(code, message, hint string) error { return dbcore.Refuse(code, message, hint) }

package architecture

// knownImportViolations are existing layer crossings, as
// "<file> -> <imported package>". Remove an entry when the code is fixed;
// never add one. Moving logic out of internal/postgres removes most of them.
var knownImportViolations = map[string]bool{
	"internal/httpapi/api_catalog.go -> internal/adapters/seerr":                    true,
	"internal/httpapi/request_decisions.go -> internal/adapters/seerr":              true,
	"internal/httpapi/server.go -> internal/adapters/altmount":                      true,
	"internal/httpapi/server.go -> internal/adapters/seerr":                         true,
	"internal/postgres/altmount.go -> internal/adapters/altmount":                   true,
	"internal/postgres/commission.go -> internal/adapters/newznab":                  true,
	"internal/postgres/indexer_capabilities.go -> internal/adapters/newznab":        true,
	"internal/postgres/indexer_commissioning_draft.go -> internal/adapters/newznab": true,
	"internal/postgres/indexer_commissioning_draft.go -> internal/commissioningv2":  true,
	"internal/postgres/indexer_commissioning.go -> internal/adapters/newznab":       true,
	"internal/postgres/indexer_commissioning.go -> internal/commissioningv2":        true,
	"internal/postgres/indexer_commissioning_live.go -> internal/adapters/newznab":  true,
	"internal/postgres/indexer_commissioning_live.go -> internal/commissioningv2":   true,
	"internal/postgres/oldboys.go -> internal/adapters/oldboys":                     true,
	"internal/postgres/prowlarr.go -> internal/prowlarrbackup":                      true,
	"internal/postgres/publication.go -> internal/adapters/plex":                    true,
	"internal/postgres/search.go -> internal/adapters/newznab":                      true,
	"internal/postgres/settings.go -> internal/adapters/newznab":                    true,
}

// oversizedFiles are production Go files above maxFileLines, with the line
// count they may not exceed. Shrink the number as a file is split; delete the
// entry once the file is under the limit.
var oversizedFiles = map[string]int{
	"internal/httpapi/server.go":                      2245,
	"internal/postgres/indexer_commissioning.go":      1350,
	"internal/postgres/indexer_commissioning_live.go": 1249,
	"internal/postgres/publication.go":                1161,
	"internal/commissioningv2/probe_plan.go":          1139,
	"internal/adapters/newznab/profile.go":            1133,
}

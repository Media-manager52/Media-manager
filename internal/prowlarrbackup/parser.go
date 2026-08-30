package prowlarrbackup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	ParserVersion        = "prowlarr-sqlite-v1"
	maximumBackupBytes   = 128 << 20
	maximumDatabaseBytes = 256 << 20
	maximumZipEntries    = 128
)

var (
	ErrInvalidBackup = errors.New("invalid Prowlarr backup")
	environmentToken = regexp.MustCompile(`[^A-Z0-9]+`)
)

type Candidate struct {
	Name               string          `json:"name"`
	Implementation     string          `json:"implementation"`
	BaseURL            string          `json:"base_url"`
	Categories         []int           `json:"categories"`
	SanitizedConfig    json.RawMessage `json:"sanitized_config"`
	SuggestedSecretRef string          `json:"suggested_secret_ref"`
	APIKey             string          `json:"api_key,omitempty"`
	ReviewState        string          `json:"review_state"`
	AttentionDetail    string          `json:"attention_detail,omitempty"`
}

type Result struct {
	SHA256     [32]byte    `json:"-"`
	Candidates []Candidate `json:"candidates"`
}

func Parse(ctx context.Context, backup []byte) (Result, error) {
	if len(backup) == 0 || len(backup) > maximumBackupBytes {
		return Result{}, ErrInvalidBackup
	}
	result := Result{SHA256: sha256.Sum256(backup)}
	archive, err := zip.NewReader(bytes.NewReader(backup), int64(len(backup)))
	if err != nil || len(archive.File) == 0 || len(archive.File) > maximumZipEntries {
		return Result{}, ErrInvalidBackup
	}
	var database *zip.File
	var total uint64
	for _, entry := range archive.File {
		total += entry.UncompressedSize64
		if total > maximumDatabaseBytes*2 || strings.ContainsRune(entry.Name, '\x00') {
			return Result{}, ErrInvalidBackup
		}
		if strings.EqualFold(filepath.Base(filepath.ToSlash(entry.Name)), "prowlarr.db") {
			if database != nil || entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > maximumDatabaseBytes {
				return Result{}, ErrInvalidBackup
			}
			database = entry
		}
	}
	if database == nil {
		return Result{}, fmt.Errorf("%w: prowlarr.db is missing", ErrInvalidBackup)
	}
	reader, err := database.Open()
	if err != nil {
		return Result{}, ErrInvalidBackup
	}
	defer reader.Close()
	temporary, err := os.CreateTemp("", "media-manager-prowlarr-*.db")
	if err != nil {
		return Result{}, err
	}
	path := temporary.Name()
	defer os.Remove(path)
	defer temporary.Close()
	if err := temporary.Chmod(0o600); err != nil {
		return Result{}, err
	}
	written, err := io.Copy(temporary, io.LimitReader(reader, maximumDatabaseBytes+1))
	if err != nil || written <= 16 || written > maximumDatabaseBytes {
		return Result{}, ErrInvalidBackup
	}
	if err := temporary.Sync(); err != nil {
		return Result{}, err
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return Result{}, err
	}
	header := make([]byte, 16)
	if _, err := io.ReadFull(temporary, header); err != nil || string(header) != "SQLite format 3\x00" {
		return Result{}, ErrInvalidBackup
	}
	if err := temporary.Close(); err != nil {
		return Result{}, err
	}

	databaseURL := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}).String()
	db, err := sql.Open("sqlite", databaseURL)
	if err != nil {
		return Result{}, ErrInvalidBackup
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	queryContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(queryContext); err != nil {
		return Result{}, ErrInvalidBackup
	}
	var quickCheck string
	if err := db.QueryRowContext(queryContext, `PRAGMA quick_check(1)`).Scan(&quickCheck); err != nil || !strings.EqualFold(quickCheck, "ok") {
		return Result{}, fmt.Errorf("%w: SQLite integrity check failed", ErrInvalidBackup)
	}
	table, columns, err := indexerTable(queryContext, db)
	if err != nil {
		return Result{}, err
	}
	result.Candidates, err = readCandidates(queryContext, db, table, columns)
	if err != nil {
		return Result{}, err
	}
	if len(result.Candidates) == 0 {
		return Result{}, fmt.Errorf("%w: no indexers were found", ErrInvalidBackup)
	}
	return result, nil
}

func indexerTable(ctx context.Context, db *sql.DB) (string, map[string]string, error) {
	var table string
	if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND lower(name)='indexers' LIMIT 1`).Scan(&table); err != nil {
		return "", nil, fmt.Errorf("%w: Indexers table is missing", ErrInvalidBackup)
	}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+quoteIdentifier(table)+`)`)
	if err != nil {
		return "", nil, ErrInvalidBackup
	}
	defer rows.Close()
	columns := map[string]string{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return "", nil, ErrInvalidBackup
		}
		columns[strings.ToLower(name)] = name
	}
	for _, required := range []string{"name", "implementation", "settings"} {
		if columns[required] == "" {
			return "", nil, fmt.Errorf("%w: Indexers.%s is missing", ErrInvalidBackup, required)
		}
	}
	return table, columns, rows.Err()
}

func readCandidates(ctx context.Context, db *sql.DB, table string, columns map[string]string) ([]Candidate, error) {
	expression := func(name string) string {
		if actual := columns[name]; actual != "" {
			return `COALESCE(CAST(` + quoteIdentifier(actual) + ` AS TEXT),'')`
		}
		return `''`
	}
	query := `SELECT ` + expression("name") + `,` + expression("implementation") + `,` + expression("configcontract") + `,` + expression("settings") + `,` + expression("enable") + ` FROM ` + quoteIdentifier(table) + ` ORDER BY ` + quoteIdentifier(columns["name"])
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, ErrInvalidBackup
	}
	defer rows.Close()
	var result []Candidate
	usedRefs := map[string]int{}
	for rows.Next() {
		var name, implementation, contract, settingsText, enabledText string
		if err := rows.Scan(&name, &implementation, &contract, &settingsText, &enabledText); err != nil {
			return nil, ErrInvalidBackup
		}
		name, implementation, contract = strings.TrimSpace(name), strings.TrimSpace(implementation), strings.TrimSpace(contract)
		if name == "" {
			continue
		}
		candidate := Candidate{Name: name, Implementation: implementation, ReviewState: "needs_attention"}
		if enabledText != "" && enabledText != "1" && !strings.EqualFold(enabledText, "true") {
			candidate.AttentionDetail = "Indexer is disabled in the Prowlarr backup"
		}
		var settings any
		if err := json.Unmarshal([]byte(settingsText), &settings); err != nil {
			candidate.AttentionDetail = "Prowlarr settings could not be decoded"
			candidate.SanitizedConfig = defaultSanitizedConfig("", nil)
			result = append(result, candidate)
			continue
		}
		base := firstString(settings, "baseurl", "url", "indexerurl")
		apiPath := firstString(settings, "apipath")
		candidate.APIKey = firstString(settings, "apikey")
		candidate.Categories = firstIntegers(settings, "categories")
		candidate.BaseURL, err = normalizeEndpoint(base, apiPath)
		if err != nil {
			candidate.AttentionDetail = "A safe Newznab API endpoint was not found"
		}
		candidate.SanitizedConfig = defaultSanitizedConfig(candidate.BaseURL, candidate.Categories)
		looksNewznab := strings.Contains(strings.ToLower(implementation+" "+contract), "newznab")
		if candidate.AttentionDetail == "" && looksNewznab && candidate.APIKey != "" {
			candidate.ReviewState = "pending"
		} else if candidate.AttentionDetail == "" {
			candidate.AttentionDetail = "Only Usenet Newznab indexers can be imported into the first release"
		}
		candidate.SuggestedSecretRef = suggestedSecretRef(candidate.Name, candidate.BaseURL, usedRefs)
		result = append(result, candidate)
	}
	return result, rows.Err()
}

func defaultSanitizedConfig(endpoint string, categories []int) json.RawMessage {
	allowHTTP := false
	if parsed, err := url.Parse(endpoint); err == nil {
		allowHTTP = parsed.Scheme == "http"
	}
	value, _ := json.Marshal(map[string]any{"base_url": endpoint, "allow_http": allowHTTP, "minimum_interval_ms": 3000, "mode": "search", "mode_verified": false, "categories": append([]int(nil), categories...), "extended": true})
	return value
}

func normalizeEndpoint(base, apiPath string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", ErrInvalidBackup
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	apiPath = strings.TrimSpace(apiPath)
	if apiPath != "" {
		if !strings.HasPrefix(apiPath, "/") || strings.Contains(apiPath, "\\") || strings.Contains(apiPath, "..") {
			return "", ErrInvalidBackup
		}
		if !strings.HasSuffix(strings.ToLower(parsed.Path), strings.ToLower(strings.TrimRight(apiPath, "/"))) {
			parsed.Path += strings.TrimRight(apiPath, "/")
		}
	}
	if parsed.Path == "" {
		parsed.Path = "/api"
	}
	return parsed.String(), nil
}

func firstString(value any, wanted ...string) string {
	wantedSet := make(map[string]struct{}, len(wanted))
	for _, item := range wanted {
		wantedSet[normalizeKey(item)] = struct{}{}
	}
	var visit func(any) string
	visit = func(current any) string {
		switch typed := current.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if _, ok := wantedSet[normalizeKey(key)]; ok {
					switch scalar := typed[key].(type) {
					case string:
						if strings.TrimSpace(scalar) != "" {
							return strings.TrimSpace(scalar)
						}
					case json.Number:
						return scalar.String()
					}
				}
			}
			for _, key := range keys {
				if found := visit(typed[key]); found != "" {
					return found
				}
			}
		case []any:
			for _, item := range typed {
				if found := visit(item); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return visit(value)
}

func firstIntegers(value any, wanted string) []int {
	var result []int
	var visit func(any) bool
	visit = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for key, item := range typed {
				if normalizeKey(key) == normalizeKey(wanted) {
					collectIntegers(item, &result)
					return true
				}
			}
			for _, item := range typed {
				if visit(item) {
					return true
				}
			}
		case []any:
			for _, item := range typed {
				if visit(item) {
					return true
				}
			}
		}
		return false
	}
	visit(value)
	sort.Ints(result)
	return compactIntegers(result)
}

func collectIntegers(value any, result *[]int) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			collectIntegers(item, result)
		}
	case float64:
		if typed >= 0 && typed <= 999999 && typed == float64(int(typed)) {
			*result = append(*result, int(typed))
		}
	case json.Number:
		if parsed, err := strconv.Atoi(typed.String()); err == nil && parsed >= 0 && parsed <= 999999 {
			*result = append(*result, parsed)
		}
	}
}

func compactIntegers(values []int) []int {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func suggestedSecretRef(name, endpoint string, used map[string]int) string {
	base := strings.Trim(environmentToken.ReplaceAllString(strings.ToUpper(name), "_"), "_")
	if base == "" || base[0] < 'A' || base[0] > 'Z' {
		base = "INDEXER_" + base
	}
	if len(base) > 72 {
		base = base[:72]
	}
	result := "NEWZNAB_" + base + "_API_KEY"
	used[result]++
	if used[result] > 1 {
		digest := sha256.Sum256([]byte(endpoint))
		result = fmt.Sprintf("NEWZNAB_%s_%X_API_KEY", base, digest[:3])
	}
	return result
}

func normalizeKey(value string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(value) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func quoteIdentifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

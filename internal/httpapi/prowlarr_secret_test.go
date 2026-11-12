package httpapi

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestProwlarrUploadNeverReturnsTheIndexerKeys proves the review created from
// a Prowlarr backup does not echo the API keys found in it (S18).
func TestProwlarrUploadNeverReturnsTheIndexerKeys(t *testing.T) {
	store := openHTTPTestStore(t)
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "test"}, store)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	path := t.TempDir() + "/prowlarr.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE Indexers (Name TEXT, Implementation TEXT, ConfigContract TEXT, Settings TEXT, Enable INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO Indexers VALUES (?,?,?,?,?)`, "Treasure Maps", "Newznab", "NewznabSettings", `{"baseUrl":"https://example.test","apiPath":"/api","apiKey":"prowlarr-indexer-secret-value","categories":[2000,5000]}`, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	archive := zip.NewWriter(&body)
	entry, err := archive.Create("prowlarr.db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(database); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(http.MethodPost, testServer.URL+"/api/v1/imports/prowlarr", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	request.Header.Set("Content-Type", "application/zip")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.StatusCode, payload)
	}
	if !strings.Contains(string(payload), "Treasure Maps") {
		t.Fatalf("the review lists no candidate: %s", payload)
	}
	if strings.Contains(string(payload), "prowlarr-indexer-secret-value") || strings.Contains(string(payload), "api_key") {
		t.Fatalf("the upload response contains the indexer API key: %s", payload)
	}
}

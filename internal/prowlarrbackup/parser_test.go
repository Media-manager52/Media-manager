package prowlarrbackup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

func TestParseProwlarrBackup(t *testing.T) {
	path := t.TempDir() + "/prowlarr.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE Indexers (Name TEXT, Implementation TEXT, ConfigContract TEXT, Settings TEXT, Enable INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO Indexers VALUES (?,?,?,?,?)`, "Treasure Maps", "Newznab", "NewznabSettings", `{"baseUrl":"https://example.test","apiPath":"/api","apiKey":"secret-value","categories":[2000,5000]}`, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO Indexers VALUES (?,?,?,?,?)`, "Torrent only", "Cardigann", "TorznabSettings", `{"baseUrl":"https://torrent.example","apiPath":"/api","apiKey":"torrent-secret"}`, 1); err != nil {
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
	writer := zip.NewWriter(&body)
	entry, err := writer.Create("prowlarr.db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(database); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := Parse(context.Background(), body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("candidates=%d, want 2", len(result.Candidates))
	}
	usable := result.Candidates[1]
	if result.Candidates[0].Name == "Treasure Maps" {
		usable = result.Candidates[0]
	}
	if usable.BaseURL != "https://example.test/api" || usable.APIKey != "secret-value" || usable.ReviewState != "pending" || len(usable.Categories) != 2 {
		t.Fatalf("unexpected usable candidate: %#v", usable)
	}
	for _, candidate := range result.Candidates {
		if candidate.Name == "Torrent only" && candidate.ReviewState == "pending" {
			t.Fatalf("torrent candidate was classified as Newznab: %#v", candidate)
		}
	}
}

func TestParseRejectsNonBackup(t *testing.T) {
	if _, err := Parse(context.Background(), []byte("not a zip")); err == nil {
		t.Fatal("expected invalid backup error")
	}
}

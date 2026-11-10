package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpenTurnsJITOffUnlessTheURLSetsIt(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	for _, test := range []struct{ name, url, want string }{
		{"default", databaseURL, "off"},
		{"explicit", databaseURL + separator + "jit=on", "on"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := Open(ctx, test.url)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			var jit string
			if err := store.Pool.QueryRow(ctx, `SHOW jit`).Scan(&jit); err != nil {
				t.Fatal(err)
			}
			if jit != test.want {
				t.Fatalf("jit=%q, want %q", jit, test.want)
			}
		})
	}
}

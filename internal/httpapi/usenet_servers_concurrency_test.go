package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentUsenetChangesKeepAnEnabledServer(t *testing.T) {
	for _, methods := range [][2]string{{"PATCH", "PATCH"}, {"DELETE", "DELETE"}, {"PATCH", "DELETE"}} {
		t.Run(methods[0]+"/"+methods[1], func(t *testing.T) { concurrentUsenetChanges(t, methods) })
	}
}

func concurrentUsenetChanges(t *testing.T, methods [2]string) {
	store := openHTTPTestStore(t)
	fake := &fakeUsenetAltMount{providers: []map[string]any{
		{"id": "provider_1", "host": "first.example.test", "enabled": true},
		{"id": "provider_2", "host": "second.example.test", "enabled": true},
	}}
	inner := fake.handler(t)
	var readCount atomic.Int32
	ready := make(chan struct{})
	altMount := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/config" {
			recorded := httptest.NewRecorder()
			inner.ServeHTTP(recorded, r)
			if readCount.Add(1) == 2 {
				close(ready)
			}
			select {
			case <-ready:
			case <-time.After(time.Second):
				// A serialized second request cannot read until the first finishes.
			}
			for k, vals := range recorded.Header() {
				w.Header()[k] = vals
			}
			w.WriteHeader(recorded.Code)
			_, _ = w.Write(recorded.Body.Bytes())
			return
		}
		inner.ServeHTTP(w, r)
	}))
	defer altMount.Close()
	if _, err := store.Pool.Exec(context.Background(), `UPDATE integration_instances SET enabled=true,config=$1 WHERE kind='altmount'`, `{"base_url":"`+altMount.URL+`","allow_http":true}`); err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{OwnerToken: testOwnerToken, Version: "review", ResolveSecret: func(string) (string, error) { return `{"username":"mm-user","password":"mm-pass"}`, nil }}, store)
	if err != nil {
		t.Fatal(err)
	}
	otherServer, err := New(Config{OwnerToken: testOwnerToken, Version: "test", ResolveSecret: func(string) (string, error) { return `{"username":"mm-user","password":"mm-pass"}`, nil }}, store)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1)%2 == 0 {
			otherServer.Handler().ServeHTTP(w, r)
		} else {
			server.Handler().ServeHTTP(w, r)
		}
	}))
	defer api.Close()
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i, id := range []string{"provider_1", "provider_2"} {
		method := methods[i]
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			req, _ := http.NewRequest(method, api.URL+"/api/v1/settings/usenet-servers/"+id, strings.NewReader(`{"enabled":false}`))
			req.Header.Set("Authorization", "Bearer "+testOwnerToken)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			statuses <- resp.StatusCode
		}(id)
	}
	wg.Wait()
	close(statuses)
	got := map[int]int{}
	for status := range statuses {
		got[status]++
	}
	if got[http.StatusOK] != 1 || got[http.StatusConflict] != 1 {
		t.Fatalf("responses=%v, want one success and one refusal", got)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	enabled := 0
	for _, p := range fake.providers {
		if p["enabled"] == true {
			enabled++
		}
	}
	if enabled == 0 {
		t.Fatal("both concurrent disable requests succeeded; zero Usenet servers remain enabled")
	}
}

package acquisition

import (
	"errors"
	"testing"
)

func TestNewAltMountClientAnonymousAdminDoesNotResolveSecret(t *testing.T) {
	resolved := false
	client, err := newAltMountClient(altMountConfig{
		BaseURL:        "http://altmount.invalid:8080",
		Authentication: "anonymous_admin",
		AllowHTTP:      true,
	}, "ALTMOUNT_SECRET", func(string) (string, error) {
		resolved = true
		return "", errors.New("should not resolve a session secret")
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client == nil || resolved {
		t.Fatalf("unexpected anonymous client result: client=%v resolved=%v", client != nil, resolved)
	}
}

func TestNewAltMountClientSessionResolvesCredentials(t *testing.T) {
	resolvedReference := ""
	client, err := newAltMountClient(altMountConfig{
		BaseURL:        "http://altmount.invalid:8080",
		Authentication: "session",
		AllowHTTP:      true,
	}, "ALTMOUNT_SECRET", func(reference string) (string, error) {
		resolvedReference = reference
		return `{"username":"owner","password":"password"}`, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client == nil || resolvedReference != "ALTMOUNT_SECRET" {
		t.Fatalf("unexpected session client result: client=%v reference=%q", client != nil, resolvedReference)
	}
}

func TestNewAltMountClientRejectsUnknownAuthentication(t *testing.T) {
	_, err := newAltMountClient(altMountConfig{
		BaseURL:        "http://altmount.invalid:8080",
		Authentication: "api_key",
		AllowHTTP:      true,
	}, "ALTMOUNT_SECRET", nil, nil)
	if err == nil || err.Error() != "unsupported AltMount authentication mode" {
		t.Fatalf("expected unsupported mode error, got %v", err)
	}
}

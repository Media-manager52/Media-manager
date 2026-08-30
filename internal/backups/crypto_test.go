package backups

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestEncryptedBackupRoundTripAndAuthentication(t *testing.T) {
	key := bytes.Repeat([]byte{42}, 32)
	plaintext := bytes.Repeat([]byte("Media Manager backup payload\n"), 100000)
	var encrypted bytes.Buffer
	if err := Encrypt(&encrypted, bytes.NewReader(plaintext), key); err != nil {
		t.Fatal(err)
	}
	var decrypted bytes.Buffer
	if err := Decrypt(&decrypted, bytes.NewReader(encrypted.Bytes()), key); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Fatal("decrypted backup differs from source")
	}
	tampered := append([]byte(nil), encrypted.Bytes()...)
	tampered[len(tampered)-8] ^= 1
	if err := Decrypt(&bytes.Buffer{}, bytes.NewReader(tampered), key); err == nil {
		t.Fatal("tampered backup was accepted")
	}
}

func TestParseKeyRequiresAES256Material(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	if key, err := ParseKey(encoded); err != nil || len(key) != 32 {
		t.Fatalf("key length=%d err=%v", len(key), err)
	}
	if _, err := ParseKey("not-a-key"); err == nil {
		t.Fatal("invalid key was accepted")
	}
}

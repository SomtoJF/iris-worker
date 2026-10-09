package sqldb

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
)

func TestBrowserSecretEncryptionIsBoundToApplicationBrowserID(t *testing.T) {
	t.Setenv(browserDataEncryptionKeyEnv, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	id := uuid.New()
	ciphertext, err := encryptBrowserSecret("https://kernel.example/live?token=secret", id)
	if err != nil {
		t.Fatalf("encryptBrowserSecret: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("secret")) {
		t.Fatal("ciphertext contains the plaintext token")
	}
	got, err := decryptBrowserSecret(ciphertext, id)
	if err != nil {
		t.Fatalf("decryptBrowserSecret: %v", err)
	}
	if got != "https://kernel.example/live?token=secret" {
		t.Fatalf("decrypted URL = %q", got)
	}
	if _, err := decryptBrowserSecret(ciphertext, uuid.New()); err == nil {
		t.Fatal("ciphertext decrypted for a different application browser ID")
	}
}

func TestBrowserSecretRequiresValidKey(t *testing.T) {
	t.Setenv(browserDataEncryptionKeyEnv, "invalid")
	if _, err := encryptBrowserSecret("value", uuid.New()); err == nil {
		t.Fatal("expected invalid key error")
	}
}

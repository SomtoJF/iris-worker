package sqldb

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
)

const browserDataEncryptionKeyEnv = "BROWSER_DATA_ENCRYPTION_KEY"

func encryptBrowserSecret(plaintext string, applicationBrowserID uuid.UUID) ([]byte, error) {
	key, err := browserDataEncryptionKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create browser secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create browser secret AEAD: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate browser secret nonce: %w", err)
	}
	sealed := aead.Seal(nil, nonce, []byte(plaintext), []byte(applicationBrowserID.String()))
	return append(nonce, sealed...), nil
}

func decryptBrowserSecret(ciphertext []byte, applicationBrowserID uuid.UUID) (string, error) {
	key, err := browserDataEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create browser secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create browser secret AEAD: %w", err)
	}
	if len(ciphertext) < aead.NonceSize()+aead.Overhead() {
		return "", errors.New("browser secret ciphertext is malformed")
	}
	nonce, sealed := ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, sealed, []byte(applicationBrowserID.String()))
	if err != nil {
		return "", fmt.Errorf("decrypt browser secret: %w", err)
	}
	return string(plaintext), nil
}

func browserDataEncryptionKey() ([]byte, error) {
	encoded := os.Getenv(browserDataEncryptionKeyEnv)
	if encoded == "" {
		return nil, fmt.Errorf("%s is required to encrypt browser secrets", browserDataEncryptionKeyEnv)
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("%s must be a base64-encoded 32-byte key", browserDataEncryptionKeyEnv)
	}
	return key, nil
}

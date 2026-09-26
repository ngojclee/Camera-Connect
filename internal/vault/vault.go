// Package vault seals sensitive payloads (rclone.conf) with a user passphrase
// before they touch AppDB — the server only ever sees the encrypted blob.
//
// Wire format is byte-compatible with LNC-Proxy lib/cred-vault.js:
//
//	PBKDF2-SHA256, 150_000 iterations → AES-256-GCM (16B salt, 12B IV)
//	plaintext = {"tag":"lnc-cred-vault-v1","payload":<payload>}
//	blob      = {format, alg, kdf, iter, salt(b64), iv(b64), data(b64)}
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/crypto/pbkdf2"
)

const (
	iterations = 150_000
	tag        = "lnc-cred-vault-v1"
	saltLen    = 16
	ivLen      = 12
	keyLen     = 32
)

// Blob is the encrypted wire format stored in AppDB settings.
// Field names deliberately avoid sensitive-sounding keys (same as JS).
type Blob struct {
	Format string `json:"format"`
	Alg    string `json:"alg"`
	KDF    string `json:"kdf"`
	Iter   int    `json:"iter"`
	Salt   string `json:"salt"`
	IV     string `json:"iv"`
	Data   string `json:"data"`
}

// ErrAuth is returned on wrong passphrase or tampered blob (GCM auth fail).
var ErrAuth = errors.New("vault: decryption failed (wrong passphrase or tampered data)")

// ErrMalformed is returned when the blob structurally isn't a vault blob.
var ErrMalformed = errors.New("vault: blob is malformed")

func deriveKey(passphrase string, salt []byte) []byte {
	return pbkdf2.Key([]byte(passphrase), salt, iterations, keyLen, sha256.New)
}

// Seal encrypts payload (any JSON-marshalable value) with the passphrase.
func Seal(passphrase string, payload any) (*Blob, error) {
	if passphrase == "" {
		return nil, errors.New("vault: passphrase required")
	}
	salt := make([]byte, saltLen)
	iv := make([]byte, ivLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	key := deriveKey(passphrase, salt)

	plaintext, err := json.Marshal(map[string]any{"tag": tag, "payload": payload})
	if err != nil {
		return nil, fmt.Errorf("vault: marshal payload: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, iv, plaintext, nil)

	return &Blob{
		Format: tag,
		Alg:    "AES-GCM",
		KDF:    "PBKDF2-SHA256",
		Iter:   iterations,
		Salt:   base64.StdEncoding.EncodeToString(salt),
		IV:     base64.StdEncoding.EncodeToString(iv),
		Data:   base64.StdEncoding.EncodeToString(sealed),
	}, nil
}

// Open decrypts a vault blob. Returns ErrMalformed for structurally invalid
// blobs, ErrAuth for wrong passphrase/tampering.
func Open(passphrase string, blob *Blob, out any) error {
	if blob == nil || blob.Salt == "" || blob.IV == "" || blob.Data == "" {
		return ErrMalformed
	}
	salt, err := base64.StdEncoding.DecodeString(blob.Salt)
	if err != nil || len(salt) != saltLen {
		return ErrMalformed
	}
	iv, err := base64.StdEncoding.DecodeString(blob.IV)
	if err != nil || len(iv) != ivLen {
		return ErrMalformed
	}
	data, err := base64.StdEncoding.DecodeString(blob.Data)
	if err != nil {
		return ErrMalformed
	}

	key := deriveKey(passphrase, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	plaintext, err := gcm.Open(nil, iv, data, nil)
	if err != nil {
		return ErrAuth
	}

	var decoded struct {
		Tag     string          `json:"tag"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(plaintext, &decoded); err != nil {
		return ErrAuth
	}
	if decoded.Tag != tag {
		return ErrMalformed
	}
	if out != nil {
		if err := json.Unmarshal(decoded.Payload, out); err != nil {
			return fmt.Errorf("vault: unmarshal payload: %w", err)
		}
	}
	return nil
}

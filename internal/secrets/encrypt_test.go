package secrets_test

import (
	"bytes"
	"testing"

	"github.com/softsrv/ergracer/internal/secrets"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	enc, err := secrets.New(key)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("hello, ergracer oauth token")
	ct, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}

	got, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, plaintext) {
		t.Errorf("got %q, want %q", got, plaintext)
	}
}

func TestEncryptProducesUniqueCiphertexts(t *testing.T) {
	key := make([]byte, 32)
	enc, _ := secrets.New(key)
	plaintext := []byte("same input")

	ct1, _ := enc.Encrypt(plaintext)
	ct2, _ := enc.Encrypt(plaintext)

	if bytes.Equal(ct1, ct2) {
		t.Error("two encryptions of the same plaintext should not be identical (random nonce)")
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	enc, _ := secrets.New(key)

	ct, _ := enc.Encrypt([]byte("secret"))
	ct[len(ct)-1] ^= 0xFF // flip last byte

	if _, err := enc.Decrypt(ct); err == nil {
		t.Error("expected error decrypting tampered ciphertext")
	}
}

func TestNewRejectsWrongKeyLength(t *testing.T) {
	if _, err := secrets.New(make([]byte, 16)); err == nil {
		t.Error("expected error for 16-byte key")
	}
}

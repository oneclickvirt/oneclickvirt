package utils

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testPrivateKeyPEM(t *testing.T, passphrase string) string {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(key, "test")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(key, "test", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block))
}

func TestParseSSHPrivateKeySupportsEncryptedAndUnencryptedKeys(t *testing.T) {
	const passphrase = "test-passphrase"

	if _, err := ParseSSHPrivateKey(testPrivateKeyPEM(t, ""), ""); err != nil {
		t.Fatalf("unencrypted key should parse: %v", err)
	}
	encrypted := testPrivateKeyPEM(t, passphrase)
	if _, err := ParseSSHPrivateKey(encrypted, passphrase); err != nil {
		t.Fatalf("encrypted key should parse with its passphrase: %v", err)
	}
	if _, err := ParseSSHPrivateKey(encrypted, ""); err == nil {
		t.Fatal("encrypted key should reject an empty passphrase")
	}
	if _, err := ParseSSHPrivateKey(encrypted, "wrong"); err == nil {
		t.Fatal("encrypted key should reject a wrong passphrase")
	} else if strings.Contains(err.Error(), passphrase) {
		t.Fatal("parse error must not expose the passphrase")
	}
}

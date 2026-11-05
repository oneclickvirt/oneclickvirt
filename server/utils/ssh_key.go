package utils

import "golang.org/x/crypto/ssh"

// ParseSSHPrivateKey parses an SSH private key with the optional passphrase
// supplied by the node administrator. An empty passphrase intentionally uses
// the normal unencrypted-key parser so existing keys keep working.
//
// The key and passphrase are never logged here. Callers should preserve that
// property when wrapping or reporting the returned error.
func ParseSSHPrivateKey(privateKey, passphrase string) (ssh.Signer, error) {
	if passphrase == "" {
		return ssh.ParsePrivateKey([]byte(privateKey))
	}
	return ssh.ParsePrivateKeyWithPassphrase([]byte(privateKey), []byte(passphrase))
}

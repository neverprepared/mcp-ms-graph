// OS keychain accessors for the two secrets we never write to disk:
// the Ably API key and the encryption passphrase shared with the browser extension.
package secrets

import (
	"fmt"

	"github.com/zalando/go-keyring"
)

const (
	Service           = "mcp-ms-graph"
	passphraseAccount = "encryption-passphrase"
	ablyKeyAccount    = "ably-api-key"
)

func GetPassphrase() (string, error) {
	v, err := keyring.Get(Service, passphraseAccount)
	if err != nil || v == "" {
		return "", fmt.Errorf("no encryption passphrase in keychain (service=%q account=%q); run `mcp-ms-graph setup`", Service, passphraseAccount)
	}
	return v, nil
}

func SetPassphrase(value string) error {
	return keyring.Set(Service, passphraseAccount, value)
}

func GetAblyKey() (string, error) {
	v, err := keyring.Get(Service, ablyKeyAccount)
	if err != nil || v == "" {
		return "", fmt.Errorf("no Ably API key in keychain (service=%q account=%q); run `mcp-ms-graph setup`", Service, ablyKeyAccount)
	}
	return v, nil
}

func SetAblyKey(value string) error {
	return keyring.Set(Service, ablyKeyAccount, value)
}

func Clear() {
	_ = keyring.Delete(Service, passphraseAccount)
	_ = keyring.Delete(Service, ablyKeyAccount)
}

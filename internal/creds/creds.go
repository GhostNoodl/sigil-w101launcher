// Package creds stores account passwords in the operating system's secure
// credential store (Windows Credential Manager) rather than on disk.
package creds

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const serviceName = "w101client"

// ErrNotFound is returned when no password is stored for the username.
var ErrNotFound = keyring.ErrNotFound

func Set(username, password string) error {
	return keyring.Set(serviceName, username, password)
}

func Get(username string) (string, error) {
	pw, err := keyring.Get(serviceName, username)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return pw, err
}

func Delete(username string) error {
	err := keyring.Delete(serviceName, username)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

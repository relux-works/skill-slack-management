package config

import (
	"errors"

	"github.com/zalando/go-keyring"
)

var ErrSecretNotFound = errors.New("secret not found")

type keychainStore struct {
	get    func(service, user string) (string, error)
	set    func(service, user, password string) error
	delete func(service, user string) error
}

func NewDefaultKeychainStore() KeychainStore {
	return NewKeychainStore(keyring.Get, keyring.Set, keyring.Delete)
}

func NewKeychainStore(
	getFn func(service, user string) (string, error),
	setFn func(service, user, password string) error,
	deleteFn func(service, user string) error,
) KeychainStore {
	return &keychainStore{
		get:    getFn,
		set:    setFn,
		delete: deleteFn,
	}
}

func (k *keychainStore) Get(service, user string) (string, error) {
	value, err := k.get(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrSecretNotFound
	}
	return value, err
}

func (k *keychainStore) Set(service, user, password string) error {
	return k.set(service, user, password)
}

func (k *keychainStore) Delete(service, user string) error {
	err := k.delete(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrSecretNotFound
	}
	return err
}

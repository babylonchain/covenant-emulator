package main

import "github.com/cosmos/cosmos-sdk/crypto/keyring"

const (
	homeFlag           = "home"
	forceFlag          = "force"
	keyNameFlag        = "key-name"
	passphraseFlag     = "passphrase"
	hdPathFlag         = "hd-path"
	chainIdFlag        = "chain-id"
	keyringBackendFlag = "keyring-backend"

	defaultChainID        = "chain-test"
	defaultKeyringBackend = keyring.BackendTest
	defaultPassphrase     = ""
	defaultHdPath         = ""
)

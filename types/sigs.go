package types

import (
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
)

type CovenantSigs struct {
	PublicKey             *btcec.PublicKey
	StakingTxHash         chainhash.Hash
	SlashingSigs          [][]byte
	UnbondingSig          *schnorr.Signature
	SlashingUnbondingSigs [][]byte
}

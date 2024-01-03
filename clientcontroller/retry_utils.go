package clientcontroller

import (
	sdkErr "cosmossdk.io/errors"
	btcstakingtypes "github.com/babylonchain/babylon/x/btcstaking/types"
)

// these errors are considered unrecoverable because these indicate
// something critical in the finality provider program or the consumer chain
var unrecoverableErrors = []*sdkErr.Error{
	btcstakingtypes.ErrInvalidCovenantPK,
	btcstakingtypes.ErrInvalidCovenantSig,
}

var expectedErrors = []*sdkErr.Error{}

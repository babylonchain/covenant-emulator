package testutil

import (
	"encoding/hex"
	"math/rand"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/babylonchain/babylon/testutil/datagen"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/stretchr/testify/require"

	"github.com/babylonchain/covenant-emulator/types"
)

func GenRandomByteArray(r *rand.Rand, length uint64) []byte {
	newHeaderBytes := make([]byte, length)
	r.Read(newHeaderBytes)
	return newHeaderBytes
}

func GenRandomHexStr(r *rand.Rand, length uint64) string {
	randBytes := GenRandomByteArray(r, length)
	return hex.EncodeToString(randBytes)
}

func AddRandomSeedsToFuzzer(f *testing.F, num uint) {
	// Seed based on the current time
	r := rand.New(rand.NewSource(time.Now().Unix()))
	var idx uint
	for idx = 0; idx < num; idx++ {
		f.Add(r.Int63())
	}
}

func GenValidSlashingRate(r *rand.Rand) sdkmath.LegacyDec {
	return sdkmath.LegacyNewDecWithPrec(int64(datagen.RandomInt(r, 41)+10), 2)
}

func GenRandomParams(r *rand.Rand, t *testing.T) *types.StakingParams {
	covThreshold := datagen.RandomInt(r, 5) + 1
	covNum := covThreshold * 2
	covenantPks := make([]*btcec.PublicKey, 0, covNum)
	for i := 0; i < int(covNum); i++ {
		_, covPk, err := datagen.GenRandomBTCKeyPair(r)
		require.NoError(t, err)
		covenantPks = append(covenantPks, covPk)
	}

	slashingAddr, err := datagen.GenRandomBTCAddress(r, &chaincfg.SimNetParams)
	require.NoError(t, err)
	return &types.StakingParams{
		ComfirmationTimeBlocks:    10,
		FinalizationTimeoutBlocks: 100,
		MinSlashingTxFeeSat:       1,
		CovenantPks:               covenantPks,
		SlashingAddress:           slashingAddr,
		CovenantQuorum:            uint32(covThreshold),
		SlashingRate:              GenValidSlashingRate(r),
	}
}

func GenBtcPublicKeys(r *rand.Rand, t *testing.T, num int) []*btcec.PublicKey {
	pks := make([]*btcec.PublicKey, 0, num)
	for i := 0; i < num; i++ {
		_, covPk, err := datagen.GenRandomBTCKeyPair(r)
		require.NoError(t, err)
		pks = append(pks, covPk)
	}

	return pks
}

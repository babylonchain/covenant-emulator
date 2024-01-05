package covenant_test

import (
	"encoding/hex"
	"math/rand"
	"testing"

	"github.com/babylonchain/babylon/btcstaking"
	asig "github.com/babylonchain/babylon/crypto/schnorr-adaptor-signature"
	"github.com/babylonchain/babylon/testutil/datagen"
	bbntypes "github.com/babylonchain/babylon/types"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	covcfg "github.com/babylonchain/covenant-emulator/config"
	"github.com/babylonchain/covenant-emulator/covenant"
	"github.com/babylonchain/covenant-emulator/testutil"
	"github.com/babylonchain/covenant-emulator/types"
)

const (
	passphrase = "testpass"
	hdPath     = ""
)

var net = &chaincfg.SimNetParams

func FuzzAddCovenantSig(f *testing.F) {
	testutil.AddRandomSeedsToFuzzer(f, 10)
	f.Fuzz(func(t *testing.T, seed int64) {
		r := rand.New(rand.NewSource(seed))

		params := testutil.GenRandomParams(r, t)
		mockClientController := testutil.PrepareMockedClientController(t, params)

		// create a Covenant key pair in the keyring
		covenantConfig := covcfg.DefaultConfig()
		covKeyPair, err := covenant.CreateCovenantKey(
			covenantConfig.BabylonConfig.KeyDirectory,
			covenantConfig.BabylonConfig.ChainID,
			covenantConfig.BabylonConfig.Key,
			covenantConfig.BabylonConfig.KeyringBackend,
			passphrase,
			hdPath,
		)
		require.NoError(t, err)

		// create and start covenant emulator
		ce, err := covenant.NewCovenantEmulator(&covenantConfig, mockClientController, passphrase, zap.NewNop())
		require.NoError(t, err)

		err = ce.UpdateParams()
		require.NoError(t, err)

		// generate BTC delegation
		delSK, delPK, err := datagen.GenRandomBTCKeyPair(r)
		require.NoError(t, err)
		stakingTimeBlocks := uint16(5)
		stakingValue := int64(2 * 10e8)
		unbondingTime := uint16(params.FinalizationTimeoutBlocks) + 1
		fpNum := datagen.RandomInt(r, 5) + 1
		fpPks := testutil.GenBtcPublicKeys(r, t, int(fpNum))
		testInfo := datagen.GenBTCStakingSlashingInfo(
			r,
			t,
			net,
			delSK,
			fpPks,
			params.CovenantPks,
			params.CovenantQuorum,
			stakingTimeBlocks,
			stakingValue,
			params.SlashingAddress.String(),
			params.SlashingRate,
			unbondingTime,
		)
		stakingTxBytes, err := bbntypes.SerializeBTCTx(testInfo.StakingTx)
		require.NoError(t, err)
		startHeight := datagen.RandomInt(r, 1000) + 100
		btcDel := &types.Delegation{
			BtcPk:            delPK,
			FpBtcPks:         fpPks,
			StartHeight:      startHeight, // not relevant here
			EndHeight:        startHeight + uint64(stakingTimeBlocks),
			TotalSat:         uint64(stakingValue),
			UnbondingTime:    uint32(unbondingTime),
			StakingTxHex:     hex.EncodeToString(stakingTxBytes),
			StakingOutputIdx: 0,
			SlashingTxHex:    testInfo.SlashingTx.ToHexStr(),
		}
		// generate covenant staking sigs
		slashingSpendInfo, err := testInfo.StakingInfo.SlashingPathSpendInfo()
		require.NoError(t, err)
		covSigs := make([][]byte, 0, len(fpPks))
		for _, fpPk := range fpPks {
			encKey, err := asig.NewEncryptionKeyFromBTCPK(fpPk)
			require.NoError(t, err)
			covenantSig, err := testInfo.SlashingTx.EncSign(
				testInfo.StakingTx,
				0,
				slashingSpendInfo.GetPkScriptPath(),
				covKeyPair.PrivateKey, encKey,
			)
			require.NoError(t, err)
			covSigs = append(covSigs, covenantSig.MustMarshal())
		}

		// generate undelegation
		unbondingValue := int64(btcDel.TotalSat) - 1000

		stakingTxHash := testInfo.StakingTx.TxHash()
		testUnbondingInfo := datagen.GenBTCUnbondingSlashingInfo(
			r,
			t,
			net,
			delSK,
			btcDel.FpBtcPks,
			params.CovenantPks,
			params.CovenantQuorum,
			wire.NewOutPoint(&stakingTxHash, 0),
			unbondingTime,
			unbondingValue,
			params.SlashingAddress.String(),
			params.SlashingRate,
			unbondingTime,
		)
		require.NoError(t, err)
		// random signer
		unbondingTxMsg := testUnbondingInfo.UnbondingTx

		unbondingSlashingPathInfo, err := testUnbondingInfo.UnbondingInfo.SlashingPathSpendInfo()
		require.NoError(t, err)

		serializedUnbondingTx, err := bbntypes.SerializeBTCTx(testUnbondingInfo.UnbondingTx)
		require.NoError(t, err)
		undel := &types.Undelegation{
			UnbondingTxHex: hex.EncodeToString(serializedUnbondingTx),
			SlashingTxHex:  testUnbondingInfo.SlashingTx.ToHexStr(),
		}
		btcDel.BtcUndelegation = undel
		stakingTxUnbondingPathInfo, err := testInfo.StakingInfo.UnbondingPathSpendInfo()
		require.NoError(t, err)
		// generate covenant unbonding sigs
		unbondingCovSig, err := btcstaking.SignTxWithOneScriptSpendInputStrict(
			unbondingTxMsg,
			testInfo.StakingTx,
			btcDel.StakingOutputIdx,
			stakingTxUnbondingPathInfo.GetPkScriptPath(),
			covKeyPair.PrivateKey,
		)
		require.NoError(t, err)
		// generate covenant unbonding slashing sigs
		unbondingCovSlashingSigs := make([][]byte, 0, len(fpPks))
		for _, fpPk := range fpPks {
			encKey, err := asig.NewEncryptionKeyFromBTCPK(fpPk)
			require.NoError(t, err)
			covenantSig, err := testUnbondingInfo.SlashingTx.EncSign(
				testUnbondingInfo.UnbondingTx,
				0,
				unbondingSlashingPathInfo.GetPkScriptPath(),
				covKeyPair.PrivateKey,
				encKey,
			)
			require.NoError(t, err)
			unbondingCovSlashingSigs = append(unbondingCovSlashingSigs, covenantSig.MustMarshal())
		}

		// check the sigs are expected
		expectedTxHash := testutil.GenRandomHexStr(r, 32)
		mockClientController.EXPECT().SubmitCovenantSigs(
			covKeyPair.PublicKey,
			testInfo.StakingTx.TxHash().String(),
			covSigs,
			unbondingCovSig,
			unbondingCovSlashingSigs,
		).
			Return(&types.TxResponse{TxHash: expectedTxHash}, nil).AnyTimes()
		res, err := ce.AddCovenantSignatures(btcDel)
		require.NoError(t, err)
		require.Equal(t, expectedTxHash, res.TxHash)
	})
}

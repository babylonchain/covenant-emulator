package e2etest

import (
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/babylonchain/babylon/testutil/datagen"
	bbntypes "github.com/babylonchain/babylon/types"
	btcctypes "github.com/babylonchain/babylon/x/btccheckpoint/types"
	btclctypes "github.com/babylonchain/babylon/x/btclightclient/types"
	bstypes "github.com/babylonchain/babylon/x/btcstaking/types"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	covcc "github.com/babylonchain/covenant-emulator/clientcontroller"
	covcfg "github.com/babylonchain/covenant-emulator/config"
	"github.com/babylonchain/covenant-emulator/covenant"
	"github.com/babylonchain/covenant-emulator/testutil"
	"github.com/babylonchain/covenant-emulator/types"
)

var (
	eventuallyWaitTimeOut = 1 * time.Minute
	eventuallyPollTime    = 500 * time.Millisecond
	btcNetworkParams      = &chaincfg.SimNetParams

	covenantKeyName = "covenant-key"
	chainID         = "chain-test"
	passphrase      = "testpass"
	hdPath          = ""
)

type TestManager struct {
	Wg               sync.WaitGroup
	BabylonHandler   *BabylonNodeHandler
	CovenantEmulator *covenant.CovenantEmulator
	CovenanConfig    *covcfg.Config
	CovBBNClient     *covcc.BabylonController
	StakingParams    *types.StakingParams
	baseDir          string
}

type TestDelegationData struct {
	DelegatorPrivKey *btcec.PrivateKey
	DelegatorKey     *btcec.PublicKey
	SlashingTx       *bstypes.BTCSlashingTx
	StakingTx        *wire.MsgTx
	StakingTxInfo    *btcctypes.TransactionInfo
	DelegatorSig     *bbntypes.BIP340Signature
	FpPks            []*btcec.PublicKey

	SlashingAddr  string
	StakingTime   uint16
	StakingAmount int64
}

type testFinalityProviderData struct {
	BabylonPrivKey   *secp256k1.PrivKey
	BabylonPublicKey *secp256k1.PubKey
	BtcPrivKey       *btcec.PrivateKey
	BtcKey           *btcec.PublicKey
	PoP              *bstypes.ProofOfPossession
}

func StartManager(t *testing.T) *TestManager {
	testDir, err := baseDir("cee2etest")
	require.NoError(t, err)

	logger := zap.NewNop()

	// 1. prepare covenant key, which will be used as input of Babylon node
	covenantConfig := defaultCovenantConfig(testDir)
	err = covenantConfig.Validate()
	require.NoError(t, err)
	covKeyPair, err := covenant.CreateCovenantKey(testDir, chainID, covenantKeyName, keyring.BackendTest, passphrase, hdPath)
	require.NoError(t, err)

	// 2. prepare Babylon node
	bh := NewBabylonNodeHandler(t, bbntypes.NewBIP340PubKeyFromBTCPK(covKeyPair.PublicKey))
	err = bh.Start()
	require.NoError(t, err)

	// 3. prepare covenant emulator
	bbnCfg := defaultBBNConfigWithKey("test-spending-key", bh.GetNodeDataDir())
	covbc, err := covcc.NewBabylonController(bbnCfg, &covenantConfig.BTCNetParams, logger)
	require.NoError(t, err)
	ce, err := covenant.NewCovenantEmulator(covenantConfig, covbc, passphrase, logger)
	require.NoError(t, err)
	err = ce.Start()
	require.NoError(t, err)

	tm := &TestManager{
		BabylonHandler:   bh,
		CovenantEmulator: ce,
		CovenanConfig:    covenantConfig,
		CovBBNClient:     covbc,
		baseDir:          testDir,
	}

	tm.WaitForServicesStart(t)

	return tm
}

func (tm *TestManager) WaitForServicesStart(t *testing.T) {
	// wait for Babylon node starts
	require.Eventually(t, func() bool {
		params, err := tm.CovBBNClient.QueryStakingParamsByVersion(0)
		if err != nil {
			return false
		}
		tm.StakingParams = params
		return true
	}, eventuallyWaitTimeOut, eventuallyPollTime)

	t.Logf("Babylon node is started")
}

func StartManagerWithFinalityProvider(t *testing.T, n int) (*TestManager, []*btcec.PublicKey) {
	tm := StartManager(t)

	var btcPks []*btcec.PublicKey
	for i := 0; i < n; i++ {
		fpData := genTestFinalityProviderData(t)
		btcPubKey := bbntypes.NewBIP340PubKeyFromBTCPK(fpData.BtcKey)
		_, err := tm.CovBBNClient.RegisterFinalityProvider(
			fpData.BabylonPublicKey,
			btcPubKey,
			&tm.StakingParams.MinComissionRate,
			&stakingtypes.Description{
				Moniker: "tester",
			},
			fpData.PoP,
		)
		require.NoError(t, err)

		btcPks = append(btcPks, fpData.BtcKey)
	}

	// check finality providers on Babylon side
	require.Eventually(t, func() bool {
		fps, err := tm.CovBBNClient.QueryFinalityProviders()
		if err != nil {
			t.Logf("failed to query finality providers from Babylon %s", err.Error())
			return false
		}

		return len(fps) == n
	}, eventuallyWaitTimeOut, eventuallyPollTime)

	t.Logf("the test manager is running with %v finality-provider(s)", n)

	return tm, btcPks
}

func genTestFinalityProviderData(t *testing.T) *testFinalityProviderData {
	finalityProviderEOTSPrivKey, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	finalityProviderBabylonPrivKey := secp256k1.GenPrivKey()
	finalityProviderBabylonPubKey := finalityProviderBabylonPrivKey.PubKey().(*secp256k1.PubKey)
	pop, err := bstypes.NewPoP(finalityProviderBabylonPrivKey, finalityProviderEOTSPrivKey)
	require.NoError(t, err)

	return &testFinalityProviderData{
		BabylonPrivKey:   finalityProviderBabylonPrivKey,
		BabylonPublicKey: finalityProviderBabylonPubKey,
		BtcPrivKey:       finalityProviderEOTSPrivKey,
		BtcKey:           finalityProviderEOTSPrivKey.PubKey(),
		PoP:              pop,
	}
}

func (tm *TestManager) Stop(t *testing.T) {
	err := tm.CovenantEmulator.Stop()
	require.NoError(t, err)
	err = tm.BabylonHandler.Stop()
	require.NoError(t, err)
	err = os.RemoveAll(tm.baseDir)
	require.NoError(t, err)
}

func (tm *TestManager) WaitForNPendingDels(t *testing.T, n int) []*types.Delegation {
	var (
		dels []*types.Delegation
		err  error
	)
	require.Eventually(t, func() bool {
		dels, err = tm.CovBBNClient.QueryPendingDelegations(
			tm.CovenanConfig.DelegationLimit,
		)
		if err != nil {
			return false
		}
		return len(dels) == n
	}, eventuallyWaitTimeOut, eventuallyPollTime)

	t.Logf("delegations are pending")

	return dels
}

func (tm *TestManager) WaitForNActiveDels(t *testing.T, n int) []*types.Delegation {
	var (
		dels []*types.Delegation
		err  error
	)
	require.Eventually(t, func() bool {
		dels, err = tm.CovBBNClient.QueryActiveDelegations(
			tm.CovenanConfig.DelegationLimit,
		)
		if err != nil {
			return false
		}
		return len(dels) == n
	}, eventuallyWaitTimeOut, eventuallyPollTime)

	t.Logf("delegations are active")

	return dels
}

func (tm *TestManager) InsertBTCDelegation(t *testing.T, fpPks []*btcec.PublicKey, stakingTime uint16, stakingAmount int64) *TestDelegationData {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	params := tm.StakingParams

	// delegator BTC key pairs, staking tx and slashing tx
	delBtcPrivKey, delBtcPubKey, err := datagen.GenRandomBTCKeyPair(r)
	require.NoError(t, err)

	unbondingTime := uint16(tm.StakingParams.MinimumUnbondingTime()) + 1
	testStakingInfo := testutil.GenBTCStakingSlashingInfo(
		r,
		t,
		btcNetworkParams,
		delBtcPrivKey,
		fpPks,
		params.CovenantPks,
		params.CovenantQuorum,
		stakingTime,
		stakingAmount,
		params.SlashingAddress.String(),
		params.SlashingRate,
		unbondingTime,
	)

	// proof-of-possession
	pop, err := bstypes.NewPoPBTC(tm.CovBBNClient.GetKeyAddress(), delBtcPrivKey)
	require.NoError(t, err)

	// create and insert BTC headers which include the staking tx to get staking tx info
	currentBtcTipResp, err := tm.CovBBNClient.QueryBtcLightClientTip()
	require.NoError(t, err)
	tipHeader, err := bbntypes.NewBTCHeaderBytesFromHex(currentBtcTipResp.HeaderHex)
	require.NoError(t, err)
	blockWithStakingTx := datagen.CreateBlockWithTransaction(r, tipHeader.ToBlockHeader(), testStakingInfo.StakingTx)
	accumulatedWork := btclctypes.CalcWork(&blockWithStakingTx.HeaderBytes)
	accumulatedWork = btclctypes.CumulativeWork(accumulatedWork, currentBtcTipResp.Work)
	parentBlockHeaderInfo := &btclctypes.BTCHeaderInfo{
		Header: &blockWithStakingTx.HeaderBytes,
		Hash:   blockWithStakingTx.HeaderBytes.Hash(),
		Height: currentBtcTipResp.Height + 1,
		Work:   &accumulatedWork,
	}
	headers := make([]bbntypes.BTCHeaderBytes, 0)
	headers = append(headers, blockWithStakingTx.HeaderBytes)
	for i := 0; i < int(params.ComfirmationTimeBlocks); i++ {
		headerInfo := datagen.GenRandomValidBTCHeaderInfoWithParent(r, *parentBlockHeaderInfo)
		headers = append(headers, *headerInfo.Header)
		parentBlockHeaderInfo = headerInfo
	}
	_, err = tm.CovBBNClient.InsertBtcBlockHeaders(headers)
	require.NoError(t, err)
	btcHeader := blockWithStakingTx.HeaderBytes
	serializedStakingTx, err := bbntypes.SerializeBTCTx(testStakingInfo.StakingTx)
	require.NoError(t, err)
	txInfo := btcctypes.NewTransactionInfo(&btcctypes.TransactionKey{Index: 1, Hash: btcHeader.Hash()}, serializedStakingTx, blockWithStakingTx.SpvProof.MerkleNodes)

	slashignSpendInfo, err := testStakingInfo.StakingInfo.SlashingPathSpendInfo()
	require.NoError(t, err)

	// delegator sig
	delegatorSig, err := testStakingInfo.SlashingTx.Sign(
		testStakingInfo.StakingTx,
		1,
		slashignSpendInfo.GetPkScriptPath(),
		delBtcPrivKey,
	)
	require.NoError(t, err)

	unbondingValue := stakingAmount - 1000
	stakingTxHash := testStakingInfo.StakingTx.TxHash()

	testUnbondingInfo := datagen.GenBTCUnbondingSlashingInfo(
		r,
		t,
		btcNetworkParams,
		delBtcPrivKey,
		fpPks,
		params.CovenantPks,
		params.CovenantQuorum,
		wire.NewOutPoint(&stakingTxHash, 1),
		unbondingTime,
		unbondingValue,
		params.SlashingAddress.String(),
		params.SlashingRate,
		unbondingTime,
	)

	unbondingTxMsg := testUnbondingInfo.UnbondingTx

	unbondingSlashingPathInfo, err := testUnbondingInfo.UnbondingInfo.SlashingPathSpendInfo()
	require.NoError(t, err)

	unbondingSig, err := testUnbondingInfo.SlashingTx.Sign(
		unbondingTxMsg,
		0,
		unbondingSlashingPathInfo.GetPkScriptPath(),
		delBtcPrivKey,
	)
	require.NoError(t, err)

	serializedUnbondingTx, err := bbntypes.SerializeBTCTx(testUnbondingInfo.UnbondingTx)
	require.NoError(t, err)

	// submit the BTC delegation to Babylon
	_, err = tm.CovBBNClient.CreateBTCDelegation(
		bbntypes.NewBIP340PubKeyFromBTCPK(delBtcPubKey),
		fpPks,
		pop,
		uint32(stakingTime),
		stakingAmount,
		txInfo,
		testStakingInfo.SlashingTx,
		delegatorSig,
		serializedUnbondingTx,
		uint32(unbondingTime),
		unbondingValue,
		testUnbondingInfo.SlashingTx,
		unbondingSig)
	require.NoError(t, err)

	t.Log("successfully submitted a BTC delegation")

	return &TestDelegationData{
		DelegatorPrivKey: delBtcPrivKey,
		DelegatorKey:     delBtcPubKey,
		FpPks:            fpPks,
		StakingTx:        testStakingInfo.StakingTx,
		SlashingTx:       testStakingInfo.SlashingTx,
		StakingTxInfo:    txInfo,
		DelegatorSig:     delegatorSig,
		SlashingAddr:     params.SlashingAddress.String(),
		StakingTime:      stakingTime,
		StakingAmount:    stakingAmount,
	}
}

func defaultBBNConfigWithKey(key, keydir string) *covcfg.BBNConfig {
	bbnCfg := covcfg.DefaultBBNConfig()
	bbnCfg.Key = key
	bbnCfg.KeyDirectory = keydir
	bbnCfg.GasAdjustment = 20

	return &bbnCfg
}

func defaultCovenantConfig(homeDir string) *covcfg.Config {
	cfg := covcfg.DefaultConfigWithHomePath(homeDir)
	cfg.BabylonConfig.KeyDirectory = homeDir

	return &cfg
}

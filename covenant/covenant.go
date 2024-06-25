package covenant

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/babylonchain/babylon/btcstaking"
	asig "github.com/babylonchain/babylon/crypto/schnorr-adaptor-signature"
	bbntypes "github.com/babylonchain/babylon/types"
	bstypes "github.com/babylonchain/babylon/x/btcstaking/types"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	"go.uber.org/zap"

	"github.com/babylonchain/covenant-emulator/clientcontroller"
	covcfg "github.com/babylonchain/covenant-emulator/config"
	"github.com/babylonchain/covenant-emulator/keyring"
	"github.com/babylonchain/covenant-emulator/types"
)

var (
	// TODO: Maybe configurable?
	RtyAttNum = uint(5)
	RtyAtt    = retry.Attempts(RtyAttNum)
	RtyDel    = retry.Delay(time.Millisecond * 400)
	RtyErr    = retry.LastErrorOnly(true)
)

type CovenantEmulator struct {
	startOnce sync.Once
	stopOnce  sync.Once

	wg   sync.WaitGroup
	quit chan struct{}

	pk *btcec.PublicKey

	cc clientcontroller.ClientController
	kc *keyring.ChainKeyringController

	config *covcfg.Config
	logger *zap.Logger

	// input is used to pass passphrase to the keyring
	input      *strings.Reader
	passphrase string
}

func NewCovenantEmulator(
	config *covcfg.Config,
	cc clientcontroller.ClientController,
	passphrase string,
	logger *zap.Logger,
) (*CovenantEmulator, error) {
	input := strings.NewReader("")
	kr, err := keyring.CreateKeyring(
		config.BabylonConfig.KeyDirectory,
		config.BabylonConfig.ChainID,
		config.BabylonConfig.KeyringBackend,
		input,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create keyring: %w", err)
	}

	kc, err := keyring.NewChainKeyringControllerWithKeyring(kr, config.BabylonConfig.Key, input)
	if err != nil {
		return nil, err
	}

	sk, err := kc.GetChainPrivKey(passphrase)
	if err != nil {
		return nil, fmt.Errorf("covenant key %s is not found: %w", config.BabylonConfig.Key, err)
	}

	pk, err := btcec.ParsePubKey(sk.PubKey().Bytes())
	if err != nil {
		return nil, err
	}

	return &CovenantEmulator{
		cc:         cc,
		kc:         kc,
		config:     config,
		logger:     logger,
		input:      input,
		passphrase: passphrase,
		pk:         pk,
		quit:       make(chan struct{}),
	}, nil
}

func (ce *CovenantEmulator) Config() *covcfg.Config {
	return ce.config
}

func (ce *CovenantEmulator) PublicKeyStr() string {
	return hex.EncodeToString(schnorr.SerializePubKey(ce.pk))
}

// AddCovenantSignatures adds Covenant signatures on every given Bitcoin delegations and submits them
// in a batch to Babylon. Invalid delegations will be skipped with error log error will be returned if
// the batch submission fails
func (ce *CovenantEmulator) AddCovenantSignatures(btcDels []*types.Delegation) (*types.TxResponse, error) {
	if len(btcDels) == 0 {
		return nil, fmt.Errorf("no delegations")
	}
	covenantSigs := make([]*types.CovenantSigs, 0, len(btcDels))
	for _, btcDel := range btcDels {
		// 0. nil checks
		if btcDel == nil {
			ce.logger.Error("empty delegation")
			continue
		}

		if btcDel.BtcUndelegation == nil {
			ce.logger.Error("empty undelegation",
				zap.String("staking_tx_hex", btcDel.StakingTxHex))
			continue
		}

		// 1. get the params matched to the delegation version
		params, err := ce.getParamsByVersionWithRetry(btcDel.ParamsVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to get staking params with version %d: %w", btcDel.ParamsVersion, err)
		}

		// 2. the quorum is already achieved, skip sending more sigs
		stakerPkHex := hex.EncodeToString(schnorr.SerializePubKey(btcDel.BtcPk))
		if btcDel.HasCovenantQuorum(params.CovenantQuorum) {
			ce.logger.Error("covenant signatures already fulfilled",
				zap.String("staker_pk", stakerPkHex),
				zap.String("staking_tx_hex", btcDel.StakingTxHex),
			)
			continue
		}

		// 3. check unbonding time (staking time from unbonding tx) is larger than min unbonding time
		// which is larger value from:
		// - MinUnbondingTime
		// - CheckpointFinalizationTimeout
		unbondingTime := btcDel.UnbondingTime
		minUnbondingTime := params.MinimumUnbondingTime()
		if uint64(unbondingTime) <= minUnbondingTime {
			ce.logger.Error("invalid unbonding time",
				zap.Uint64("min_unbonding_time", minUnbondingTime),
				zap.Uint32("got_unbonding_time", unbondingTime),
			)
			continue
		}

		// 4. decode staking tx and slashing tx from the delegation
		stakingTx, slashingTx, err := decodeDelegationTransactions(btcDel, params, &ce.config.BTCNetParams)
		if err != nil {
			ce.logger.Error("invalid delegation",
				zap.String("staker_pk", stakerPkHex),
				zap.String("staking_tx_hex", btcDel.StakingTxHex),
				zap.String("slashing_tx_hex", btcDel.SlashingTxHex),
				zap.Error(err),
			)
			continue
		}

		// 5. decode unbonding tx and slash unbonding tx from the undelegation
		unbondingTx, slashUnbondingTx, err := decodeUndelegationTransactions(btcDel, params, &ce.config.BTCNetParams)
		if err != nil {
			ce.logger.Error("invalid undelegation",
				zap.String("staker_pk", stakerPkHex),
				zap.String("unbonding_tx_hex", btcDel.BtcUndelegation.UnbondingTxHex),
				zap.String("unbonding_slashing_tx_hex", btcDel.BtcUndelegation.SlashingTxHex),
				zap.Error(err),
			)
			continue
		}

		// 6. sign covenant staking sigs
		// record metrics
		startSignTime := time.Now()
		metricsTimeKeeper.SetPreviousSignStart(&startSignTime)

		covenantPrivKey, err := ce.getPrivKey()
		if err != nil {
			return nil, fmt.Errorf("failed to get Covenant private key: %w", err)
		}

		slashSigs, unbondingSig, err := signSlashAndUnbondSignatures(
			btcDel,
			stakingTx,
			slashingTx,
			unbondingTx,
			covenantPrivKey,
			params,
			&ce.config.BTCNetParams,
		)
		if err != nil {
			ce.logger.Error("failed to sign signatures or unbonding signature", zap.Error(err))
			continue
		}

		// 7. sign covenant slash unbonding signatures
		slashUnbondingSigs, err := signSlashUnbondingSignatures(
			btcDel,
			unbondingTx,
			slashUnbondingTx,
			covenantPrivKey,
			params,
			&ce.config.BTCNetParams,
		)
		if err != nil {
			ce.logger.Error("failed to slash unbonding signature", zap.Error(err))
			continue
		}

		// record metrics
		finishSignTime := time.Now()
		metricsTimeKeeper.SetPreviousSignFinish(&finishSignTime)
		timedSignDelegationLag.Observe(time.Since(startSignTime).Seconds())

		// 8. collect covenant sigs
		covenantSigs = append(covenantSigs, &types.CovenantSigs{
			PublicKey:             ce.pk,
			StakingTxHash:         stakingTx.TxHash(),
			SlashingSigs:          slashSigs,
			UnbondingSig:          unbondingSig,
			SlashingUnbondingSigs: slashUnbondingSigs,
		})
	}

	// 9. submit covenant sigs
	res, err := ce.cc.SubmitCovenantSigs(covenantSigs)
	if err != nil {
		ce.recordMetricsFailedSignDelegations(len(covenantSigs))
		return nil, err
	}

	// record metrics
	submittedTime := time.Now()
	metricsTimeKeeper.SetPreviousSubmission(&submittedTime)
	ce.recordMetricsTotalSignDelegationsSubmitted(len(covenantSigs))

	return res, nil
}

func signSlashUnbondingSignatures(
	del *types.Delegation,
	unbondingTx *wire.MsgTx,
	slashUnbondingTx *wire.MsgTx,
	covPrivKey *btcec.PrivateKey,
	params *types.StakingParams,
	btcNet *chaincfg.Params,
) ([][]byte, error) {
	unbondingInfo, err := btcstaking.BuildUnbondingInfo(
		del.BtcPk,
		del.FpBtcPks,
		params.CovenantPks,
		params.CovenantQuorum,
		uint16(del.UnbondingTime),
		btcutil.Amount(unbondingTx.TxOut[0].Value),
		btcNet,
	)
	if err != nil {
		return nil, err
	}

	unbondingTxSlashingPath, err := unbondingInfo.SlashingPathSpendInfo()
	if err != nil {
		return nil, err
	}

	slashUnbondingSigs := make([][]byte, 0, len(del.FpBtcPks))
	for _, fpPk := range del.FpBtcPks {
		encKey, err := asig.NewEncryptionKeyFromBTCPK(fpPk)
		if err != nil {
			return nil, err
		}
		slashUnbondingSig, err := btcstaking.EncSignTxWithOneScriptSpendInputStrict(
			slashUnbondingTx,
			unbondingTx,
			0, // 0th output is always the unbonding script output
			unbondingTxSlashingPath.GetPkScriptPath(),
			covPrivKey,
			encKey,
		)
		if err != nil {
			return nil, err
		}
		slashUnbondingSigs = append(slashUnbondingSigs, slashUnbondingSig.MustMarshal())
	}

	return slashUnbondingSigs, nil
}

func signSlashAndUnbondSignatures(
	del *types.Delegation,
	stakingTx *wire.MsgTx,
	slashingTx *wire.MsgTx,
	unbondingTx *wire.MsgTx,
	covPrivKey *btcec.PrivateKey,
	params *types.StakingParams,
	btcNet *chaincfg.Params,
) ([][]byte, *schnorr.Signature, error) {

	// sign slash signatures with every finality providers
	stakingInfo, err := btcstaking.BuildStakingInfo(
		del.BtcPk,
		del.FpBtcPks,
		params.CovenantPks,
		params.CovenantQuorum,
		del.GetStakingTime(),
		btcutil.Amount(del.TotalSat),
		btcNet,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build staking info: %w", err)
	}

	slashingPathInfo, err := stakingInfo.SlashingPathSpendInfo()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get slashing path info: %w", err)
	}

	slashSigs := make([][]byte, 0, len(del.FpBtcPks))
	for _, fpPk := range del.FpBtcPks {
		fpPkHex := bbntypes.NewBIP340PubKeyFromBTCPK(fpPk).MarshalHex()
		encKey, err := asig.NewEncryptionKeyFromBTCPK(fpPk)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get encryption key from finality provider public key %s: %w",
				fpPkHex, err)
		}
		slashSig, err := btcstaking.EncSignTxWithOneScriptSpendInputStrict(
			slashingTx,
			stakingTx,
			del.StakingOutputIdx,
			slashingPathInfo.GetPkScriptPath(),
			covPrivKey,
			encKey,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to sign adaptor signature with finaliyt provider public key %s: %w",
				fpPkHex, err)
		}
		slashSigs = append(slashSigs, slashSig.MustMarshal())
	}

	// sign unbonding sig
	stakingTxUnbondingPathInfo, err := stakingInfo.UnbondingPathSpendInfo()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get unbonding path spend info")
	}
	unbondingSig, err := btcstaking.SignTxWithOneScriptSpendInputStrict(
		unbondingTx,
		stakingTx,
		del.StakingOutputIdx,
		stakingTxUnbondingPathInfo.GetPkScriptPath(),
		covPrivKey,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sign unbonding tx: %w", err)
	}

	return slashSigs, unbondingSig, nil
}

func decodeDelegationTransactions(del *types.Delegation, params *types.StakingParams, btcNet *chaincfg.Params) (*wire.MsgTx, *wire.MsgTx, error) {
	// 1. decode staking tx and slashing tx
	stakingMsgTx, _, err := bbntypes.NewBTCTxFromHex(del.StakingTxHex)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode staking tx from hex")
	}

	slashingTx, err := bstypes.NewBTCSlashingTxFromHex(del.SlashingTxHex)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode slashing tx from hex")
	}

	slashingMsgTx, err := slashingTx.ToMsgTx()
	if err != nil {
		return nil, nil, err
	}

	// 2. verify the transactions
	if err := btcstaking.CheckTransactions(
		slashingMsgTx,
		stakingMsgTx,
		del.StakingOutputIdx,
		int64(params.MinSlashingTxFeeSat),
		params.SlashingRate,
		params.SlashingAddress,
		del.BtcPk,
		uint16(del.UnbondingTime),
		btcNet,
	); err != nil {
		return nil, nil, fmt.Errorf("invalid txs in the delegation: %w", err)
	}

	return stakingMsgTx, slashingMsgTx, nil
}

func decodeUndelegationTransactions(del *types.Delegation, params *types.StakingParams, btcNet *chaincfg.Params) (*wire.MsgTx, *wire.MsgTx, error) {
	// 1. decode unbonding tx and slashing tx
	unbondingMsgTx, _, err := bbntypes.NewBTCTxFromHex(del.BtcUndelegation.UnbondingTxHex)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode unbonding tx from hex: %w", err)
	}

	unbondingSlashingMsgTx, _, err := bbntypes.NewBTCTxFromHex(del.BtcUndelegation.SlashingTxHex)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode unbonding slashing tx from hex: %w", err)
	}

	// 2. verify transactions
	if err := btcstaking.CheckTransactions(
		unbondingSlashingMsgTx,
		unbondingMsgTx,
		0,
		int64(params.MinSlashingTxFeeSat),
		params.SlashingRate,
		params.SlashingAddress,
		del.BtcPk,
		uint16(del.UnbondingTime),
		btcNet,
	); err != nil {
		return nil, nil, fmt.Errorf("invalid txs in the undelegation: %w", err)
	}

	return unbondingMsgTx, unbondingSlashingMsgTx, err
}

func (ce *CovenantEmulator) getPrivKey() (*btcec.PrivateKey, error) {
	sdkPrivKey, err := ce.kc.GetChainPrivKey(ce.passphrase)
	if err != nil {
		return nil, err
	}

	privKey, _ := btcec.PrivKeyFromBytes(sdkPrivKey.Key)

	return privKey, nil
}

// delegationsToBatches takes a list of delegations and splits them into batches
func (ce *CovenantEmulator) delegationsToBatches(dels []*types.Delegation) [][]*types.Delegation {
	batchSize := ce.config.SigsBatchSize
	batches := make([][]*types.Delegation, 0)

	for i := uint64(0); i < uint64(len(dels)); i += batchSize {
		end := i + batchSize
		if end > uint64(len(dels)) {
			end = uint64(len(dels))
		}
		batches = append(batches, dels[i:end])
	}

	return batches
}

// removeAlreadySigned removes any delegations that have already been signed by the covenant
func (ce *CovenantEmulator) removeAlreadySigned(dels []*types.Delegation) []*types.Delegation {
	sanitized := make([]*types.Delegation, 0, len(dels))

	for _, del := range dels {
		delCopy := del
		alreadySigned := false
		for _, covSig := range delCopy.CovenantSigs {
			if bytes.Equal(schnorr.SerializePubKey(covSig.Pk), schnorr.SerializePubKey(ce.pk)) {
				alreadySigned = true
				break
			}
		}
		if !alreadySigned {
			sanitized = append(sanitized, delCopy)
		}
	}
	return sanitized
}

// covenantSigSubmissionLoop is the reactor to submit Covenant signature for BTC delegations
func (ce *CovenantEmulator) covenantSigSubmissionLoop() {
	defer ce.wg.Done()

	interval := ce.config.QueryInterval
	limit := ce.config.DelegationLimit
	covenantSigTicker := time.NewTicker(interval)

	ce.logger.Info("starting signature submission loop",
		zap.Float64("interval seconds", interval.Seconds()))

	for {
		select {
		case <-covenantSigTicker.C:
			// 1. Get all pending delegations
			dels, err := ce.cc.QueryPendingDelegations(limit)
			if err != nil {
				ce.logger.Debug("failed to get pending delegations", zap.Error(err))
				continue
			}

			// record delegation metrics
			ce.recordMetricsCurrentPendingDelegations(len(dels))

			if len(dels) == 0 {
				ce.logger.Debug("no pending delegations are found")
			}
			// 2. Remove delegations that do not need the covenant's signature
			sanitizedDels := ce.removeAlreadySigned(dels)

			// 3. Split delegations into batches for submission
			batches := ce.delegationsToBatches(sanitizedDels)
			for _, delBatch := range batches {
				_, err := ce.AddCovenantSignatures(delBatch)
				if err != nil {
					ce.logger.Error(
						"failed to submit covenant signatures for BTC delegations",
						zap.Error(err),
					)
				}
			}

		case <-ce.quit:
			ce.logger.Debug("exiting covenant signature submission loop")
			return
		}
	}

}

func (ce *CovenantEmulator) metricsUpdateLoop() {
	defer ce.wg.Done()

	interval := ce.config.Metrics.UpdateInterval
	ce.logger.Info("starting metrics update loop",
		zap.Float64("interval seconds", interval.Seconds()))
	updateTicker := time.NewTicker(interval)

	for {
		select {
		case <-updateTicker.C:
			metricsTimeKeeper.UpdatePrometheusMetrics()
		case <-ce.quit:
			updateTicker.Stop()
			ce.logger.Info("exiting metrics update loop")
			return
		}
	}
}

func CreateCovenantKey(keyringDir, chainID, keyName, backend, passphrase, hdPath string) (*types.ChainKeyInfo, error) {
	sdkCtx, err := keyring.CreateClientCtx(
		keyringDir, chainID,
	)
	if err != nil {
		return nil, err
	}

	krController, err := keyring.NewChainKeyringController(
		sdkCtx,
		keyName,
		backend,
	)
	if err != nil {
		return nil, err
	}

	return krController.CreateChainKey(passphrase, hdPath)
}

func (ce *CovenantEmulator) getParamsByVersionWithRetry(version uint32) (*types.StakingParams, error) {
	var (
		params *types.StakingParams
		err    error
	)

	if err := retry.Do(func() error {
		params, err = ce.cc.QueryStakingParamsByVersion(version)
		if err != nil {
			return err
		}
		return nil
	}, RtyAtt, RtyDel, RtyErr, retry.OnRetry(func(n uint, err error) {
		ce.logger.Debug(
			"failed to query the consumer chain for the staking params",
			zap.Uint("attempt", n+1),
			zap.Uint("max_attempts", RtyAttNum),
			zap.Error(err),
		)
	})); err != nil {
		return nil, err
	}

	return params, nil
}

func (ce *CovenantEmulator) recordMetricsFailedSignDelegations(n int) {
	failedSignDelegations.WithLabelValues(ce.PublicKeyStr()).Add(float64(n))
}

func (ce *CovenantEmulator) recordMetricsTotalSignDelegationsSubmitted(n int) {
	totalSignDelegationsSubmitted.WithLabelValues(ce.PublicKeyStr()).Add(float64(n))
}

func (ce *CovenantEmulator) recordMetricsCurrentPendingDelegations(n int) {
	currentPendingDelegations.WithLabelValues(ce.PublicKeyStr()).Set(float64(n))
}

func (ce *CovenantEmulator) Start() error {
	var startErr error
	ce.startOnce.Do(func() {
		ce.logger.Info("Starting Covenant Emulator")

		ce.wg.Add(2)
		go ce.covenantSigSubmissionLoop()
		go ce.metricsUpdateLoop()
	})

	return startErr
}

func (ce *CovenantEmulator) Stop() error {
	var stopErr error
	ce.stopOnce.Do(func() {
		ce.logger.Info("Stopping Covenant Emulator")

		close(ce.quit)
		ce.wg.Wait()

		ce.logger.Debug("Covenant Emulator successfully stopped")
	})
	return stopErr
}

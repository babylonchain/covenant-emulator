package main

import (
	"fmt"
	"path/filepath"

	covcfg "github.com/babylonchain/covenant-emulator/config"
	"github.com/babylonchain/covenant-emulator/log"
	"github.com/babylonchain/covenant-emulator/util"

	"github.com/lightningnetwork/lnd/signal"
	"github.com/urfave/cli"

	"github.com/babylonchain/covenant-emulator/clientcontroller"
	"github.com/babylonchain/covenant-emulator/covenant"
	covsrv "github.com/babylonchain/covenant-emulator/covenant/service"
)

var startCommand = cli.Command{
	Name:        "start",
	Usage:       "Start the Covenant Emulator Daemon",
	Description: "Start the Covenant Emulator Daemon. Note that the Covenant key pair should be created beforehand",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  passphraseFlag,
			Usage: "The pass phrase used to encrypt the keys",
			Value: defaultPassphrase,
		},
		cli.StringFlag{
			Name:  homeFlag,
			Usage: "The path to the covenant home directory",
			Value: covcfg.DefaultCovenantDir,
		},
	},
	Action: start,
}

func start(ctx *cli.Context) error {
	homePath, err := filepath.Abs(ctx.String(homeFlag))
	if err != nil {
		return err
	}
	homePath = util.CleanAndExpandPath(homePath)

	cfg, err := covcfg.LoadConfig(homePath)
	if err != nil {
		return fmt.Errorf("failed to load config at %s: %w", homePath, err)
	}

	logger, err := log.NewRootLoggerWithFile(covcfg.LogFile(homePath), cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("failed to load the logger: %w", err)
	}

	bbnClient, err := clientcontroller.NewBabylonController(cfg.BabylonConfig, &cfg.BTCNetParams, logger)
	if err != nil {
		return fmt.Errorf("failed to create rpc client for the consumer chain: %w", err)
	}

	ce, err := covenant.NewCovenantEmulator(cfg, bbnClient, ctx.String(passphraseFlag), logger)
	if err != nil {
		return fmt.Errorf("failed to start the covenant emulator: %w", err)
	}

	// Hook interceptor for os signals.
	shutdownInterceptor, err := signal.Intercept()
	if err != nil {
		return err
	}

	srv := covsrv.NewCovenantServer(logger, ce, shutdownInterceptor)
	if err != nil {
		return fmt.Errorf("failed to create covenant server: %w", err)
	}

	return srv.RunUntilShutdown()
}

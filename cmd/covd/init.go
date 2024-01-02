package main

import (
	"fmt"
	"path/filepath"

	"github.com/jessevdk/go-flags"
	"github.com/urfave/cli"

	covcfg "github.com/babylonchain/covenant-emulator/config"
	"github.com/babylonchain/covenant-emulator/util"
)

var initCommand = cli.Command{
	Name:  "init",
	Usage: "Initialize a covenant home directory.",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  homeFlag,
			Usage: "Path to where the home directory will be initialized",
			Value: covcfg.DefaultCovenantDir,
		},
		cli.BoolFlag{
			Name:     forceFlag,
			Usage:    "Override existing configuration",
			Required: false,
		},
	},
	Action: initHome,
}

func initHome(c *cli.Context) error {
	homePath, err := filepath.Abs(c.String(homeFlag))
	if err != nil {
		return err
	}
	// ensure the directory exists
	homePath = util.CleanAndExpandPath(homePath)
	force := c.Bool(forceFlag)

	if util.FileExists(homePath) && !force {
		return fmt.Errorf("home path %s already exists", homePath)
	}

	if err := util.MakeDirectory(homePath); err != nil {
		return err
	}
	// Create log directory
	logDir := covcfg.LogDir(homePath)
	if err := util.MakeDirectory(logDir); err != nil {
		return err
	}

	defaultConfig := covcfg.DefaultConfigWithHomePath(homePath)
	fileParser := flags.NewParser(&defaultConfig, flags.Default)

	return flags.NewIniParser(fileParser).WriteFile(covcfg.ConfigFile(homePath), flags.IniIncludeComments|flags.IniIncludeDefaults)
}

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gregorybednov/lbc/blockchain"
	"github.com/gregorybednov/lbc/cfg"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init [genesis|join] [genesis-path]",
	Short: "Инициализация ноды: genesis или join",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		switch args[0] {
		case "genesis":
			config, viper := cfg.InitGenesis(chainName, defaultConfigPath)
			nodeinfo, err := blockchain.GetNodeInfo(config, dbPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v", err)
				panic(err)
			}

			cfg.UpdateGenesisJson(nodeinfo, viper, filepath.Dir(defaultConfigPath))
			fmt.Println("Genesis node initialized.")
		case "join":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "Укажите путь к genesis.json")
				os.Exit(1)
			}
			cfg.InitJoiner(chainName, defaultConfigPath, args[1])

			fmt.Println("Joiner node initialized.")
		default:
			fmt.Fprintf(os.Stderr, "Неизвестный режим init: %s\n", args[0])
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}

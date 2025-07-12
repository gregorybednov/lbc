package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init [genesis|join] [genesis-path]",
	Short: "Инициализация ноды: genesis или join",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		switch args[0] {
		case "genesis":
			initGenesis()
		case "join":
			if len(args) < 2 {
				fmt.Println("Укажите путь к genesis.json")
				os.Exit(1)
			}
			initJoiner(args[1])
		default:
			fmt.Printf("Неизвестный режим init: %s\n", args[0])
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}

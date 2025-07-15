package cli

import (
	"fmt"
	"lbc/cfg"
	"lbc/yggdrasil"

	"github.com/spf13/cobra"
)

var testYggdrasilCmd = &cobra.Command{
	Use:   "testYggdrasil",
	Short: "Тест подключения через Yggdrasil без Tendermint",
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := cfg.LoadViperConfig(defaultConfigPath)
		if err != nil {
			return fmt.Errorf("не удалось прочитать конфигурацию: %w", err)
		}
		if err := yggdrasil.TestConnectivity(v); err != nil {
			return fmt.Errorf("тест не пройден: %w", err)
		}
		fmt.Println("Yggdrasil connectivity test successful")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(testYggdrasilCmd)
}

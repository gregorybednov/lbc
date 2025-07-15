package cli

import (
	"context"
	"fmt"
	"lbc/blockchain"
	"lbc/cfg"
	"lbc/yggdrasil"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var defaultConfigPath string
var dbPath string
var chainName string

func init() {
	rootCmd.PersistentFlags().StringVar(&defaultConfigPath, "config", "./config/config.toml", "Путь к конфигурационному файлу")
	rootCmd.PersistentFlags().StringVar(&dbPath, "badger", "./badger", "Путь к базе данных BadgerDB")
	rootCmd.PersistentFlags().StringVar(&chainName, "chainname", "lbc-chain", "Название цепочки блоков")
}

var rootCmd = &cobra.Command{
	Use:   "lbc",
	Short: "Лёгкий блокчейн координатор",
	Run: func(cmd *cobra.Command, args []string) {
		//Run()
		v, err := cfg.LoadViperConfig(defaultConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, `Конфигурационный файл не найден: %v

	Похоже, что нода ещё не инициализирована.

	Чтобы создать необходимые файлы, запусти одну из следующих команд:

	  lbc --init=genesis    # если это новая цепочка
	  lbc --init=join       # если ты присоединяешься к существующей

	По умолчанию файл конфигурации ищется по пути: %s
	`, err, defaultConfigPath)
			os.Exit(1)
		}

		config, err := cfg.ReadConfig(defaultConfigPath)
		if err != nil {
			fmt.Printf("конфигурация не прочитана: %w", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		laddrReturner := make(chan string, 2)
		go yggdrasil.Yggdrasil(v, laddrReturner)
		go blockchain.Run(ctx, dbPath, config, laddrReturner)

		if err != nil {
			// TODO exitWithError("error creating node", err)
		}
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh  // ждём SIGINT/SIGTERM
		cancel() // говорим горутинам завершаться
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "ошибка: %v\n", err)
		os.Exit(1)
	}
}

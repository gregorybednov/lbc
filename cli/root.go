package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/gregorybednov/lbc/blockchain"
	"github.com/gregorybednov/lbc/cfg"
	"github.com/gregorybednov/lbc/yggdrasil"

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

	  lbc init genesis    # если это новая цепочка
	  lbc init join       # если присоединяешься к существующей

	По умолчанию файл конфигурации ищется по пути: %s
	`, err, defaultConfigPath)
			os.Exit(1)
		}

		config, err := cfg.ReadConfig(defaultConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "конфигурация не прочитана: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		laddrReturner := make(chan string, 3)
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

func ExecuteWithArgs(args []string, home string, stdout, stderr io.Writer) error {
	// Перенаправляем вывод Cobra
	if stdout != nil {
		rootCmd.SetOut(stdout)
	}
	if stderr != nil {
		rootCmd.SetErr(stderr)
	}

	// Если задан home, подставим дефолты, на которые завязаны флаги
	// (флаги привязаны к переменным через StringVar, поэтому смена переменных до Execute — норм.)
	origCfg := defaultConfigPath
	origDB := dbPath

	if home != "" {
		defaultConfigPath = filepath.Join(home, "config", "config.toml")
		// Примем convention: BADGER в (home)/data/badger, но если у тебя другое — поменяй строку ниже.
		dbPath = filepath.Join(home, "data", "badger")
	}

	// Важное: подаём именно те аргументы, которые хотел вызвать вызывающий код.
	rootCmd.SetArgs(args)

	// Выполняем и аккуратно восстанавливаем глобальные дефолты.
	err := rootCmd.Execute()

	defaultConfigPath = origCfg
	dbPath = origDB

	return err
}

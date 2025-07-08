package main

import (
	"flag"
	"fmt"

	cfg "lbc/configfunctions"
	"lbc/yggdrasil"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/dgraph-io/badger"
	"github.com/spf13/viper"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	nm "github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/proxy"
	tmTypes "github.com/tendermint/tendermint/types"
)

var (
	defaultConfigPath   = flag.String("config", "./config/config.toml", "Path to config file")
	dbPath              = flag.String("badger", "./badger", "Path to database")
	initMode            = flag.Bool("init", false, "Generate new config, keys and genesis")
	initType            = flag.String("mode", "genesis", "Init mode: genesis or join")
	writeConfigStubPath = flag.String("joiner-config", "", "Stub for join file")
)

func initFiles() {
	config := cfg.DefaultConfig()
	config.RootDir = filepath.Dir(filepath.Dir(*defaultConfigPath))

	switch *initType {
	case "genesis":
		cfg.WriteConfig(config, defaultConfigPath, false, p2p.DefaultNodeInfo{})
		if err := cfg.InitTendermintFiles(config); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to init files: %v\n", err)
			panic(err)
		}
		fmt.Println("Genesis node initialized.")
	case "join":
		cfg.WriteConfig(config, defaultConfigPath, false, p2p.DefaultNodeInfo{})

		if err := os.MkdirAll(filepath.Join(config.RootDir, "data"), 0700); err != nil {
			fmt.Fprint(os.Stderr, "не удалось создать директорию data")
			os.Exit(1)
		}

		if _, err := p2p.LoadOrGenNodeKey(config.NodeKeyFile()); err != nil {
			fmt.Fprint(os.Stderr, "ошибка генерации node_key.json")
			os.Exit(2)
		}

		pv := privval.GenFilePV(
			config.PrivValidatorKeyFile(),
			config.PrivValidatorStateFile(),
		)
		pv.Save()

		fmt.Println("Peer node initialized. Please ensure genesis.json is placed correctly.")
	default:
		fmt.Fprintf(os.Stderr, "Unknown mode: %s\n", *initType)
		return
	}
}

func main() {
	flag.Parse()

	if *initMode {
		initFiles()
		return
	}

	viper.SetConfigFile(*defaultConfigPath)
	if err := viper.ReadInConfig(); err != nil {
		fmt.Fprintf(os.Stderr, `Конфигурационный файл не найден: %v

	Похоже, что нода ещё не инициализирована.

	Чтобы создать необходимые файлы, запусти одну из следующих команд:

	  lbc --init --mode=genesis    # если это новая цепочка
	  lbc --init --mode=join       # если ты присоединяешься к существующей

	По умолчанию файл конфигурации ищется по пути: %s
	`, err, *defaultConfigPath)
		os.Exit(1)
	}

	db, err := badger.Open(badger.DefaultOptions(*dbPath).WithTruncate(true))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open badger db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	app := NewKVStoreApplication(db)

	ch := make(chan string, 2)

	go yggdrasil.Yggdrasil(viper.GetViper(), ch)

	node, err := newTendermint(app, *defaultConfigPath, ch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating node: %v\n", err)
		os.Exit(2)
	}

	if *writeConfigStubPath != "" {
		cfg.WriteConfig(node.Config(), writeConfigStubPath, true, node.NodeInfo())
		return
	}

	node.Start()
	defer func() { node.Stop(); node.Wait() }()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
}

func newTendermint(app abci.Application, configFile string, laddrReturner chan string) (*nm.Node, error) {
	config, err := cfg.ReadConfig(configFile)
	if err != nil {
		return nil, fmt.Errorf("конфигурация не прочитана")
	}
	config.P2P.ListenAddress = "tcp://" + <-laddrReturner
	config.P2P.PersistentPeers = <-laddrReturner

	var pv tmTypes.PrivValidator
	if _, err := os.Stat(config.PrivValidatorKeyFile()); err == nil {
		pv = privval.LoadFilePV(
			config.PrivValidatorKeyFile(),
			config.PrivValidatorStateFile(),
		)
	} else {
		fmt.Println("⚠️ priv_validator_key.json not found. Node will run as non-validator.")
		pv = tmTypes.NewMockPV()
	}

	nodeKey, err := p2p.LoadNodeKey(config.NodeKeyFile())
	if err != nil {
		return nil, fmt.Errorf("load node key: %w", err)
	}

	clientCreator := proxy.NewLocalClientCreator(app)
	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))

	return nm.NewNode(
		config,
		pv,
		nodeKey,
		clientCreator,
		nm.DefaultGenesisDocProviderFunc(config),
		nm.DefaultDBProvider,
		nm.DefaultMetricsProvider(config.Instrumentation),
		logger,
	)
}

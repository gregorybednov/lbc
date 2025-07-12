package cli

import (
	"fmt"
	"io"

	abciApp "lbc/abciapp"
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

var defaultConfigPath string
var dbPath string
var chainName string

func init() {
	rootCmd.PersistentFlags().StringVar(&defaultConfigPath, "config", "./config/config.toml", "Путь к конфигурационному файлу")
	rootCmd.PersistentFlags().StringVar(&dbPath, "badger", "./badger", "Путь к базе данных BadgerDB")
	rootCmd.PersistentFlags().StringVar(&chainName, "chainname", "lbc-chain", "Название цепочки блоков")
}

func newTendermint(app abci.Application, configFile string, v *viper.Viper) (*nm.Node, error) {
	config, err := cfg.ReadConfig(configFile)
	if err != nil {
		return nil, fmt.Errorf("конфигурация не прочитана: %w", err)
	}

	laddrReturner := make(chan string, 2)

	config.P2P.PersistentPeers = cfg.ReadP2Peers(configFile)
	//v.Set("p2p.persistent_peers", config.P2P.PersistentPeers)
	//v.Set("")

	go yggdrasil.Yggdrasil(v, laddrReturner)
	config.P2P.ListenAddress = "tcp://" + <-laddrReturner

	//if config.P2P.PersistentPeers == "" {
	config.P2P.PersistentPeers = <-laddrReturner
	//} else {
	//	<- laddrReturner
	//}

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

func exitWithError(msg string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", msg, err)
	os.Exit(1)
}

func loadViperConfig(path string) (*viper.Viper, error) {
	v := viper.New()
	v.SetConfigFile(path)
	err := v.ReadInConfig()
	return v, err
}

func openBadger(path string) (*badger.DB, error) {
	return badger.Open(badger.DefaultOptions(path).WithTruncate(true))
}

func buildNode() (*nm.Node, *badger.DB, error) {
	v, err := loadViperConfig(defaultConfigPath)
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

	db, err := openBadger(dbPath)
	if err != nil {
		exitWithError("failed to open badger db", err)
	}

	app := abciApp.NewKVStoreApplication(db)

	node, err := newTendermint(app, defaultConfigPath, v)
	if err != nil {
		exitWithError("error creating node", err)
	}
	return node, db, err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Sync()
		_ = out.Close()
	}()

	_, err = io.Copy(out, in)
	return err
}

func runNode() {
	node, db, err := buildNode()
	if err != nil {
		exitWithError("failed to build node", err)
	}
	defer db.Close()

	if err := node.Start(); err != nil {
		exitWithError("failed to start node", err)
	}
	defer func() {
		node.Stop()
		node.Wait()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
}

func initGenesis() {
	config := cfg.DefaultConfig()
	config.RootDir = filepath.Dir(filepath.Dir(defaultConfigPath))

	nodeinfo := p2p.DefaultNodeInfo{}
	viper := cfg.WriteConfig(config, &defaultConfigPath, nodeinfo)
	if err := cfg.InitTendermintFiles(config, true, chainName); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init files: %v\n", err)
		panic(err)
	}

	node, db, err := buildNode()
	if err != nil {
		panic(err)
	}
	db.Close()

	cfg.UpdateGenesisJson(node.NodeInfo(), viper, filepath.Dir(defaultConfigPath))
	fmt.Println("Genesis node initialized.")
}

func initJoiner(path string) {
	config := cfg.DefaultConfig()
	config.RootDir = filepath.Dir(filepath.Dir(defaultConfigPath))

	if err := copyFile(path, config.GenesisFile()); err != nil {
		fmt.Fprintln(os.Stderr, "не удалось скопировать genesis.json:", err)
		os.Exit(3)
	}

	nodeinfo := p2p.DefaultNodeInfo{}
	cfg.WriteConfig(config, &defaultConfigPath, nodeinfo)
	//viper := cfg.WriteConfig(config, &defaultConfigPath, nodeinfo)
	if err := cfg.InitTendermintFiles(config, false, chainName); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init files: %v\n", err)
		panic(err)
	}

	node, db, err := buildNode()
	if err != nil {
		panic(err)
	}
	defer db.Close()

	cfg.WriteConfig(config, &defaultConfigPath, node.NodeInfo())

	//if err := os.MkdirAll(filepath.Join(config.RootDir, "data"), 0o700); err != nil {
	//	fmt.Fprintln(os.Stderr, "не удалось создать директорию data")
	//	os.Exit(1)
	//}

	if _, err := p2p.LoadOrGenNodeKey(config.NodeKeyFile()); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка генерации node_key.json")
		os.Exit(2)
	}

	pv := privval.GenFilePV(
		config.PrivValidatorKeyFile(),
		config.PrivValidatorStateFile(),
	)
	pv.Save()

	fmt.Println("Joiner node initialized.")
}

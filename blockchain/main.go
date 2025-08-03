package blockchain

import (
	"context"
	"fmt"

	"lbc/cfg"
	"os"

	"github.com/dgraph-io/badger"
	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	nm "github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/proxy"
	tmTypes "github.com/tendermint/tendermint/types"
)

func openBadger(path string) (*badger.DB, error) {
	return badger.Open(badger.DefaultOptions(path).WithTruncate(true))
}

func newTendermint(app abci.Application, config *cfg.Config, laddrReturner chan string) (*nm.Node, error) {
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

func GetNodeInfo(config *cfg.Config, dbPath string) (p2p.NodeInfo, error) {
	db, err := openBadger(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db to get node info %v", err)
	}
	defer db.Close()

	app := NewKVStoreApplication(db)

	nodeKey, err := p2p.LoadNodeKey(config.NodeKeyFile())
	if err != nil {
		return nil, fmt.Errorf("load node key: %w", err)
	}

	clientCreator := proxy.NewLocalClientCreator(app)
	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))
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

	config.P2P.PersistentPeers = ""

	node, err := nm.NewNode(
		config,
		pv,
		nodeKey,
		clientCreator,
		nm.DefaultGenesisDocProviderFunc(config),
		nm.DefaultDBProvider,
		nm.DefaultMetricsProvider(config.Instrumentation),
		logger,
	)

	if err != nil {
		return nil, err
	}
	return node.NodeInfo(), nil

}

func Run(ctx context.Context, dbPath string, config *cfg.Config, laddrReturner chan string) error {
	db, err := openBadger(dbPath)
	if err != nil {
		return fmt.Errorf("open badger db: %w", err)
	}
	defer db.Close()

	app := NewKVStoreApplication(db)
	node, err := newTendermint(app, config, laddrReturner)
	if err != nil {
		return fmt.Errorf("build node: %w", err)
	}
	if err := node.Start(); err != nil {
		return fmt.Errorf("start node: %w", err)
	}
	defer func() {

	}()

	select {
	case <-ctx.Done():
	case <-node.Quit():
		return err
	}
	node.Stop()
	node.Wait()
	return nil
}

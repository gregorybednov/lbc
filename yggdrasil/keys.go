package yggdrasil

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/gologme/log"
	"github.com/spf13/viper"

	yggConfig "github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

func GeneratePrivateKey() yggConfig.KeyBytes {
	return yggConfig.GenerateConfig().PrivateKey
}

func GetPublicKey(keyPath string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return ed25519.PublicKey{}, err
	}

	decoded, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return ed25519.PublicKey{}, err
	}

	if len(decoded) != ed25519.PrivateKeySize {
		return ed25519.PublicKey{}, fmt.Errorf("invalid private key size: %d", len(decoded))
	}

	privateKey := ed25519.PrivateKey(decoded)
	return privateKey.Public().(ed25519.PublicKey), nil
}

func GetYggdrasilAddress(config *viper.Viper) string {
	//var remoteTcp types.TCPRemoteMappings
	ygg := config.Sub("yggdrasil")
	if ygg == nil {
		return ""
	}

	//laddr := config.Sub("p2p").GetString("laddr")
	//remoteTcp.Set(laddr)

	cfg := yggConfig.GenerateConfig()

	cfg.PrivateKeyPath = ygg.GetString("private_key_file")
	keyFile, err := os.ReadFile(cfg.PrivateKeyPath)
	if err != nil {
		panic(err)
	}
	keyHex := strings.TrimSpace(string(keyFile))
	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		panic(fmt.Errorf("failed to decode private key hex: %w", err))
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		panic(fmt.Errorf("invalid private key length: got %d, expected %d", len(keyBytes), ed25519.PrivateKeySize))
	}
	copy(cfg.PrivateKey[:], keyBytes)

	// Заполняем Certificate из PrivateKey
	err = cfg.GenerateSelfSignedCertificate()
	if err != nil {
		panic(fmt.Errorf("failed to generate certificate from private key: %w", err))
	}

	logger := log.Default()

	n := &node{}

	// Setup the Yggdrasil node itself.
	{
		options := []core.SetupOption{
			core.NodeInfo(cfg.NodeInfo),
			core.NodeInfoPrivacy(cfg.NodeInfoPrivacy),
		}
		for _, addr := range cfg.Listen {
			options = append(options, core.ListenAddress(addr))
		}
		for _, peer := range cfg.Peers {
			options = append(options, core.Peer{URI: peer})
		}
		for intf, peers := range cfg.InterfacePeers {
			for _, peer := range peers {
				options = append(options, core.Peer{URI: peer, SourceInterface: intf})
			}
		}
		for _, allowed := range cfg.AllowedPublicKeys {
			k, err := hex.DecodeString(allowed)
			if err != nil {
				panic(err)
			}
			options = append(options, core.AllowedPublicKey(k[:]))
		}

		var err error
		if n.core, err = core.New(cfg.Certificate, logger, options...); err != nil {
			panic(err)
		}

		address := n.core.Address()
		n.core.Stop()
		return address.String()
	}

}

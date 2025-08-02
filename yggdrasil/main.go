package yggdrasil

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"github.com/gologme/log"
	"github.com/spf13/viper"

	"github.com/yggdrasil-network/yggdrasil-go/src/admin"
	yggConfig "github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/multicast"
	"github.com/yggdrasil-network/yggstack/src/netstack"
	"github.com/yggdrasil-network/yggstack/src/types"
)

type node struct {
	core       *core.Core
	multicast  *multicast.Multicast
	admin      *admin.AdminSocket
	socks5Tcp  net.Listener
	socks5Unix net.Listener
}

// The main function is responsible for configuring and starting Yggdrasil.
func Yggdrasil(config *viper.Viper, ch chan string) {
	var remoteTcp types.TCPRemoteMappings
	socks := ""

	ygg := config.Sub("yggdrasil")
	if ygg == nil {
		log.Errorln("No [yggdrasil] config found")
		return
	}
	p2p := config.Sub("p2p")
	if p2p == nil {
		log.Errorln("No [p2p] config found")
		return
	}

	// Create a new logger that logs output to stdout.
	var logger *log.Logger
	if logger == nil {
		logger = log.New(os.Stdout, "", log.Flags())
	}

	laddr := p2p.GetString("laddr")
	remoteTcp.Set(laddr)
	ch <- remoteTcp[0].Mapped.String()

	peers := p2p.GetString("persistent_peers")
	parsed, err := ParseEntries(peers)
	if err != nil {
		parsed = []ParsedEntry{}
		ch <- ""
		log.Warnln("Warning: persistent peers has an error")
	}

	cfg := yggConfig.GenerateConfig()

	// Чтение ключа из файла
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

	cfg.AllowedPublicKeys = ygg.GetStringSlice("allowed_public_keys")
	cfg.AdminListen = ygg.GetString("admin_listen")
	cfg.Listen = ygg.GetStringSlice("listen")
	if ygg.GetString("peers") == "auto" {
		publicPeers := RandomPick(GetClosestPeers(getPublicPeers(), 20), 3)
		var urls []string
		for _, u := range publicPeers {
			urls = append(urls, u.String())
		}
		cfg.Peers = urls
	} else {
		cfg.Peers = ygg.GetStringSlice("peers")
	}

	logger.Printf("Yggdrasil peers: %s", cfg.Peers)

	// Catch interrupts from the operating system to exit gracefully.
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

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
		address, subnet := n.core.Address(), n.core.Subnet()
		publicstr := hex.EncodeToString(n.core.PublicKey())
		logger.Printf("Your public key is %s", publicstr)
		logger.Printf("Your IPv6 address is %s", address.String())
		logger.Printf("Your IPv6 subnet is %s", subnet.String())
		logger.Printf("Your Yggstack resolver name is %s%s", publicstr, types.NameMappingSuffix)
	}

	// Setup the admin socket.
	{
		options := []admin.SetupOption{
			admin.ListenAddress(cfg.AdminListen),
		}
		if cfg.LogLookups {
			options = append(options, admin.LogLookups{})
		}
		var err error
		if n.admin, err = admin.New(n.core, logger, options...); err != nil {
			panic(err)
		}
		if n.admin != nil {
			n.admin.SetupAdminHandlers()
		}
	}

	// Setup the multicast module.
	{
		options := []multicast.SetupOption{}
		for _, intf := range cfg.MulticastInterfaces {
			options = append(options, multicast.MulticastInterface{
				Regex:    regexp.MustCompile(intf.Regex),
				Beacon:   intf.Beacon,
				Listen:   intf.Listen,
				Port:     intf.Port,
				Priority: uint8(intf.Priority),
				Password: intf.Password,
			})
		}
		var err error
		if n.multicast, err = multicast.New(n.core, logger, options...); err != nil {
			panic(err)
		}
		if n.admin != nil && n.multicast != nil {
			n.multicast.SetupAdminHandlers(n.admin)
		}
	}

	// Setup Yggdrasil netstack
	s, err := netstack.CreateYggdrasilNetstack(n.core)
	if err != nil {
		panic(err)
	}

	{
		counter := 0
		for _, p := range parsed {
			if p.Proto == "ygg" {
				counter++
			}
		}

		peerChan := make(chan string, counter)
		var wg sync.WaitGroup

		type mappingRecord struct {
			mapping types.TCPMapping
			id      string
			//	ch      chan string
		}

		addrs := make([]mappingRecord, counter)
		counter = 0

		for _, p := range parsed {
			if p.Proto != "ygg" {
				continue
			}

			cleanAddr := strings.Trim(p.Address, "[]")

			addrs[counter].id = p.ID
			addrs[counter].mapping.Mapped = &net.TCPAddr{
				IP:   net.ParseIP(cleanAddr),
				Port: *p.Port,
			}
			counter++
		}

		for _, record := range addrs {
			wg.Add(1)
			go func(q mappingRecord) {
				defer wg.Done()

				laddr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
				listener, err := net.ListenTCP("tcp", laddr)
				if err != nil {
					logger.Errorf("Failed to bind dynamic port for peer %s: %v", q.id, err)
					return
				}
				realPort := listener.Addr().(*net.TCPAddr).Port
				peerChan <- fmt.Sprintf("%s@127.0.0.1:%d", q.id, realPort)

				logger.Printf("Mapping local TCP port %d to Yggdrasil %s", realPort, q.mapping.Mapped)
				go func() {
					for {
						fmt.Println("Accepting...")
						c, err := listener.Accept()
						fmt.Println("Accepted!")
						if err != nil {
							panic(err)
						}
						fmt.Println("Dialing...")
						r, err := s.DialTCP(q.mapping.Mapped)
						fmt.Println("Dialed! (or not)")
						if err != nil {
							fmt.Println("Not dialed due ", err)
							logger.Errorf("Failed to connect to %s: %s", q.mapping.Mapped, err)
							_ = c.Close()
							continue
						}
						go types.ProxyTCP(n.core.MTU(), c, r)
					}
				}()
			}(record)
		}

		go func() {
			wg.Wait()
			close(peerChan)
		}()

		var peersList []string
		for peer := range peerChan {
			peersList = append(peersList, peer)
		}
		ch <- strings.Join(peersList, ",")
	}

	// Create remote TCP mappings (forwarding connections from Yggdrasil
	// node to local port)
	{

		for _, mapping := range remoteTcp {

			go func(mapping types.TCPMapping) {

				listener, err := s.ListenTCP(mapping.Listen)
				if err != nil {
					panic(err)
				}
				logger.Printf("Mapping Yggdrasil TCP port %s %d to %s", mapping.Listen.String(), mapping.Listen.Port, mapping.Mapped)
				for {
					c, err := listener.Accept()
					if err != nil {
						panic(err)
					}
					r, err := net.DialTCP("tcp", nil, mapping.Mapped)
					if err != nil {
						logger.Errorf("Failed to connect to %s: %s", mapping.Mapped, err)
						_ = c.Close()
						continue
					}
					go types.ProxyTCP(n.core.MTU(), c, r)
				}
			}(mapping)
		}
	}

	// Block until we are told to shut down.
	<-ctx.Done()

	// Shut down the node.
	_ = n.admin.Stop()
	_ = n.multicast.Stop()
	if n.socks5Unix != nil {
		_ = n.socks5Unix.Close()
		_ = os.RemoveAll(socks)
		logger.Infof("Stopped SOCKS5 UNIX socket listener")
	}
	if n.socks5Tcp != nil {
		_ = n.socks5Tcp.Close()
		logger.Infof("Stopped SOCKS5 TCP listener")
	}
	n.core.Stop()
}

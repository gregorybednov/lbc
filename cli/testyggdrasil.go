package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gregorybednov/lbc/cfg"
	"github.com/gregorybednov/lbc/yggdrasil"

	"github.com/spf13/cobra"
)

var testYggdrasilCmd = &cobra.Command{
	Use:   "testYggdrasil",
	Short: "Тест подключения через Yggdrasil без Tendermint",
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := cfg.LoadViperConfig(defaultConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "не удалось прочитать конфигурацию viper: %v", err)
			os.Exit(1)
		}
		config, err := cfg.ReadConfig(defaultConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "конфигурация не прочитана: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		laddrReturner := make(chan string, 2)
		go yggdrasil.Yggdrasil(v, laddrReturner)
		go notblockchain(ctx, dbPath, config, laddrReturner)

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh  // ждём SIGINT/SIGTERM
		cancel() // говорим горутинам завершаться
		return nil
	},
}

func notblockchain(ctx context.Context, dbPath string, config *cfg.Config, laddrReturner chan string) error {
	laddr := "tcp://" + <-laddrReturner
	p2peers := <-laddrReturner

	u, err := url.Parse(laddr)
	if err != nil {
		log.Fatalf("bad laddr %q: %v", laddr, err)
	}
	proto := u.Scheme    // "tcp"
	listenAddr := u.Host // "127.0.0.1:8000"
	ln, err := net.Listen(proto, listenAddr)
	if err != nil {
		log.Fatalf("listen %s failed: %v", listenAddr, err)
	}
	log.Printf("listening on %s://%s", proto, listenAddr)

	// --- парсим пиров ---
	var peerAddrs []string
	for _, entry := range strings.Split(p2peers, ",") {
		parts := strings.SplitN(entry, "@", 2)
		if len(parts) != 2 {
			log.Printf("skip invalid peer entry: %q", entry)
			continue
		}
		peerAddrs = append(peerAddrs, parts[1])
	}

	// --- стартовое сообщение от не-genesis ---
	if len(peerAddrs) > 0 {
		initial := []byte("HELLO_FROM_JOINER\n")
		for _, pa := range peerAddrs {
			go sendToPeer(proto, pa, initial)
		}
	}

	// --- главный цикл приёма+форвард---
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			data, err := io.ReadAll(c)
			if err != nil {
				log.Printf("read error: %v", err)
				return
			}

			log.Printf("received %d bytes", len(data))

			// если нет пиров, просто возвращаемся
			if len(peerAddrs) == 0 {
				log.Printf("  → no peers configured, dropping/ignoring message")
				return
			}

			log.Printf("  → waiting 10s before forwarding to %d peers", len(peerAddrs))
			time.Sleep(10 * time.Second)

			for _, pa := range peerAddrs {
				go sendToPeer(proto, pa, data)
			}
		}(conn)
	}
}

// sendToPeer коннектится к одному пиру и шлёт данные.
func sendToPeer(proto, addr string, data []byte) {
	conn, err := net.Dial(proto, addr)
	if err != nil {
		log.Printf("dial %s://%s error: %v", proto, addr, err)
		return
	}
	defer conn.Close()

	if _, err := conn.Write(data); err != nil {
		log.Printf("write to %s error: %v", addr, err)
	} else {
		log.Printf("forwarded %d bytes to %s", len(data), addr)
	}
}

func init() {
	rootCmd.AddCommand(testYggdrasilCmd)
}

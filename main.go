package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"oci-bot/bot"
	"oci-bot/config"
)

func main() {
	confFile := flag.String("c", "conf", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(*confFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	log.Printf("=== OCI Reserved IP Bot ===")
	log.Printf("Accounts: %v", cfg.AccountNames())
	log.Printf("Admin ID: %d", cfg.TelegramAdminID)

	tgBot, err := bot.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("\nStopping...")
		cancel()
	}()

	if err := tgBot.Run(ctx); err != nil {
		log.Fatalf("Bot error: %v", err)
	}
}

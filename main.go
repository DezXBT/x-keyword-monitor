package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	if err := validateConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "config invalid: %v\n", err)
		os.Exit(1)
	}

	initLogger(cfg.Logging.Level, cfg.Timezone())
	logInfo("x-keyword-monitor starting")

	dataDir := DataDir(*configPath)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		logError("create data dir: %v", err)
		os.Exit(1)
	}

	keywords, err := NewKeywordStore(dataDir)
	if err != nil {
		logError("keyword store: %v", err)
		os.Exit(1)
	}
	logInfo("keywords loaded: %d", keywords.Count())

	statePath := filepath.Join(dataDir, "state.json")
	state := NewSeenState(statePath)

	xClients := make([]*XClient, len(cfg.Twitter.Cookies))
	for i, c := range cfg.Twitter.Cookies {
		xClients[i] = NewXClient(c)
		label := c.Label
		if label == "" {
			label = fmt.Sprintf("cookie-%d", i+1)
		}
		logInfo("x client: %s", label)
	}

	if err := Init(); err != nil {
		logWarn("transaction ID init failed: %v (continuing without)", err)
	}

	bot, err := NewDiscordBot(cfg, keywords)
	if err != nil {
		logError("discord bot: %v", err)
		os.Exit(1)
	}
	bot.RegisterHandlers()
	if err := bot.Open(); err != nil {
		logError("discord open: %v", err)
		os.Exit(1)
	}
	logInfo("discord connected")

	guildID := cfg.Discord.GuildID
	if err := bot.RegisterCommands(guildID); err != nil {
		logWarn("register commands: %v", err)
	}
	logInfo("slash commands registered (guild: %s)", guildID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poller := NewPoller(cfg, keywords, state, xClients, bot)
	poller.RunResyncCheck()
	go poller.Run(ctx)
	go poller.HealthCheck(ctx)

	logInfo("x-keyword-monitor online — %d keywords, poll interval %s",
		keywords.Count(), cfg.Search.PollInterval)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logInfo("received %s, shutting down...", sig)
	cancel()
	state.Save()
	bot.Close()
	logInfo("x-keyword-monitor exited")
}

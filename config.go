package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type CookiePair struct {
	AuthToken string `yaml:"auth_token"`
	Ct0       string `yaml:"ct0"`
	Label     string `yaml:"label,omitempty"`
}

type TwitterConfig struct {
	Cookies             []CookiePair `yaml:"cookies"`
	HealthCheckInterval string       `yaml:"health_check_interval,omitempty"`
}

type SearchConfig struct {
	// PollInterval is the full cycle interval — how often the whole keyword set
	// is scanned once. Default 20s. Going below ~15s risks rate limits.
	PollInterval string `yaml:"poll_interval"`
	// TweetsPerQuery caps how many results to pull per keyword per cycle.
	TweetsPerQuery int `yaml:"tweets_per_query"`
	// PerKeywordDelay is the pause between consecutive keyword queries in a cycle,
	// to spread load across cookies and avoid bursting. Default 400ms.
	PerKeywordDelay string `yaml:"per_keyword_delay,omitempty"`
}

type DiscordConfig struct {
	BotToken       string `yaml:"bot_token"`
	GuildID        string `yaml:"guild_id,omitempty"`
	DefaultChannel string `yaml:"default_channel,omitempty"`
}

type LogConfig struct {
	Level    string `yaml:"level"`
	Timezone string `yaml:"timezone"`
}

type Config struct {
	Discord DiscordConfig `yaml:"discord"`
	Twitter TwitterConfig `yaml:"twitter"`
	Search  SearchConfig  `yaml:"search"`
	Logging LogConfig     `yaml:"logging"`
}

func (c *Config) PollIntervalDuration() time.Duration {
	d, err := time.ParseDuration(c.Search.PollInterval)
	if err != nil {
		return 20 * time.Second
	}
	return d
}

func (c *Config) PerKeywordDelayDuration() time.Duration {
	d, err := time.ParseDuration(c.Search.PerKeywordDelay)
	if err != nil {
		return 400 * time.Millisecond
	}
	return d
}

func (c *Config) Timezone() *time.Location {
	tz := c.Logging.Timezone
	if tz == "" {
		tz = "Asia/Jakarta"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.FixedZone("WIB", 7*3600)
	}
	return loc
}

func (c *Config) HealthCheckDuration() time.Duration {
	d, err := time.ParseDuration(c.Twitter.HealthCheckInterval)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

// CookieHash returns a sha256 hash of the first auth_token for change detection.
func (c *Config) CookieHash() string {
	if len(c.Twitter.Cookies) == 0 {
		return ""
	}
	h := sha256.Sum256([]byte(c.Twitter.Cookies[0].AuthToken))
	return fmt.Sprintf("%x", h[:8])
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Defaults
	if cfg.Search.PollInterval == "" {
		cfg.Search.PollInterval = "20s"
	}
	if cfg.Search.TweetsPerQuery == 0 {
		cfg.Search.TweetsPerQuery = 20
	}
	if cfg.Search.PerKeywordDelay == "" {
		cfg.Search.PerKeywordDelay = "400ms"
	}
	if cfg.Logging.Timezone == "" {
		cfg.Logging.Timezone = "Asia/Jakarta"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Twitter.HealthCheckInterval == "" {
		cfg.Twitter.HealthCheckInterval = "5m"
	}

	return cfg, nil
}

func validateConfig(cfg *Config) error {
	if cfg.Discord.BotToken == "" {
		return fmt.Errorf("discord.bot_token required")
	}
	if len(cfg.Twitter.Cookies) == 0 {
		return fmt.Errorf("twitter.cookies required (at least one)")
	}
	for i, c := range cfg.Twitter.Cookies {
		if c.AuthToken == "" || c.Ct0 == "" {
			return fmt.Errorf("cookie pair %d: auth_token and ct0 required", i+1)
		}
	}
	return nil
}

// DataDir returns the directory for storing state/keyword files, next to the config.
func DataDir(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "data")
}

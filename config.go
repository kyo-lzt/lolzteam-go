package lolzteam

import "time"

// Config holds settings for creating Forum/Market clients.
type Config struct {
	Token             string
	BaseURL           string
	ProxyURL          string        // optional, e.g. "http://proxy:8080" or "socks5://proxy:1080"
	MaxRetries        int           // default: 3
	RetryBaseDelay    time.Duration // default: 1s
	RetryMaxDelay     time.Duration // default: 30s
	RequestsPerMinute int           // default: per-client (Forum=300, Market=120)
	Timeout           time.Duration // default: 30s
}

func (c Config) withDefaults() Config {
	if c.MaxRetries <= 0 {
		c.MaxRetries = 3
	}
	if c.RetryBaseDelay <= 0 {
		c.RetryBaseDelay = time.Second
	}
	if c.RetryMaxDelay <= 0 {
		c.RetryMaxDelay = 30 * time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	return c
}

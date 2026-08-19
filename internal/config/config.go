// Package config resolves runtime settings from environment and flags.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	Addr          string        // listen address, behind Caddy
	DBPath        string        // SQLite file
	RPID          string        // WebAuthn relying party ID, e.g. dash.marcusson.dev
	RPOrigins     []string      // acceptable origins
	RPDisplayName string        // shown by the authenticator
	Username      string        // the single account
	SessionTTL    time.Duration //
	SecureCookies bool          // false only for plain-http local dev
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load builds a config from DASHBOARD_* environment variables, applying
// defaults that match the live deployment.
func Load() (Config, error) {
	origin := env("DASHBOARD_ORIGIN", "https://dash.marcusson.dev")

	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return Config{}, fmt.Errorf("DASHBOARD_ORIGIN %q is not a valid absolute URL", origin)
	}

	c := Config{
		Addr:          env("DASHBOARD_ADDR", "127.0.0.1:13000"),
		DBPath:        env("DASHBOARD_DB", "/var/lib/dashboard/dashboard.sqlite"),
		RPID:          env("DASHBOARD_RP_ID", u.Hostname()),
		RPOrigins:     []string{strings.TrimRight(origin, "/")},
		RPDisplayName: env("DASHBOARD_RP_NAME", "Server Dashboard"),
		Username:      env("DASHBOARD_USER", "oliver"),
		SessionTTL:    30 * 24 * time.Hour,
		SecureCookies: u.Scheme == "https",
	}

	if extra := os.Getenv("DASHBOARD_EXTRA_ORIGINS"); extra != "" {
		for _, o := range strings.Split(extra, ",") {
			if o = strings.TrimSpace(strings.TrimRight(o, "/")); o != "" {
				c.RPOrigins = append(c.RPOrigins, o)
			}
		}
	}
	if ttl := os.Getenv("DASHBOARD_SESSION_TTL"); ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil {
			return Config{}, fmt.Errorf("DASHBOARD_SESSION_TTL: %w", err)
		}
		c.SessionTTL = d
	}
	return c, nil
}

// This file is Free Software under the MIT License
// without warranty, see README.md and LICENSES/MIT.txt for details.
//
// SPDX-License-Identifier: MIT
//
// SPDX-FileCopyrightText: 2022 German Federal Office for Information Security (BSI) <https://www.bsi.bund.de>
// Software-Engineering: 2022 Intevation GmbH <https://intevation.de>

package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/csaf-poc/csaf_distribution/csaf"
	"golang.org/x/time/rate"
)

const (
	defaultConfigPath = "aggregator.toml"
	defaultWorkers    = 10
	defaultFolder     = "/var/www"
	defaultWeb        = "/var/www/html"
	defaultDomain     = "https://example.com"
)

type provider struct {
	Name     string   `toml:"name"`
	Domain   string   `toml:"domain"`
	Rate     *float64 `toml:"rate"`
	Insecure *bool    `toml:"insecure"`
}

type config struct {
	Workers    int                 `toml:"workers"`
	Folder     string              `toml:"folder"`
	Web        string              `toml:"web"`
	Domain     string              `toml:"domain"`
	Rate       *float64            `toml:"rate"`
	Insecure   *bool               `toml:"insecure"`
	Aggregator csaf.AggregatorInfo `toml:"aggregator"`
	Providers  []*provider         `toml:"providers"`
	Key        string              `toml:"key"`
	Passphrase *string             `toml:"passphrase"`

	keyMu  sync.Mutex
	key    *crypto.Key
	keyErr error
}

func (c *config) cryptoKey() (*crypto.Key, error) {
	if c.Key == "" {
		return nil, nil
	}
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	if c.key != nil || c.keyErr != nil {
		return c.key, c.keyErr
	}
	var f *os.File
	if f, c.keyErr = os.Open(c.Key); c.keyErr != nil {
		return nil, c.keyErr
	}
	defer f.Close()
	c.key, c.keyErr = crypto.NewKeyFromArmoredReader(f)
	return c.key, c.keyErr
}

func (c *config) httpClient(p *provider) client {

	client := http.Client{}
	if p.Insecure != nil && *p.Insecure || c.Insecure != nil && *c.Insecure {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}
	if p.Rate == nil && c.Rate == nil {
		return &client
	}

	var r float64
	if c.Rate != nil {
		r = *c.Rate
	}
	if p.Rate != nil {
		r = *p.Rate
	}
	return &limitingClient{
		client:  &client,
		limiter: rate.NewLimiter(rate.Limit(r), 1),
	}
}

func (c *config) checkProviders() error {
	already := make(map[string]bool)

	for _, p := range c.Providers {
		if p.Name == "" {
			return errors.New("no name given for provider")
		}
		if p.Domain == "" {
			return errors.New("no domain given for provider")
		}
		if already[p.Name] {
			return fmt.Errorf("provider '%s' is configured more than once", p.Name)
		}
		already[p.Name] = true
	}
	return nil
}

func (c *config) setDefaults() {
	if c.Folder == "" {
		c.Folder = defaultFolder
	}

	if c.Web == "" {
		c.Web = defaultWeb
	}

	if c.Domain == "" {
		c.Domain = defaultDomain
	}

	if c.Workers <= 0 {
		if n := runtime.NumCPU(); n > defaultWorkers {
			c.Workers = defaultWorkers
		} else {
			c.Workers = n
		}
	}

	if c.Workers > len(c.Providers) {
		c.Workers = len(c.Providers)
	}
}

func (c *config) check() error {
	if len(c.Providers) == 0 {
		return errors.New("no providers given in configuration")
	}

	if err := c.Aggregator.Validate(); err != nil {
		return err
	}

	return c.checkProviders()
}

func loadConfig(path string) (*config, error) {
	if path == "" {
		path = defaultConfigPath
	}

	var cfg config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, err
	}

	cfg.setDefaults()

	if err := cfg.check(); err != nil {
		return nil, err
	}

	return &cfg, nil
}
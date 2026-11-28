// SPEC: _spec/internal/dashboard/dashboard-config.puml
package dashboard

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// dashboardConfigFile is the operator's own dashboard settings under the proveo home; proveo never writes it.
const dashboardConfigFile = "dashboard.yml"

// dashboardConfig is dashboard.yml.
type dashboardConfig struct {
	Links linkRules   `yaml:"links"`
	Tasks *taskConfig `yaml:"tasks"` // the new-instance picker's sources; nil keeps proveo's defaults
}

// linkRules decide which PR description links Task Details shows: a link a block rule matches is hidden unless an
// allow rule matches it too.
type linkRules struct {
	Block []linkRule `yaml:"block"`
	Allow []linkRule `yaml:"allow"`
}

// linkRule matches a link by host glob (* is any run of characters) and, when set, a path prefix.
type linkRule struct {
	Host string `yaml:"host"`
	Path string `yaml:"path"`
}

func (r linkRule) matches(u *url.URL) bool {
	ok, err := path.Match(strings.ToLower(r.Host), strings.ToLower(u.Hostname()))
	return err == nil && ok && strings.HasPrefix(u.EscapedPath(), r.Path)
}

// shows is whether rules let link through.
func (rules linkRules) shows(link string) bool {
	u, err := url.Parse(link)
	if err != nil {
		return true
	}
	for _, b := range rules.Block {
		if b.matches(u) {
			for _, a := range rules.Allow {
				if a.matches(u) {
					return true
				}
			}
			return false
		}
	}
	return true
}

// readDashboardConfig reads dashboard.yml strictly: an unknown key or a bad host glob is an error, a missing file is
// an empty config.
func readDashboardConfig(file string) (dashboardConfig, error) {
	var c dashboardConfig
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return dashboardConfig{}, err
	}
	if c.Tasks != nil {
		if err := c.Tasks.validate(); err != nil {
			return dashboardConfig{}, err
		}
	}
	for _, r := range append(append([]linkRule{}, c.Links.Block...), c.Links.Allow...) {
		if r.Host == "" {
			return dashboardConfig{}, errors.New("links: every rule needs a host")
		}
		if _, err := path.Match(r.Host, ""); err != nil {
			return dashboardConfig{}, fmt.Errorf("links: host %q: %w", r.Host, err)
		}
	}
	return c, nil
}

// configCache is dashboard.yml as last read, keyed by its modification time so an edit applies without a restart.
type configCache struct {
	at     time.Time
	config dashboardConfig
	err    string
}

// config is dashboard.yml, read again whenever the file changes; a broken file says why once and applies nothing.
func (m *dashModel) config() dashboardConfig {
	file := filepath.Join(m.Home, dashboardConfigFile)
	var at time.Time
	if st, err := os.Stat(file); err == nil {
		at = st.ModTime()
	}
	if m.configRead && at.Equal(m.configCache.at) {
		return m.configCache.config
	}
	c, err := readDashboardConfig(file)
	m.configRead, m.configCache = true, configCache{at: at, config: c}
	if err != nil {
		m.configCache.err = fmt.Sprintf("%s: %s; its settings are off until it parses", file, err.Error())
		m.fail(errors.New(m.configCache.err))
	}
	return c
}

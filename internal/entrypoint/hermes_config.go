// SPEC: _spec/_plans/isolated-browser-profiles.puml
package entrypoint

import (
	"errors"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

func DisableHermesBrowser(managed, saved string) error {
	config, err := readHermesConfig(managed)
	if err != nil {
		return err
	}
	persisted, err := readHermesConfig(saved)
	if err != nil {
		return err
	}
	agent, ok := config["agent"].(map[string]any)
	if !ok {
		if config["agent"] != nil {
			return errors.New("agent configuration must be a map")
		}
		agent = make(map[string]any)
		config["agent"] = agent
	}
	disabled, exists := agent["disabled_toolsets"]
	if !exists {
		if prev, ok := persisted["agent"].(map[string]any); ok {
			disabled = prev["disabled_toolsets"]
		}
	}
	var tools []string
	switch entries := disabled.(type) {
	case nil:
	case string:
		if entries != "" {
			tools = append(tools, entries)
		}
	case []any:
		for _, value := range entries {
			tool, ok := value.(string)
			if !ok {
				return errors.New("disabled toolsets must be strings")
			}
			if !slices.Contains(tools, tool) {
				tools = append(tools, tool)
			}
		}
	default:
		return errors.New("disabled toolsets must be a string or list")
	}
	if !slices.Contains(tools, "browser") {
		tools = append(tools, "browser")
	}
	agent["disabled_toolsets"] = tools
	body, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(managed, body, 0o600)
}

func readHermesConfig(path string) (map[string]any, error) {
	out := make(map[string]any)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = make(map[string]any)
	}
	return out, nil
}

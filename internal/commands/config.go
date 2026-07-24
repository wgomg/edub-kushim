package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/viper"
	"github.com/wgomg/edub-kushim/internal/config"
	"gopkg.in/yaml.v3"
)

func configHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)
	if fp.Help("Usage: kushim config [<key> [<value>]]\n\n" +
		"View and edit configuration values.\n\n" +
		"  kushim config                  Dump full config as YAML\n" +
		"  kushim config <key>            Get a value\n" +
		"  kushim config <key> <value>    Set a value\n" +
		"  kushim config --unset <key>    Remove a key (revert to default)\n" +
		"  kushim config --validate       Validate config and report\n" +
		"  kushim config --path           Print config file path\n\n" +
		"Keys use dot notation, e.g. server.port, consumer.ocr.languages") {
		return nil
	}

	var unsetKey string
	validate := false
	showPath := false

	if err := fp.String("--unset", &unsetKey); err != nil {
		return err
	}
	fp.Bool("--validate", &validate)
	fp.Bool("--path", &showPath)

	rest := fp.Rest()
	configDir := c.config.App.ConfigDir

	if showPath {
		fmt.Println(filepath.Join(configDir, "config.yaml"))
		return nil
	}

	if len(rest) > 0 && (validate || showPath) {
		return fmt.Errorf("unknown arguments: %v", rest)
	}

	if validate {
		return validateConfig(configDir)
	}

	if unsetKey != "" {
		if len(rest) > 0 {
			return fmt.Errorf("unknown arguments: %v", rest)
		}
		return unsetConfigValue(configDir, unsetKey)
	}

	switch len(rest) {
	case 0:
		return dumpAllConfig(configDir)
	case 1:
		return getConfigValue(configDir, rest[0])
	default:
		return setConfigValue(configDir, rest[0], strings.Join(rest[1:], " "))
	}
}

func validateConfig(configDir string) error {
	_, err := config.Load(configDir)
	if err != nil {
		return fmt.Errorf("config.yaml: %w", err)
	}
	fmt.Println("config.yaml is valid")
	return nil
}

func dumpAllConfig(configDir string) error {
	v := viper.New()
	v.SetConfigType("yaml")
	configPath := filepath.Join(configDir, "config.yaml")

	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config not found: %s", configPath)
	}

	v.SetConfigFile(configPath)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	keys := v.AllKeys()
	sort.Strings(keys)
	for _, key := range keys {
		val := v.Get(key)
		if val == nil {
			continue
		}
		switch vv := val.(type) {
		case string, bool, int, int64, float64:
			fmt.Printf("%s = %v\n", key, vv)
		case []any:
			parts := make([]string, len(vv))
			for i, item := range vv {
				parts[i] = fmt.Sprintf("%v", item)
			}
			fmt.Printf("%s = %s\n", key, strings.Join(parts, ", "))
		case []string:
			fmt.Printf("%s = %s\n", key, strings.Join(vv, ", "))
		}
	}

	return nil
}

func getConfigValue(configDir, key string) error {
	v := viper.New()
	v.SetConfigType("yaml")
	configPath := filepath.Join(configDir, "config.yaml")

	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config not found: %s", configPath)
	}

	v.SetConfigFile(configPath)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	val := v.Get(key)
	if val == nil {
		return fmt.Errorf("key not found: %s", key)
	}

	printValue(val)
	return nil
}

func setConfigValue(configDir, key, rawValue string) error {
	val := parseValue(rawValue)
	if err := atomicSetConfig(configDir, map[string]any{key: val}); err != nil {
		return err
	}
	fmt.Printf("%s = %v\n", key, val)
	return nil
}

func unsetConfigValue(configDir, key string) error {
	configPath := filepath.Join(configDir, "config.yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	if !deleteNestedKey(cfg, key) {
		return fmt.Errorf("key not found: %s", key)
	}

	newData, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "kushim-config-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpConfigPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(tmpConfigPath, newData, 0644); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}

	if _, err := config.Load(tmpDir); err != nil {
		return fmt.Errorf("config validation: %w", err)
	}

	if err := os.Rename(tmpConfigPath, configPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("unset %s\n", key)
	return nil
}

func deleteNestedKey(m map[string]any, key string) bool {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) == 1 {
		if _, ok := m[parts[0]]; ok {
			delete(m, parts[0])
			return true
		}
		return false
	}
	nested, ok := m[parts[0]].(map[string]any)
	if !ok {
		return false
	}
	return deleteNestedKey(nested, parts[1])
}

func atomicSetConfig(configDir string, body map[string]any) error {
	v := viper.New()
	v.SetConfigType("yaml")
	configPath := filepath.Join(configDir, "config.yaml")

	if _, err := os.Stat(configPath); err == nil {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read config: %w", err)
		}
	}

	for key, val := range body {
		v.Set(key, val)
	}

	tmpDir, err := os.MkdirTemp("", "kushim-config-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpConfigPath := filepath.Join(tmpDir, "config.yaml")
	if err := v.WriteConfigAs(tmpConfigPath); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}

	if _, err := config.Load(tmpDir); err != nil {
		return fmt.Errorf("config validation: %w", err)
	}

	if err := os.Rename(tmpConfigPath, configPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

func parseValue(raw string) any {
	switch strings.ToLower(raw) {
	case "true":
		return true
	case "false":
		return false
	}

	if i, err := strconv.Atoi(raw); err == nil {
		return i
	}

	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}

	if strings.Contains(raw, ",") {
		parts := strings.Split(raw, ",")
		result := make([]string, len(parts))
		for i, p := range parts {
			result[i] = strings.TrimSpace(p)
		}
		return result
	}

	return raw
}

func printValue(val any) {
	switch v := val.(type) {
	case string, bool, int, int64, float64:
		fmt.Println(v)
	case []any:
		data, _ := yaml.Marshal(v)
		fmt.Print(string(data))
	case []string:
		data, _ := yaml.Marshal(v)
		fmt.Print(string(data))
	case map[string]any:
		data, _ := yaml.Marshal(v)
		fmt.Print(string(data))
	default:
		fmt.Printf("%v\n", v)
	}
}

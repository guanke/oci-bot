package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// OCIAccount represents a single OCI account configuration
type OCIAccount struct {
	Name          string
	User          string
	Fingerprint   string
	Tenancy       string
	Region        string
	CompartmentID string
	KeyFile       string
}

// Config holds the application configuration
type Config struct {
	// Telegram Bot
	TelegramToken   string
	TelegramAdminID int64

	// IP Purity Check
	AutoCheckIP bool // Auto check IP purity after creation (default: false)

	// OCI Accounts (multiple)
	Accounts []OCIAccount
}

// Load loads configuration from conf file (INI-style format)
func Load(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	cfg := &Config{}
	var currentSection string
	var currentAccount *OCIAccount
	globalValues := make(map[string]string)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for section header [name]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			// Save previous account if exists
			if currentAccount != nil {
				cfg.Accounts = append(cfg.Accounts, *currentAccount)
			}
			currentSection = strings.TrimPrefix(strings.TrimSuffix(line, "]"), "[")
			currentAccount = &OCIAccount{Name: currentSection}
			continue
		}

		// Parse key=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if currentAccount != nil {
			// Inside a section - OCI account settings
			switch key {
			case "user":
				currentAccount.User = value
			case "fingerprint":
				currentAccount.Fingerprint = value
			case "tenancy":
				currentAccount.Tenancy = value
			case "region":
				currentAccount.Region = value
			case "compartment_id":
				currentAccount.CompartmentID = value
			case "key_file":
				currentAccount.KeyFile = expandHome(value)
			}
		} else {
			// Global settings (Telegram)
			globalValues[key] = value
		}
	}

	// Save last account if exists
	if currentAccount != nil {
		cfg.Accounts = append(cfg.Accounts, *currentAccount)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Telegram settings
	cfg.TelegramToken = globalValues["token"]
	if chatID := globalValues["chat_id"]; chatID != "" {
		cfg.TelegramAdminID, _ = strconv.ParseInt(chatID, 10, 64)
	}

	// IP Purity settings (default: false)
	if autoCheck := globalValues["auto_check_ip"]; autoCheck == "true" || autoCheck == "1" {
		cfg.AutoCheckIP = true
	}

	return cfg, nil
}

// Validate checks if required configuration is present
func (c *Config) Validate() error {
	if c.TelegramToken == "" {
		return fmt.Errorf("token is required")
	}
	if c.TelegramAdminID == 0 {
		return fmt.Errorf("chat_id is required")
	}
	if len(c.Accounts) == 0 {
		return fmt.Errorf("at least one OCI account section is required")
	}
	// Use index to modify the original slice element
	for i := range c.Accounts {
		// Default compartment_id to tenancy if not set
		if c.Accounts[i].CompartmentID == "" {
			c.Accounts[i].CompartmentID = c.Accounts[i].Tenancy
		}
		if err := c.Accounts[i].Validate(); err != nil {
			return fmt.Errorf("account [%s]: %w", c.Accounts[i].Name, err)
		}
	}
	return nil
}

// Validate checks if OCI account has all required fields
func (a *OCIAccount) Validate() error {
	if a.User == "" {
		return fmt.Errorf("user is required")
	}
	if a.Fingerprint == "" {
		return fmt.Errorf("fingerprint is required")
	}
	if a.Tenancy == "" {
		return fmt.Errorf("tenancy is required")
	}
	if a.Region == "" {
		return fmt.Errorf("region is required")
	}
	if a.KeyFile == "" {
		return fmt.Errorf("key_file is required")
	}
	// compartment_id can default to tenancy
	if a.CompartmentID == "" {
		a.CompartmentID = a.Tenancy
	}
	return nil
}

// GetAccount returns account by name, or first account if name is empty
func (c *Config) GetAccount(name string) *OCIAccount {
	if name == "" && len(c.Accounts) > 0 {
		return &c.Accounts[0]
	}
	for i := range c.Accounts {
		if c.Accounts[i].Name == name {
			return &c.Accounts[i]
		}
	}
	return nil
}

// AccountNames returns list of all account names
func (c *Config) AccountNames() []string {
	names := make([]string, len(c.Accounts))
	for i, acc := range c.Accounts {
		names[i] = acc.Name
	}
	return names
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return strings.Replace(path, "~", home, 1)
	}
	return path
}

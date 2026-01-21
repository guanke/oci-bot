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
	// VPS settings
	VPSAvailabilityDomain string
	VPSSubnetID           string
	VPSImageArm           string
	VPSImageAmd           string
	VPSShapeArm           string
	VPSShapeAmd           string
	VPSOCPUsArm           float32
	VPSMemoryGBArm        float32
	VPSOCPUsAmd           float32
	VPSMemoryGBAmd        float32
	VPSSSHKeys            string
	VPSBootVolumeGB       int
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
			case "vps_ad":
				currentAccount.VPSAvailabilityDomain = value
			case "vps_subnet_id":
				currentAccount.VPSSubnetID = value
			case "vps_image_arm":
				currentAccount.VPSImageArm = value
			case "vps_image_amd":
				currentAccount.VPSImageAmd = value
			case "vps_shape_arm":
				currentAccount.VPSShapeArm = value
			case "vps_shape_amd":
				currentAccount.VPSShapeAmd = value
			case "vps_ocpus_arm":
				currentAccount.VPSOCPUsArm = parseFloat32(value)
			case "vps_memory_gb_arm":
				currentAccount.VPSMemoryGBArm = parseFloat32(value)
			case "vps_ocpus_amd":
				currentAccount.VPSOCPUsAmd = parseFloat32(value)
			case "vps_memory_gb_amd":
				currentAccount.VPSMemoryGBAmd = parseFloat32(value)
			case "vps_ssh_keys":
				currentAccount.VPSSSHKeys = value
			case "vps_boot_volume_gb":
				currentAccount.VPSBootVolumeGB = parseInt(value)
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

// ValidateVPSConfig checks if VPS config is valid for the given architecture.
func (a *OCIAccount) ValidateVPSConfig(arch string) error {
	if a.VPSAvailabilityDomain == "" {
		return fmt.Errorf("vps_ad is required")
	}
	if a.VPSSubnetID == "" {
		return fmt.Errorf("vps_subnet_id is required")
	}
	if a.VPSSSHKeys == "" {
		return fmt.Errorf("vps_ssh_keys is required")
	}

	switch arch {
	case "arm":
		if a.VPSImageArm == "" {
			return fmt.Errorf("vps_image_arm is required")
		}
		if a.VPSShapeArm == "" {
			return fmt.Errorf("vps_shape_arm is required")
		}
	case "amd":
		if a.VPSImageAmd == "" {
			return fmt.Errorf("vps_image_amd is required")
		}
		if a.VPSShapeAmd == "" {
			return fmt.Errorf("vps_shape_amd is required")
		}
	default:
		return fmt.Errorf("unsupported arch: %s", arch)
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

func parseFloat32(value string) float32 {
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 32)
	if err != nil {
		return 0
	}
	return float32(parsed)
}

func parseInt(value string) int {
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

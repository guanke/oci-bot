package oci

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"oci-bot/config"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

// Client wraps the OCI VirtualNetwork client
type Client struct {
	vnClient      core.VirtualNetworkClient
	compartmentID string
	region        string
	accountName   string
}

// PublicIPInfo contains information about a reserved public IP
type PublicIPInfo struct {
	ID          string
	IPAddress   string
	DisplayName string
	Lifetime    string
	State       string
}

// NewClient creates a new OCI client from account config
func NewClient(acc *config.OCIAccount) (*Client, error) {
	// Debug logging
	log.Printf("Creating OCI client for [%s]", acc.Name)
	log.Printf("  Tenancy: %s", acc.Tenancy)
	log.Printf("  User: %s", acc.User)
	log.Printf("  Region: %s", acc.Region)
	log.Printf("  Fingerprint: %s", acc.Fingerprint)
	log.Printf("  KeyFile: %s", acc.KeyFile)

	// Check if key file exists
	if _, err := os.Stat(acc.KeyFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("key file does not exist: %s", acc.KeyFile)
	}

	// Read private key file content
	keyContent, err := os.ReadFile(acc.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file %s: %w", acc.KeyFile, err)
	}
	log.Printf("  Key file read OK (%d bytes)", len(keyContent))

	configProvider := common.NewRawConfigurationProvider(
		acc.Tenancy,
		acc.User,
		acc.Region,
		acc.Fingerprint,
		string(keyContent),
		nil,
	)

	vnClient, err := core.NewVirtualNetworkClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to create VirtualNetwork client: %w", err)
	}

	vnClient.SetRegion(acc.Region)

	return &Client{
		vnClient:      vnClient,
		compartmentID: acc.CompartmentID,
		region:        acc.Region,
		accountName:   acc.Name,
	}, nil
}

// AccountName returns the account name
func (c *Client) AccountName() string {
	return c.accountName
}

// Region returns the region
func (c *Client) Region() string {
	return c.region
}

// CreateReservedIP creates a new reserved public IP
func (c *Client) CreateReservedIP(ctx context.Context, displayName string) (*PublicIPInfo, error) {
	request := core.CreatePublicIpRequest{
		CreatePublicIpDetails: core.CreatePublicIpDetails{
			CompartmentId: common.String(c.compartmentID),
			Lifetime:      core.CreatePublicIpDetailsLifetimeReserved,
			DisplayName:   common.String(displayName),
		},
	}

	response, err := c.vnClient.CreatePublicIp(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to create reserved IP: %w", err)
	}

	return &PublicIPInfo{
		ID:          *response.PublicIp.Id,
		IPAddress:   *response.PublicIp.IpAddress,
		DisplayName: safeString(response.PublicIp.DisplayName),
		Lifetime:    string(response.PublicIp.Lifetime),
		State:       string(response.PublicIp.LifecycleState),
	}, nil
}

// DeleteReservedIP deletes a reserved public IP by its OCID
func (c *Client) DeleteReservedIP(ctx context.Context, publicIPID string) error {
	request := core.DeletePublicIpRequest{
		PublicIpId: common.String(publicIPID),
	}

	_, err := c.vnClient.DeletePublicIp(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to delete reserved IP: %w", err)
	}

	return nil
}

// WaitForIPReady waits for the public IP to be in AVAILABLE state
func (c *Client) WaitForIPReady(ctx context.Context, publicIPID string, timeout time.Duration) (*PublicIPInfo, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		request := core.GetPublicIpRequest{
			PublicIpId: common.String(publicIPID),
		}

		response, err := c.vnClient.GetPublicIp(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("failed to get public IP status: %w", err)
		}

		if response.PublicIp.LifecycleState == core.PublicIpLifecycleStateAvailable {
			return &PublicIPInfo{
				ID:          *response.PublicIp.Id,
				IPAddress:   *response.PublicIp.IpAddress,
				DisplayName: safeString(response.PublicIp.DisplayName),
				Lifetime:    string(response.PublicIp.Lifetime),
				State:       string(response.PublicIp.LifecycleState),
			}, nil
		}

		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("timeout waiting for public IP to become available")
}

// ListReservedIPs lists all reserved public IPs in the compartment
func (c *Client) ListReservedIPs(ctx context.Context) ([]PublicIPInfo, error) {
	request := core.ListPublicIpsRequest{
		CompartmentId: common.String(c.compartmentID),
		Scope:         core.ListPublicIpsScopeRegion,
		Lifetime:      core.ListPublicIpsLifetimeReserved,
	}

	response, err := c.vnClient.ListPublicIps(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to list reserved IPs: %w", err)
	}

	var ips []PublicIPInfo
	for _, ip := range response.Items {
		ips = append(ips, PublicIPInfo{
			ID:          *ip.Id,
			IPAddress:   safeString(ip.IpAddress),
			DisplayName: safeString(ip.DisplayName),
			Lifetime:    string(ip.Lifetime),
			State:       string(ip.LifecycleState),
		})
	}

	return ips, nil
}

func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

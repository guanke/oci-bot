package oci

import (
	"context"
	"fmt"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/core"
)

// VPSLaunchDetails stores launch parameters for a VPS instance.
type VPSLaunchDetails struct {
	AvailabilityDomain string
	SubnetID           string
	ImageID            string
	Shape              string
	DisplayName        string
	SSHAuthorizedKeys  string
	OCPUs              float32
	MemoryGB           float32
	BootVolumeGB       int
}

// LaunchInstance launches a compute instance based on given details.
func (c *Client) LaunchInstance(ctx context.Context, details VPSLaunchDetails) (*core.Instance, error) {
	launchDetails := core.LaunchInstanceDetails{
		CompartmentId:      common.String(c.compartmentID),
		AvailabilityDomain: common.String(details.AvailabilityDomain),
		Shape:              common.String(details.Shape),
		DisplayName:        common.String(details.DisplayName),
		CreateVnicDetails: &core.CreateVnicDetails{
			SubnetId:       common.String(details.SubnetID),
			AssignPublicIp: common.Bool(true),
		},
	}

	sourceDetails := core.InstanceSourceViaImageDetails{
		ImageId: common.String(details.ImageID),
	}
	if details.BootVolumeGB > 0 {
		sourceDetails.BootVolumeSizeInGBs = common.Int64(int64(details.BootVolumeGB))
	}
	launchDetails.SourceDetails = sourceDetails

	if details.SSHAuthorizedKeys != "" {
		launchDetails.Metadata = map[string]string{
			"ssh_authorized_keys": details.SSHAuthorizedKeys,
		}
	}

	if details.OCPUs > 0 || details.MemoryGB > 0 {
		shapeConfig := core.LaunchInstanceShapeConfigDetails{}
		if details.OCPUs > 0 {
			shapeConfig.Ocpus = common.Float32(details.OCPUs)
		}
		if details.MemoryGB > 0 {
			shapeConfig.MemoryInGBs = common.Float32(details.MemoryGB)
		}
		launchDetails.ShapeConfig = &shapeConfig
	}

	request := core.LaunchInstanceRequest{
		LaunchInstanceDetails: launchDetails,
	}

	response, err := c.computeClient.LaunchInstance(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to launch instance: %w", err)
	}

	return &response.Instance, nil
}

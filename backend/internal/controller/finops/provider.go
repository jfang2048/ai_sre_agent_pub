package finops

import (
	"context"
	"fmt"
)

// CloudProvider defines the interface for interacting with cloud vendors
type CloudProvider interface {
	// GetInstancePrice returns hourly USD cost for a given instance type and region
	GetInstancePrice(ctx context.Context, region, instanceType string) (float64, error)

	// GetSimilarInstances finds cheaper/smaller instances in the same family
	GetSimilarInstances(ctx context.Context, currentType string) ([]InstanceTypeInfo, error)
}

// InstanceTypeInfo standardizes instance specs across clouds
type InstanceTypeInfo struct {
	Type   string
	VCPU   int
	Memory float64 // GiB
	Price  float64 // Hourly
}

// AWSProvider implements CloudProvider for AWS using a curated static catalog.
//
// The static catalog keeps this module deterministic/offline and avoids network
// dependency on live pricing APIs in the controller hot path.
type AWSProvider struct {
}

func NewAWSProvider() *AWSProvider {
	return &AWSProvider{}
}

func (p *AWSProvider) GetInstancePrice(ctx context.Context, region, instanceType string) (float64, error) {
	// Region is accepted for interface compatibility; the static catalog below
	// represents baseline on-demand pricing for recommendation ranking.
	_ = region
	prices := map[string]float64{
		"t3.micro":   0.0104,
		"t3.small":   0.0208,
		"t3.medium":  0.0416,
		"m5.large":   0.096,
		"m5.xlarge":  0.192,
		"m5.2xlarge": 0.384,
		"c5.large":   0.085,
		"c5.xlarge":  0.170,
	}

	if price, ok := prices[instanceType]; ok {
		return price, nil
	}
	return 0, fmt.Errorf("price not found for %s", instanceType)
}

func (p *AWSProvider) GetSimilarInstances(ctx context.Context, currentType string) ([]InstanceTypeInfo, error) {
	_ = currentType
	// Return a deterministic candidate set for right-sizing suggestions.
	return []InstanceTypeInfo{
		{Type: "t3.micro", VCPU: 2, Memory: 1.0, Price: 0.0104},
		{Type: "t3.small", VCPU: 2, Memory: 2.0, Price: 0.0208},
		{Type: "m5.large", VCPU: 2, Memory: 8.0, Price: 0.096},
		{Type: "m5.xlarge", VCPU: 4, Memory: 16.0, Price: 0.192},
	}, nil
}

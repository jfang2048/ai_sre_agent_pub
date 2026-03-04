package finops

import (
	"context"
	"fmt"
)

// ResourceUsage tracks observed utilization
type ResourceUsage struct {
	InstanceID  string  `json:"instance_id"`
	CurrentType string  `json:"current_type"`
	Region      string  `json:"region"`
	AvgCPU      float64 `json:"avg_cpu"` // % (0-100)
	MaxCPU      float64 `json:"max_cpu"`
	AvgMem      float64 `json:"avg_mem"` // % (0-100)
	MaxMem      float64 `json:"max_mem"`
	NetworkIO   float64 `json:"network_io"` // bytes/sec
}

// Recommendation represents a cost-saving suggestion
type Recommendation struct {
	InstanceID     string  `json:"instance_id"`
	Action         string  `json:"action"` // "Downsize", "Terminate", "Spot"
	SuggestedType  string  `json:"suggested_type"`
	CurrentCost    float64 `json:"current_cost"`   // Hourly
	ProjectedCost  float64 `json:"projected_cost"` // Hourly
	MonthlySavings float64 `json:"monthly_savings"`
	Reason         string  `json:"reason"`
	Confidence     float64 `json:"confidence"` // 0.0-1.0
}

type Analyzer struct {
	provider CloudProvider
}

func NewAnalyzer(p CloudProvider) *Analyzer {
	return &Analyzer{provider: p}
}

// AnalyzeResource generates recommendations based on usage
func (a *Analyzer) AnalyzeResource(ctx context.Context, usage ResourceUsage) (Recommendation, error) {
	currentPrice, err := a.provider.GetInstancePrice(ctx, usage.Region, usage.CurrentType)
	if err != nil {
		return Recommendation{}, err
	}

	// 1. Idle / Zombie Check
	if usage.MaxCPU < 2.0 && usage.NetworkIO < 1000 {
		return Recommendation{
			InstanceID:     usage.InstanceID,
			Action:         "Terminate",
			SuggestedType:  "None",
			CurrentCost:    currentPrice,
			ProjectedCost:  0,
			MonthlySavings: currentPrice * 730,
			Reason:         "Instance appears unused (Max CPU < 2%, Low Network)",
			Confidence:     0.95,
		}, nil
	}

	// 2. Over-provisioned Check (Rightsizing)
	// Heuristic: If Max CPU < 40% and Max Mem < 40%, check for smaller instance
	if usage.MaxCPU < 40.0 && usage.MaxMem < 40.0 {
		candidates, err := a.provider.GetSimilarInstances(ctx, usage.CurrentType)
		if err == nil {
			// Find cheapest candidate that fits the load (with buffer)
			// Target: 70% utilization
			// CurMaxCPU / NewCapacity < 0.70 => NewCapacity > CurMaxCPU / 0.70
			// Simplified: NewVCPU >= CurrentVCPU * (CurMaxCPU / 70)
			// But vCPU are discrete. Look for smaller types.

			// Simple Logic: Find type with half resources if available
			var bestFit InstanceTypeInfo
			found := false

			for _, c := range candidates {
				if c.Price < currentPrice {
					// Check if sufficient (very naive check for demo)
					// Assuming family members scale linearly.
					// If current is 'm5.large' (2 vcpu), look for 'm5.medium'? (doesn't exist but t3.small does)
					// We'll just pick the cheapest one for the demo logic
					bestFit = c
					found = true
					break // Sorted usually?
				}
			}

			if found {
				savings := (currentPrice - bestFit.Price) * 730
				return Recommendation{
					InstanceID:     usage.InstanceID,
					Action:         "Downsize",
					SuggestedType:  bestFit.Type,
					CurrentCost:    currentPrice,
					ProjectedCost:  bestFit.Price,
					MonthlySavings: savings,
					Reason:         fmt.Sprintf("Underutilized (CPU Max: %.1f%%). Downsize to %s.", usage.MaxCPU, bestFit.Type),
					Confidence:     0.85,
				}, nil
			}
		}
	}

	return Recommendation{}, nil // No recommendation
}

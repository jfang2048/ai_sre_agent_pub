package finops

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock provider ──────────────────────────────────────────────────────

type mockProvider struct {
	prices     map[string]float64
	candidates []InstanceTypeInfo
	priceErr   error
	simErr     error
}

func (m *mockProvider) GetInstancePrice(_ context.Context, _, instanceType string) (float64, error) {
	if m.priceErr != nil {
		return 0, m.priceErr
	}
	if price, ok := m.prices[instanceType]; ok {
		return price, nil
	}
	return 0, fmt.Errorf("price not found for %s", instanceType)
}

func (m *mockProvider) GetSimilarInstances(_ context.Context, _ string) ([]InstanceTypeInfo, error) {
	if m.simErr != nil {
		return nil, m.simErr
	}
	return m.candidates, nil
}

// ── Idle / Zombie detection tests ──────────────────────────────────────

func TestAnalyzeResourceIdleInstance(t *testing.T) {
	provider := &mockProvider{
		prices: map[string]float64{"m5.large": 0.096},
	}
	a := NewAnalyzer(provider)

	rec, err := a.AnalyzeResource(context.Background(), ResourceUsage{
		InstanceID:  "i-001",
		CurrentType: "m5.large",
		Region:      "us-east-1",
		MaxCPU:      1.0, // < 2% threshold
		NetworkIO:   500, // < 1000 threshold
	})
	require.NoError(t, err)
	assert.Equal(t, "Terminate", rec.Action)
	assert.InDelta(t, 0.95, rec.Confidence, 0.01)
	assert.InDelta(t, 0.096*730, rec.MonthlySavings, 0.01)
}

func TestAnalyzeResourceNotIdleWithCPU(t *testing.T) {
	provider := &mockProvider{
		prices: map[string]float64{"m5.large": 0.096},
	}
	a := NewAnalyzer(provider)

	rec, err := a.AnalyzeResource(context.Background(), ResourceUsage{
		InstanceID:  "i-002",
		CurrentType: "m5.large",
		Region:      "us-east-1",
		MaxCPU:      50, // not idle
		MaxMem:      60, // not under-provisioned
		NetworkIO:   5000,
	})
	require.NoError(t, err)
	assert.Empty(t, rec.Action, "Well-utilized instance should have no recommendation")
}

// ── Rightsizing tests ──────────────────────────────────────────────────

func TestAnalyzeResourceDownsize(t *testing.T) {
	provider := &mockProvider{
		prices: map[string]float64{"m5.large": 0.096},
		candidates: []InstanceTypeInfo{
			{Type: "t3.small", VCPU: 2, Memory: 2.0, Price: 0.0208},
		},
	}
	a := NewAnalyzer(provider)

	rec, err := a.AnalyzeResource(context.Background(), ResourceUsage{
		InstanceID:  "i-003",
		CurrentType: "m5.large",
		Region:      "us-east-1",
		MaxCPU:      20,   // < 40%
		MaxMem:      30,   // < 40%
		NetworkIO:   5000, // not idle
	})
	require.NoError(t, err)
	assert.Equal(t, "Downsize", rec.Action)
	assert.Equal(t, "t3.small", rec.SuggestedType)
	assert.InDelta(t, 0.85, rec.Confidence, 0.01)
	assert.Greater(t, rec.MonthlySavings, 0.0)
}

func TestAnalyzeResourceNoCheaperCandidate(t *testing.T) {
	provider := &mockProvider{
		prices: map[string]float64{"t3.micro": 0.0104},
		candidates: []InstanceTypeInfo{
			// All candidates are more expensive
			{Type: "t3.small", VCPU: 2, Memory: 2.0, Price: 0.0208},
		},
	}
	a := NewAnalyzer(provider)

	rec, err := a.AnalyzeResource(context.Background(), ResourceUsage{
		InstanceID:  "i-004",
		CurrentType: "t3.micro",
		Region:      "us-east-1",
		MaxCPU:      20,
		MaxMem:      30,
		NetworkIO:   5000,
	})
	require.NoError(t, err)
	assert.Empty(t, rec.Action, "No cheaper candidate means no recommendation")
}

// ── Error handling tests ──────────────────────────────────────────────

func TestAnalyzeResourcePriceError(t *testing.T) {
	provider := &mockProvider{
		priceErr: fmt.Errorf("pricing API unavailable"),
	}
	a := NewAnalyzer(provider)

	_, err := a.AnalyzeResource(context.Background(), ResourceUsage{
		InstanceID:  "i-005",
		CurrentType: "m5.large",
		Region:      "us-east-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pricing API unavailable")
}

func TestAnalyzeResourceSimilarInstancesError(t *testing.T) {
	provider := &mockProvider{
		prices: map[string]float64{"m5.large": 0.096},
		simErr: fmt.Errorf("similar API error"),
	}
	a := NewAnalyzer(provider)

	// Under-provisioned but GetSimilarInstances fails — should just return empty rec
	rec, err := a.AnalyzeResource(context.Background(), ResourceUsage{
		InstanceID:  "i-006",
		CurrentType: "m5.large",
		Region:      "us-east-1",
		MaxCPU:      20,
		MaxMem:      20,
		NetworkIO:   5000,
	})
	require.NoError(t, err)
	assert.Empty(t, rec.Action, "Should gracefully degrade if similar instances API fails")
}

// ── AWSProvider stub tests ─────────────────────────────────────────────

func TestAWSProviderKnownPrice(t *testing.T) {
	p := NewAWSProvider()
	price, err := p.GetInstancePrice(context.Background(), "us-east-1", "m5.large")
	require.NoError(t, err)
	assert.InDelta(t, 0.096, price, 0.001)
}

func TestAWSProviderUnknownPrice(t *testing.T) {
	p := NewAWSProvider()
	_, err := p.GetInstancePrice(context.Background(), "us-east-1", "imaginary.xlarge")
	require.Error(t, err)
}

func TestAWSProviderSimilarInstances(t *testing.T) {
	p := NewAWSProvider()
	instances, err := p.GetSimilarInstances(context.Background(), "m5.large")
	require.NoError(t, err)
	assert.NotEmpty(t, instances)
}

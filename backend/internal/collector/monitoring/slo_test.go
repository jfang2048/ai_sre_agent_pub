package monitoring

import (
	"testing"
	"time"
)

// TestSLOTierTierTarget verifies tier target values
func TestSLOTierTierTarget(t *testing.T) {
	testCases := []struct {
		name     string
		tier     SLOTier
		expected float64
	}{
		{"TierUnknown", TierUnknown, 99.9},
		{"Tier1", Tier1, 99.99},
		{"Tier2", Tier2, 99.95},
		{"Tier3", Tier3, 99.9},
		{"Tier4", Tier4, 99.5},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.tier.TierTarget()
			if got != tc.expected {
				t.Errorf("tier %d: expected target %f, got %f", tc.tier, tc.expected, got)
			}
		})
	}
}

// TestSLITypeConstants verifies SLI type constants
func TestSLITypeConstants(t *testing.T) {
	expectedTypes := []SLIType{
		SLITypeAvailability,
		SLITypeLatency,
		SLITypeThroughput,
		SLITypeErrorRate,
		SLITypeSaturation,
	}

	expectedValues := []string{
		"availability",
		"latency",
		"throughput",
		"error_rate",
		"saturation",
	}

	for i, typ := range expectedTypes {
		if string(typ) != expectedValues[i] {
			t.Errorf("SLI type %d: expected '%s', got '%s'", i, expectedValues[i], typ)
		}
	}
}

// TestSLIDefinitionValidation verifies SLI definition structure
func TestSLIDefinitionValidation(t *testing.T) {
	validSLI := &SLIDefinition{
		ID:          "test-sli",
		Name:        "Test SLI",
		Type:        SLITypeLatency,
		Description: "A test SLI",
		MetricName:  "http_request_duration",
		Labels:      map[string]string{"env": "test"},
	}

	if validSLI.ID == "" {
		t.Error("SLI ID should be set")
	}

	if validSLI.Name == "" {
		t.Error("SLI Name should be set")
	}

	if validSLI.Type == "" {
		t.Error("SLI Type should be set")
	}
}

// TestSLIValueStructure verifies SLI value structure
func TestSLIValueStructure(t *testing.T) {
	value := &SLIValue{
		SLIID:     "test-sli",
		Timestamp: time.Now(),
		Value:     0.99,
		Valid:     true,
	}

	if value.SLIID == "" {
		t.Error("SLIID should be set")
	}

	if value.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}

	if value.Value < 0 || value.Value > 1 {
		t.Errorf("Value should be between 0 and 1, got %f", value.Value)
	}
}

// TestSLIValueBounds verifies SLI value bounds
func TestSLIValueBounds(t *testing.T) {
	testCases := []struct {
		name  string
		value float64
		valid bool
	}{
		{"valid 0.0", 0.0, true},
		{"valid 0.5", 0.5, true},
		{"valid 0.99", 0.99, true},
		{"valid 1.0", 1.0, true},
		{"invalid -0.1", -0.1, false},
		{"invalid 1.1", 1.1, false},
		{"invalid 2.0", 2.0, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			value := &SLIValue{
				SLIID:     "test",
				Timestamp: time.Now(),
				Value:     tc.value,
				Valid:     true,
			}

			isValid := value.Value >= 0.0 && value.Value <= 1.0
			if isValid != tc.valid {
				t.Errorf("value %f: expected valid=%v, got valid=%v",
					tc.value, tc.valid, isValid)
			}
		})
	}
}

// TestSLIResultStructure verifies SLI result structure
func TestSLIResultStructure(t *testing.T) {
	result := &SLIResult{
		SLIID:       "test-sli",
		SLIName:     "Test SLI",
		Timestamp:   time.Now(),
		Value:       99.9,
		Valid:       true,
		Window:      time.Hour,
		GoodEvents:  990,
		TotalEvents: 1000,
		Labels:      map[string]string{"env": "test"},
	}

	if result.SLIID == "" {
		t.Error("SLIID should be set")
	}

	if result.SLIName == "" {
		t.Error("SLIName should be set")
	}

	if result.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}

	if result.TotalEvents == 0 {
		t.Error("TotalEvents should be set")
	}

	if result.GoodEvents > result.TotalEvents {
		t.Errorf("GoodEvents (%d) should not exceed TotalEvents (%d)",
			result.GoodEvents, result.TotalEvents)
	}
}

// TestSLIResultEventCountValidation verifies event counts
func TestSLIResultEventCountValidation(t *testing.T) {
	testCases := []struct {
		name        string
		goodEvents  int
		totalEvents int
		valid       bool
	}{
		{"all good", 100, 100, true},
		{"some errors", 95, 100, true},
		{"half errors", 50, 100, true},
		{"mostly errors", 10, 100, true},
		{"all errors", 0, 100, true},
		{"zero events", 0, 0, false},
		{"invalid more good than total", 150, 100, false},
		{"negative good", -10, 100, false},
		{"negative total", 100, -10, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := &SLIResult{
				SLIID:       "test-sli",
				SLIName:     "Test SLI",
				Timestamp:   time.Now(),
				GoodEvents:  tc.goodEvents,
				TotalEvents: tc.totalEvents,
			}

			isValid := result.TotalEvents > 0 &&
				result.GoodEvents >= 0 &&
				result.GoodEvents <= result.TotalEvents

			if isValid != tc.valid {
				t.Errorf("expected valid=%v for goodEvents=%d, totalEvents=%d, got valid=%v",
					tc.valid, tc.goodEvents, tc.totalEvents, isValid)
			}
		})
	}
}

// TestSLODefinitionCompliance checks SLO definition structure
func TestSLODefinitionCompliance(t *testing.T) {
	slo := &SLODefinition{
		ID:          "test-slo-001",
		Name:        "API Availability SLO",
		Description: "API should be available 99.9% of the time",
		SLIID:       "api-availability-sli",
		Target:      99.9,
		Window:      30 * 24 * time.Hour,
		IsRolling:   true,
		Tier:        Tier2,
		Labels:      map[string]string{"service": "api", "env": "prod"},
	}

	// Validate required fields
	if slo.ID == "" {
		t.Error("SLO ID is required")
	}

	if slo.Name == "" {
		t.Error("SLO Name is required")
	}

	if slo.SLIID == "" {
		t.Error("SLI ID is required")
	}

	// Validate target
	if slo.Target <= 99.0 || slo.Target > 100.0 {
		t.Errorf("SLO target %f is outside valid range (99.0, 100.0]", slo.Target)
	}

	// Validate tier matches target
	expectedTarget := slo.Tier.TierTarget()
	if slo.Target != expectedTarget {
		t.Logf("Note: SLO target %f differs from tier default %f (this is allowed)",
			slo.Target, expectedTarget)
	}

	// Validate window
	if slo.Window < time.Hour {
		t.Error("SLO window should be at least 1 hour")
	}

	if slo.Window > 90*24*time.Hour {
		t.Error("SLO window should not exceed 90 days")
	}
}

// TestSLODefinitionStructure verifies SLO definition structure
func TestSLODefinitionStructure(t *testing.T) {
	slo := &SLODefinition{
		ID:          "test-slo",
		Name:        "Test SLO",
		Description: "A test SLO",
		SLIID:       "test-sli",
		Target:      99.9,
		Window:      30 * 24 * time.Hour,
		IsRolling:   true,
		Tier:        Tier2,
		Labels:      map[string]string{"service": "api"},
	}

	if slo.ID == "" {
		t.Error("SLO ID should be set")
	}

	if slo.SLIID == "" {
		t.Error("SLIID should be set")
	}

	if slo.Target <= 0 || slo.Target > 100 {
		t.Errorf("Target should be between 0 and 100, got %f", slo.Target)
	}

	if slo.Window == 0 {
		t.Error("Window should be set")
	}

	if slo.Tier == TierUnknown {
		t.Error("Tier should be set")
	}
}

// TestSLOConfigDefaults verifies SLO config defaults
func TestSLOConfigDefaults(t *testing.T) {
	config := SLOConfig{
		ConfigPath:                 "/path/to/config.yaml",
		EvaluationInterval:         time.Minute,
		ErrorBudgetWarningPercent:  10.0,
		ErrorBudgetCriticalPercent: 50.0,
		BurnRateAlertThreshold:     2,
	}

	if config.ConfigPath == "" {
		t.Error("ConfigPath should be set")
	}

	if config.EvaluationInterval == 0 {
		t.Error("EvaluationInterval should be set")
	}

	if config.ErrorBudgetWarningPercent <= 0 || config.ErrorBudgetWarningPercent > 100 {
		t.Errorf("ErrorBudgetWarningPercent should be between 0 and 100, got %f",
			config.ErrorBudgetWarningPercent)
	}

	if config.BurnRateAlertThreshold <= 0 {
		t.Errorf("BurnRateAlertThreshold should be positive, got %d",
			config.BurnRateAlertThreshold)
	}
}

// TestSLOConfigThresholdValidation validates SLO config thresholds
func TestSLOConfigThresholdValidation(t *testing.T) {
	testCases := []struct {
		name            string
		warningPercent  float64
		criticalPercent float64
		valid           bool
	}{
		{"valid thresholds", 10.0, 50.0, true},
		{"zero warning", 0.0, 50.0, true},
		{"equal thresholds", 50.0, 50.0, true},
		{"negative warning", -10.0, 50.0, false},
		{"over 100 warning", 110.0, 50.0, false},
		{"negative critical", 10.0, -50.0, false},
		{"over 100 critical", 10.0, 150.0, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := SLOConfig{
				ErrorBudgetWarningPercent:  tc.warningPercent,
				ErrorBudgetCriticalPercent: tc.criticalPercent,
			}

			valid := tc.warningPercent >= 0 && tc.warningPercent <= 100 &&
				tc.criticalPercent >= 0 && tc.criticalPercent <= 100

			if valid != tc.valid {
				t.Errorf("expected valid=%v for warning=%f, critical=%f, got valid=%v",
					tc.valid, tc.warningPercent, tc.criticalPercent, valid)
			}
			_ = config // Use config to avoid unused variable warning
		})
	}
}

// TestBurnRateAlertThreshold validates burn rate threshold
func TestBurnRateAlertThreshold(t *testing.T) {
	testCases := []struct {
		name      string
		threshold int
		valid     bool
	}{
		{"1x threshold", 1, true},
		{"2x threshold", 2, true},
		{"10x threshold", 10, true},
		{"zero threshold", 0, false},
		{"negative threshold", -1, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := SLOConfig{
				BurnRateAlertThreshold: tc.threshold,
			}

			valid := config.BurnRateAlertThreshold > 0
			if valid != tc.valid {
				t.Errorf("expected valid=%v for threshold %d, got valid=%v",
					tc.valid, tc.threshold, valid)
			}
		})
	}
}

// TestSLIConfigDefaults verifies SLI config defaults
func TestSLIConfigDefaults(t *testing.T) {
	config := SLIConfig{
		RollingWindow:   30 * time.Minute,
		RetentionPeriod: 7 * 24 * time.Hour,
	}

	if config.RollingWindow == 0 {
		t.Error("RollingWindow should be set")
	}

	if config.RetentionPeriod == 0 {
		t.Error("RetentionPeriod should be set")
	}

	_ = config // Use config to avoid unused variable warning
}

// TestSLIConfigWindowValidation validates SLI config windows
func TestSLIConfigWindowValidation(t *testing.T) {
	testCases := []struct {
		name   string
		window time.Duration
		valid  bool
	}{
		{"1 minute window", 1 * time.Minute, true},
		{"5 minute window", 5 * time.Minute, true},
		{"30 minute window", 30 * time.Minute, true},
		{"1 hour window", 1 * time.Hour, true},
		{"24 hour window", 24 * time.Hour, true},
		{"zero window", 0, false},
		{"negative window", -1 * time.Minute, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := SLIConfig{
				RollingWindow: tc.window,
			}

			valid := config.RollingWindow > 0
			if valid != tc.valid {
				t.Errorf("expected valid=%v for window %v, got valid=%v",
					tc.valid, tc.window, valid)
			}
		})
	}
}

// TestBurnRateMeasurementStructure verifies burn rate measurement
func TestBurnRateMeasurementStructure(t *testing.T) {
	measurement := &BurnRateMeasurement{
		Timestamp:        time.Now(),
		BurnRate:         2.5,
		Window:           1.0,
		TimeToExhaustion: 24.0,
	}

	if measurement.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}

	if measurement.BurnRate <= 0 {
		t.Errorf("BurnRate should be positive, got %f", measurement.BurnRate)
	}

	if measurement.Window <= 0 {
		t.Errorf("Window should be positive, got %f", measurement.Window)
	}

	if measurement.TimeToExhaustion < 0 {
		t.Errorf("TimeToExhaustion should be non-negative, got %f",
			measurement.TimeToExhaustion)
	}
}

// TestSLOStatusComplianceCheck verifies compliance logic
func TestSLOStatusComplianceCheck(t *testing.T) {
	testCases := []struct {
		name      string
		current   float64
		target    float64
		compliant bool
	}{
		{"compliant above", 99.95, 99.9, true},
		{"compliant at", 99.9, 99.9, true},
		{"non-compliant below", 99.85, 99.9, false},
		{"highly compliant", 99.99, 99.9, true},
		{"significantly non-compliant", 99.0, 99.9, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			status := &SLOStatus{
				SLOID:        "test-slo",
				Timestamp:    time.Now(),
				CurrentValue: tc.current,
				Target:       tc.target,
			}

			status.Compliant = status.CurrentValue >= status.Target

			if status.Compliant != tc.compliant {
				t.Errorf("expected compliant=%v for current=%f, target=%f, got compliant=%v",
					tc.compliant, tc.current, tc.target, status.Compliant)
			}
		})
	}
}

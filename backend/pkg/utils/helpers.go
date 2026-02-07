package utils

import (
	"encoding/json"
	"net/http"
	"time"
)

// WriteJSON writes a JSON response to the writer
func WriteJSON(w http.ResponseWriter, status int, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(data)
}

// WriteError writes an error response to the writer
func WriteError(w http.ResponseWriter, status int, message string) error {
	return WriteJSON(w, status, map[string]string{"error": message})
}

// GetBool extracts a bool value from a map
func GetBool(m map[string]interface{}, key string, defaultValue bool) bool {
	if m == nil {
		return defaultValue
	}
	val, ok := m[key]
	if !ok {
		return defaultValue
	}
	if b, ok := val.(bool); ok {
		return b
	}
	return defaultValue
}

// GetString extracts a string value from a map
func GetString(m map[string]interface{}, key string, defaultValue string) string {
	if m == nil {
		return defaultValue
	}
	val, ok := m[key]
	if !ok {
		return defaultValue
	}
	if s, ok := val.(string); ok {
		return s
	}
	return defaultValue
}

// GetInt extracts an int value from a map
func GetInt(m map[string]interface{}, key string, defaultValue int) int {
	if m == nil {
		return defaultValue
	}
	val, ok := m[key]
	if !ok {
		return defaultValue
	}
	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return defaultValue
}

// GetDuration extracts a duration value from a map
func GetDuration(m map[string]interface{}, key string, defaultValue time.Duration) time.Duration {
	if m == nil {
		return defaultValue
	}
	val, ok := m[key]
	if !ok {
		return defaultValue
	}
	switch v := val.(type) {
	case time.Duration:
		return v
	case string:
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultValue
}

// GetStringSlice extracts a string slice from a map
func GetStringSlice(m map[string]interface{}, key string, defaultValue []string) []string {
	if m == nil {
		return defaultValue
	}
	val, ok := m[key]
	if !ok {
		return defaultValue
	}
	if slice, ok := val.([]string); ok {
		return slice
	}
	if slice, ok := val.([]interface{}); ok {
		result := make([]string, 0, len(slice))
		for _, v := range slice {
			if s, ok := v.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return defaultValue
}

// LabelsEqual checks if two label maps are equal
func LabelsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if vb, ok := b[k]; !ok || v != vb {
			return false
		}
	}
	return true
}

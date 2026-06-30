package security

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaskPIIRecursiveMasking(t *testing.T) {
	input := map[string]interface{}{
		"username": "alice",
		"token":    "root-token",
		"nested": map[string]interface{}{
			"password": "p@ssw0rd",
			"safe":     "value",
		},
		"items": []interface{}{
			map[string]interface{}{
				"api_key": "abcd",
				"child": map[string]interface{}{
					"jwt": "header.payload.sig",
				},
			},
			"plain",
			[]interface{}{
				map[string]interface{}{"secret": "deep-secret"},
			},
		},
	}

	masked := MaskPII(input)
	require.Equal(t, "***", masked["token"])
	require.Equal(t, "alice", masked["username"])

	nested, ok := masked["nested"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "***", nested["password"])
	require.Equal(t, "value", nested["safe"])

	items, ok := masked["items"].([]interface{})
	require.True(t, ok)
	first, ok := items[0].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "***", first["api_key"])

	child, ok := first["child"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "***", child["jwt"])

	third, ok := items[2].([]interface{})
	require.True(t, ok)
	deep, ok := third[0].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "***", deep["secret"])
}

func TestMaskPIIDoesNotMutateInput(t *testing.T) {
	input := map[string]interface{}{
		"token": "secret-token",
		"nested": map[string]interface{}{
			"password": "pw",
		},
	}

	_ = MaskPII(input)

	nested, ok := input["nested"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "secret-token", input["token"])
	require.Equal(t, "pw", nested["password"])
}

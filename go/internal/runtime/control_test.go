package runtime

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestRestartRequestJSONRoundtrip verifies that RestartRequest marshals to and
// from JSON without data loss.
func TestRestartRequestJSONRoundtrip(t *testing.T) {
	original := RestartRequest{
		Target:      "worker",
		RequestedBy: "test-agent",
		Reason:      "config update",
		RequestedAt: time.Now().Unix(),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded RestartRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if decoded.Target != original.Target {
		t.Errorf("Target: got %q, want %q", decoded.Target, original.Target)
	}
	if decoded.RequestedBy != original.RequestedBy {
		t.Errorf("RequestedBy: got %q, want %q", decoded.RequestedBy, original.RequestedBy)
	}
	if decoded.Reason != original.Reason {
		t.Errorf("Reason: got %q, want %q", decoded.Reason, original.Reason)
	}
	if decoded.RequestedAt != original.RequestedAt {
		t.Errorf("RequestedAt: got %d, want %d", decoded.RequestedAt, original.RequestedAt)
	}
}

// TestRestartRequestJSONFieldNames verifies the exported JSON field names used
// by the wire format.
func TestRestartRequestJSONFieldNames(t *testing.T) {
	req := RestartRequest{
		Target:      "worker",
		RequestedBy: "agent",
		Reason:      "reason",
		RequestedAt: 1234567890,
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	for _, field := range []string{"target", "requested_by", "reason", "requested_at"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("expected JSON field %q not present in %s", field, data)
		}
	}
}

// TestIsSupervisedRuntimeFalse verifies the default (unset) state.
func TestIsSupervisedRuntimeFalse(t *testing.T) {
	t.Setenv(SupervisedEnv, "")
	if IsSupervisedRuntime() {
		t.Error("IsSupervisedRuntime() = true, want false when env is unset")
	}
}

// TestIsSupervisedRuntimeTrue verifies that CUSTOMCLAW_SUPERVISED=1 is detected.
func TestIsSupervisedRuntimeTrue(t *testing.T) {
	t.Setenv(SupervisedEnv, "1")
	if !IsSupervisedRuntime() {
		t.Error("IsSupervisedRuntime() = false, want true when env is 1")
	}
}

// TestIsSupervisedRuntimeOtherValue verifies that only "1" is truthy.
func TestIsSupervisedRuntimeOtherValue(t *testing.T) {
	for _, v := range []string{"true", "yes", "0", "2"} {
		t.Run(v, func(t *testing.T) {
			os.Setenv(SupervisedEnv, v) //nolint:tenv // t.Setenv is preferred but we use Setenv + cleanup
			t.Cleanup(func() { os.Unsetenv(SupervisedEnv) })
			if IsSupervisedRuntime() {
				t.Errorf("IsSupervisedRuntime() = true for env value %q, want false", v)
			}
		})
	}
}

// TestRestartKeyFormat verifies the Redis key format for restart requests.
func TestRestartKeyFormat(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{"worker", "customclaw:control:restart:worker"},
		{"app", "customclaw:control:restart:app"},
		{"my-service", "customclaw:control:restart:my-service"},
	}

	for _, tc := range tests {
		got := restartKey(tc.target)
		if got != tc.want {
			t.Errorf("restartKey(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

// TestRestartKeyPrefix verifies the exported constant matches the key format.
func TestRestartKeyPrefix(t *testing.T) {
	key := restartKey("worker")
	if key[:len(RestartKeyPrefix)] != RestartKeyPrefix {
		t.Errorf("restartKey does not start with RestartKeyPrefix: got %q", key)
	}
}

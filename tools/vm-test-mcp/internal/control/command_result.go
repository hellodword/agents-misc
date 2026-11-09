package control

import "slices"

func expectedExitCodes(values []int) []int {
	if len(values) == 0 {
		return []int{0}
	}
	result := slices.Clone(values)
	slices.Sort(result)
	return result
}

func (c *Command) outcome() string {
	if !c.Final {
		return "pending"
	}
	if c.State == "timed_out" {
		return "timed_out"
	}
	if c.State == "cancelled" {
		return "cancelled"
	}
	if c.ExitSignal != "" {
		return "signaled"
	}
	if c.State == "exited" && c.ExitCode != nil {
		if slices.Contains(expectedExitCodes(c.ExpectedExitCodes), *c.ExitCode) {
			return "expected_exit"
		}
		return "unexpected_exit"
	}
	if c.State == "failed" {
		return "failed"
	}
	return "unconfirmed"
}

func diagnosticOutcome(value string) bool {
	return oneOf(value, "unexpected_exit", "signaled", "abnormal_exit", "timed_out", "failed", "unconfirmed")
}

func directOutcome(result map[string]any, expected []int) string {
	if result["timed_out"] == true {
		return "timed_out"
	}
	if result["termination_confirmed"] != true {
		return "unconfirmed"
	}
	if result["termination"] != "exited" {
		return "abnormal_exit"
	}
	if code, ok := result["exit_code"].(float64); ok {
		if slices.Contains(expected, int(code)) {
			return "expected_exit"
		}
		return "unexpected_exit"
	}
	if minimum, ok := result["exit_code_min"].(float64); ok {
		for _, code := range expected {
			if code >= int(minimum) {
				return "unconfirmed"
			}
		}
		return "unexpected_exit"
	}
	return "unconfirmed"
}

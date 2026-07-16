package main

import "testing"

func TestCanaryPortValidation(t *testing.T) {
	if port, err := canaryPort("5188"); err != nil || port != 5188 {
		t.Fatalf("port = %d, err = %v", port, err)
	}
	for _, value := range []string{"bad", "0", "70000"} {
		if _, err := canaryPort(value); err == nil {
			t.Errorf("canaryPort(%q) succeeded", value)
		}
	}
}

func TestCanaryProcessAliveRejectsInvalidPID(t *testing.T) {
	if canaryProcessAlive(0) || canaryProcessAlive(-1) {
		t.Fatal("invalid pid reported alive")
	}
}

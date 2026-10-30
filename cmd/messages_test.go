package cmd

import "testing"

func TestFlagColorRequiresFlaggingAndValidColour(t *testing.T) {
	if err := validateFlagColorArgs(true, 4); err != nil {
		t.Errorf("--color 4 with flagging should be accepted: %v", err)
	}
	if err := validateFlagColorArgs(false, 4); err == nil {
		t.Error("--color with --flagged=false must be rejected")
	}
	if err := validateFlagColorArgs(true, 7); err == nil {
		t.Error("--color 7 is out of range and must be rejected")
	}
}

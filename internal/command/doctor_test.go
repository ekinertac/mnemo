package command

import (
	"testing"
	"time"
)

func TestDoctorHasFailure(t *testing.T) {
	clean := &doctorReport{checks: []check{{"a", statusOK, ""}, {"b", statusWarn, ""}}}
	if clean.hasFailure() {
		t.Error("ok+warn should not be a failure")
	}
	bad := &doctorReport{checks: []check{{"a", statusOK, ""}, {"b", statusFail, ""}}}
	if !bad.hasFailure() {
		t.Error("a FAIL check must make hasFailure true")
	}
}

func TestDaysSince(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	d, ok := daysSince("2026-06-07T12:00:00Z", now)
	if !ok || d != 10 {
		t.Errorf("daysSince = %d,%v want 10,true", d, ok)
	}
	if _, ok := daysSince("not-a-time", now); ok {
		t.Error("unparseable timestamp must return ok=false")
	}
}

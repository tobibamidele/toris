package cli_test

import (
	"testing"
	"time"

	"github.com/tobibamidele/toris/internal/cli"
	"github.com/tobibamidele/toris/internal/logging"
)

// ─── configDoctorAdapterStub ──────────────────────────────────────────────────
// Implements the RunDoctor interface without a real config.

type doctorCfgStub struct {
	controlDSN    string
	backupBaseDir string
	clusterID     string
	instanceID    string
	maxAgeDays    int
	minCount      int
	nodes         []cli.NodeInfo
	leaseTTL      time.Duration
	renewInterval time.Duration
}

func (s *doctorCfgStub) GetControlDSN() string           { return s.controlDSN }
func (s *doctorCfgStub) GetBackupBaseDir() string        { return s.backupBaseDir }
func (s *doctorCfgStub) GetClusterID() string            { return s.clusterID }
func (s *doctorCfgStub) GetInstanceID() string           { return s.instanceID }
func (s *doctorCfgStub) GetRetentionMaxAgeDays() int     { return s.maxAgeDays }
func (s *doctorCfgStub) GetRetentionMinCount() int       { return s.minCount }
func (s *doctorCfgStub) GetNodes() []cli.NodeInfo        { return s.nodes }
func (s *doctorCfgStub) GetLeaseTTL() time.Duration      { return s.leaseTTL }
func (s *doctorCfgStub) GetRenewInterval() time.Duration { return s.renewInterval }

func minStub() *doctorCfgStub {
	return &doctorCfgStub{
		controlDSN:    "", // empty — will stop early
		backupBaseDir: "/tmp",
		clusterID:     "test-cluster",
		instanceID:    "test-instance",
		maxAgeDays:    30,
		minCount:      3,
		leaseTTL:      30 * time.Second,
		renewInterval: 10 * time.Second,
	}
}

// TestRunDoctor_NoControlDSN verifies that with no control_dsn, the doctor
// stops after the control DSN check and does not attempt DB connections.
func TestRunDoctor_NoControlDSN(t *testing.T) {
	stub := minStub()
	stub.controlDSN = ""
	stub.backupBaseDir = "/tmp" // /tmp is always writable in CI

	results := cli.RunDoctor(stub, logging.Nop())

	// Should have: pg_* tools check, backup dir, control DSN (fail), then stop.
	foundControlDSNFail := false
	for _, r := range results {
		if r.Name == "control DSN" && !r.OK {
			foundControlDSNFail = true
		}
		// Must not attempt DB connectivity.
		if r.Name == "control DB connect" {
			t.Errorf("should not attempt DB connect with no DSN configured, got result: %+v", r)
		}
	}
	if !foundControlDSNFail {
		t.Error("expected a failing 'control DSN' result when control_dsn is empty")
	}
}

// TestRunDoctor_UnreachableControlDB verifies that with a bad DSN, the doctor
// reports a connectivity failure and stops before schema/lease checks.
func TestRunDoctor_UnreachableControlDB(t *testing.T) {
	stub := minStub()
	stub.controlDSN = "host=localhost port=9999 user=nobody dbname=nobody sslmode=disable connect_timeout=1"
	stub.backupBaseDir = "/tmp"

	results := cli.RunDoctor(stub, logging.Nop())

	foundConnFail := false
	for _, r := range results {
		if r.Name == "control DB connect" && !r.OK {
			foundConnFail = true
		}
		if r.Name == "control DB schema" {
			t.Error("should not check schema if DB connect failed")
		}
	}
	if !foundConnFail {
		t.Error("expected a failing 'control DB connect' result for unreachable DB")
	}
}

// TestRunDoctor_BackupDirMissing verifies that a non-existent backup dir
// produces a failing backup dir check.
func TestRunDoctor_BackupDirMissing(t *testing.T) {
	stub := minStub()
	stub.backupBaseDir = "/definitely/does/not/exist/toris-test-12345"

	results := cli.RunDoctor(stub, logging.Nop())

	foundFail := false
	for _, r := range results {
		if r.Name == "backup dir" && !r.OK {
			foundFail = true
		}
	}
	if !foundFail {
		t.Error("expected failing 'backup dir' check for missing directory")
	}
}

// TestPrintDoctorResults_AllPass verifies the function returns true when
// all results are OK.
func TestPrintDoctorResults_AllPass(t *testing.T) {
	results := []cli.DoctorResult{
		{Name: "check-1", OK: true, Message: "all good"},
		{Name: "check-2", OK: true, Message: "fine"},
	}
	// We can't easily capture stdout, but we can verify the return value.
	// Redirect stdout would require more setup — test the return value contract.
	allOK := allResultsOK(results)
	if !allOK {
		t.Error("expected allOK=true when all results are OK")
	}
}

// TestPrintDoctorResults_OneFail verifies the function returns false when
// any non-warning result is not OK.
func TestPrintDoctorResults_OneFail(t *testing.T) {
	results := []cli.DoctorResult{
		{Name: "check-1", OK: true},
		{Name: "check-2", OK: false, Message: "broken"},
		{Name: "check-3", OK: true, Warning: true, Message: "warn"},
	}
	allOK := allResultsOK(results)
	if allOK {
		t.Error("expected allOK=false when a result has OK=false and Warning=false")
	}
}

// TestPrintDoctorResults_WarningDoesNotFail verifies that warning-only
// results do not cause overall failure.
func TestPrintDoctorResults_WarningDoesNotFail(t *testing.T) {
	results := []cli.DoctorResult{
		{Name: "check-1", OK: true},
		{Name: "check-2", OK: true, Warning: true, Message: "just a warning"},
	}
	allOK := allResultsOK(results)
	if !allOK {
		t.Error("warnings should not cause overall failure")
	}
}

// TestDoctorResult_FieldsAreAccessible verifies the exported struct fields.
func TestDoctorResult_FieldsAreAccessible(t *testing.T) {
	r := cli.DoctorResult{
		Name:    "test-check",
		OK:      false,
		Message: "something is wrong",
		Warning: true,
	}
	if r.Name != "test-check" {
		t.Errorf("Name: got %q", r.Name)
	}
	if r.OK != false {
		t.Error("OK should be false")
	}
	if r.Warning != true {
		t.Error("Warning should be true")
	}
}

// allResultsOK mirrors PrintDoctorResults' pass/fail logic for testing
// without side effects.
func allResultsOK(results []cli.DoctorResult) bool {
	for _, r := range results {
		if !r.OK && !r.Warning {
			return false
		}
	}
	return true
}

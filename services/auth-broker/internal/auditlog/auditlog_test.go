package auditlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpen_StdoutOnly(t *testing.T) {
	logger, closer, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	logger.Info("smoke")
	// nothing else to assert; we just want no panic + clean close.
}

func TestOpen_FileMirror(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	logger, closer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("audit", "action", "auth_code.redeemed",
		"subject_id", "abc", "source_ip", "203.0.113.5")
	logger.Info("audit", "action", "auth_code.issued",
		"subject_id", "def", "source_ip", "10.0.0.1")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		`"action":"auth_code.redeemed"`,
		`"action":"auth_code.issued"`,
		`"subject_id":"abc"`,
		`"subject_id":"def"`,
		`"source_ip":"203.0.113.5"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("audit file missing %q\nfull contents:\n%s", want, s)
		}
	}
}

func TestOpen_AppendsRatherThanOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	for _, msg := range []string{"first", "second"} {
		logger, closer, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		logger.Info("audit", "action", msg)
		closer.Close()
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "first") || !strings.Contains(string(body), "second") {
		t.Fatalf("expected both records preserved, got:\n%s", body)
	}
}

func TestOpen_BadPathErrors(t *testing.T) {
	if _, _, err := Open("/proc/this/path/does/not/exist/audit.log"); err == nil {
		t.Fatal("expected error opening unwriteable path")
	}
}

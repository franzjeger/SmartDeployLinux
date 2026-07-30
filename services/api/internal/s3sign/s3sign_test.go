package s3sign

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// Known-answer test from the AWS SigV4 documentation example
// ("Authenticating Requests: Using Query Parameters"): presigned GET of
// s3.amazonaws.com/examplebucket/test.txt with the documented example
// credentials at 2013-05-24T00:00:00Z, expiring in 86400s.
func TestPresign_AWSDocumentedVector(t *testing.T) {
	s := &Signer{
		Endpoint:  "https://examplebucket.s3.amazonaws.com",
		Region:    "us-east-1",
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Now:       func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) },
	}
	got, err := s.Presign("GET", "", "test.txt", 86400*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/test.txt" {
		t.Fatalf("path = %s", u.Path)
	}
	const wantSig = "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if sig := u.Query().Get("X-Amz-Signature"); sig != wantSig {
		t.Fatalf("signature mismatch:\n got %s\nwant %s", sig, wantSig)
	}
}

func TestPresign_EscapesKeySegments(t *testing.T) {
	s := &Signer{
		Endpoint:  "http://minio:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Now:       func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}
	got, err := s.Presign("PUT", "images", "win 11/install.wim", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/images/win%2011/install.wim?") {
		t.Fatalf("key not escaped: %s", got)
	}
	// Deterministic given pinned time.
	again, _ := s.Presign("PUT", "images", "win 11/install.wim", time.Hour)
	if got != again {
		t.Fatal("presign not deterministic with pinned clock")
	}
}

func TestPresign_RequiresConfig(t *testing.T) {
	if _, err := (&Signer{}).Presign("PUT", "b", "k", time.Hour); err == nil {
		t.Fatal("expected error without endpoint/credentials")
	}
}

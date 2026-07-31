package main

import (
	"os"
	"strings"
	"testing"

	"github.com/your-org/deployserver/api/internal/store"
)

func TestRestoreShMatchesCanonical(t *testing.T) {
	if len(restoreShBody) < 1000 {
		t.Fatalf("restore.sh looks like a stub (%d bytes)", len(restoreShBody))
	}
	for _, want := range []string{
		"DEPLOY_ARCHIVE_URL", "grub-install", "machine-id", "ssh-keygen -A",
		"Authorization: Bearer", "fstab",
	} {
		if !strings.Contains(restoreShBody, want) {
			t.Fatalf("restore.sh missing %q", want)
		}
	}
	if strings.Contains(restoreShBody, "?token=") {
		t.Fatal("restore.sh must not use query-string tokens")
	}
	canonical, err := os.ReadFile("../../../../linux/scripts/restore.sh")
	if err != nil {
		t.Skipf("canonical restore.sh not present: %v", err)
	}
	if string(canonical) != restoreShBody {
		t.Fatal("services/api/cmd/api/restore.sh out of sync with linux/scripts/restore.sh")
	}
}

func TestIsLinuxGolden(t *testing.T) {
	mk := func(family, blobKey, media string) *store.RenderBundle {
		b := &store.RenderBundle{
			ImageOSFamily: family, ImageBlobKey: blobKey,
		}
		if media != "" {
			b.ImageMedia = []byte(media)
		}
		return b
	}
	cases := []struct {
		name string
		b    *store.RenderBundle
		want bool
	}{
		{"golden linux", mk("linux", "ab/sha-golden.tar.zst", `{"deploy_method":"golden"}`), true},
		{"linux installer (no flag)", mk("linux", "ab/sha-x", `{}`), false},
		{"linux flag but no blob", mk("linux", "", `{"deploy_method":"golden"}`), false},
		{"windows golden flag", mk("windows", "ab/sha-x", `{"deploy_method":"golden"}`), false},
		{"nil media", mk("linux", "ab/sha-x", ""), false},
	}
	for _, tc := range cases {
		if got := isLinuxGolden(tc.b); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestMediaDeployMethod(t *testing.T) {
	if got := mediaDeployMethod([]byte(`{"deploy_method":"golden","wim_url":"x"}`)); got != "golden" {
		t.Fatalf("got %q", got)
	}
	if got := mediaDeployMethod(nil); got != "" {
		t.Fatalf("nil media: %q", got)
	}
	if got := mediaDeployMethod([]byte(`not-json`)); got != "" {
		t.Fatalf("bad json: %q", got)
	}
}

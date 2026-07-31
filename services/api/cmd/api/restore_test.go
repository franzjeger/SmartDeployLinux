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
		// Phase 21: BIOS + LVM support.
		"i386-pc", "x86_64-efi", "/sys/firmware/efi",
		"vgcreate", "lvcreate", "mkswap",
		"update-initramfs", "dracut",
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

func TestRestoreLayoutEnv(t *testing.T) {
	cases := []struct {
		name string
		vars string
		want string
	}{
		{"default", "", "export DEPLOY_LAYOUT=plain"},
		{"explicit plain", `{"restore_layout":"plain"}`, "export DEPLOY_LAYOUT=plain"},
		{"lvm defaults", `{"restore_layout":"lvm"}`, "export DEPLOY_LAYOUT=lvm"},
		{"lvm full", `{"restore_layout":"lvm","restore_vg":"vg_sys","restore_swap":"8G"}`,
			"export DEPLOY_LAYOUT=lvm DEPLOY_VG=vg_sys DEPLOY_SWAP=8G"},
		// Injection attempts and junk are dropped, not quoted-through:
		// these strings land inside a shell script.
		{"injection vg", `{"restore_layout":"lvm","restore_vg":"vg0; rm -rf /"}`,
			"export DEPLOY_LAYOUT=lvm"},
		{"injection swap", `{"restore_layout":"lvm","restore_swap":"$(reboot)"}`,
			"export DEPLOY_LAYOUT=lvm"},
		{"unknown layout falls back", `{"restore_layout":"zfs"}`, "export DEPLOY_LAYOUT=plain"},
	}
	for _, tc := range cases {
		var vars []byte
		if tc.vars != "" {
			vars = []byte(tc.vars)
		}
		if got := restoreLayoutEnv(vars); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
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

package main

import (
	"strings"
	"testing"
)

func sampleData() *machineProfileResp {
	d := &machineProfileResp{}
	d.Machine.ID = "0b1e0e5e-0000-0000-0000-000000000001"
	d.Machine.AssetTag = "LAB-01"
	d.Profile.Name = "Ubuntu 24.04 base"
	d.Image.OSFamily = "linux"
	d.Image.KernelURL = "https://deploy.example.com/static/linux-24.04/vmlinuz?a=1&b=2"
	d.Image.InitrdURL = "https://deploy.example.com/static/linux-24.04/initrd"
	d.Image.WimbootURL = "https://deploy.example.com/static/wimboot/wimboot"
	d.Image.BootWimURL = "https://deploy.example.com/static/winpe/boot.wim"
	d.OneShotToken = "tok_0123456789abcdef0123456789abcdef"
	d.JobID = "9c0f13c0-0000-0000-0000-000000000002"
	return d
}

func render(t *testing.T, tplName string) string {
	t.Helper()
	data := sampleData()
	var sb strings.Builder
	tplData := struct {
		*machineProfileResp
		DeployFQDN   string
		UserDataBase string
	}{data, "deploy.example.com", "https://deploy.example.com"}
	tpl := linuxTpl
	if tplName == "windows" {
		tpl = windowsTpl
	}
	if err := tpl.Execute(&sb, tplData); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return sb.String()
}

// iPXE scripts must not contain HTML-entity escaping (& → &amp; breaks
// kernel args) or backslash line continuations (not iPXE syntax).
func TestIPXETemplatesAreBootable(t *testing.T) {
	for _, name := range []string{"linux", "windows"} {
		out := render(t, name)
		if strings.Contains(out, "&amp;") {
			t.Fatalf("%s: HTML-escaped ampersand:\n%s", name, out)
		}
		if strings.Contains(out, "\\\n") {
			t.Fatalf("%s: backslash line continuation:\n%s", name, out)
		}
		if !strings.HasPrefix(out, "#!ipxe\n") {
			t.Fatalf("%s: missing shebang:\n%s", name, out)
		}
	}
}

// The Linux nocloud datasource must be keyed by the one-shot token,
// never the machine UUID (SECURITY.md §4 #1).
func TestLinuxTemplateUsesTokenDatasource(t *testing.T) {
	out := render(t, "linux")
	want := "s=https://deploy.example.com/boot/tok_0123456789abcdef0123456789abcdef/"
	if !strings.Contains(out, want) {
		t.Fatalf("nocloud URL not token-based:\n%s", out)
	}
	if strings.Contains(out, "/boot/0b1e0e5e") {
		t.Fatalf("machine UUID leaked into boot URL:\n%s", out)
	}
}

// A chain_url must hand the whole boot to the chained script: no kernel
// or initrd lines (they would be empty or point at /static 404s), and
// still a valid iPXE script.
func TestLinuxTemplateChainURL(t *testing.T) {
	data := sampleData()
	data.Image.ChainURL = "https://boot.netboot.xyz/menu.ipxe"
	data.Image.KernelURL = ""
	data.Image.InitrdURL = ""
	var sb strings.Builder
	tplData := struct {
		*machineProfileResp
		DeployFQDN   string
		UserDataBase string
	}{data, "deploy.example.com", "https://deploy.example.com"}
	if err := linuxTpl.Execute(&sb, tplData); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "chain https://boot.netboot.xyz/menu.ipxe") {
		t.Fatalf("missing chain line:\n%s", out)
	}
	for _, forbidden := range []string{"kernel ", "initrd "} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("chain script must not contain %q:\n%s", forbidden, out)
		}
	}
	if !strings.HasPrefix(out, "#!ipxe\n") {
		t.Fatalf("missing shebang:\n%s", out)
	}
}

// kernel_args must be appended to the kernel line — the field was saved
// by the UI and silently ignored here for a long time.
func TestLinuxTemplateAppendsKernelArgs(t *testing.T) {
	data := sampleData()
	data.Image.KernelArgs = "console=ttyS0 quiet"
	var sb strings.Builder
	tplData := struct {
		*machineProfileResp
		DeployFQDN   string
		UserDataBase string
	}{data, "deploy.example.com", "https://deploy.example.com"}
	if err := linuxTpl.Execute(&sb, tplData); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(sb.String(), " console=ttyS0 quiet\n") {
		t.Fatalf("kernel_args not appended:\n%s", sb.String())
	}
}

// The Windows path must hand WinPE both the job id and the bearer token.
func TestWindowsTemplateInjectsToken(t *testing.T) {
	out := render(t, "windows")
	for _, want := range []string{
		"_DEPLOY_TOKEN=tok_0123456789abcdef0123456789abcdef",
		"_DEPLOY_JOB_ID=9c0f13c0-0000-0000-0000-000000000002",
		"_DEPLOY_API=https://deploy.example.com",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

// The installer hop must be switchable to plain HTTP: an OS installer's
// initrd trusts only public CAs, so an internal-CA deploy server is
// unreachable over TLS from cloud-init.
func TestUserDataBaseHonoursHTTPPort(t *testing.T) {
	t.Setenv("NOCLOUD_HTTP_PORT", "")
	if got := userDataBase("deploy.example.com"); got != "https://deploy.example.com" {
		t.Fatalf("default must stay HTTPS, got %q", got)
	}
	t.Setenv("NOCLOUD_HTTP_PORT", "8080")
	if got := userDataBase("deploy.example.com"); got != "http://deploy.example.com:8080" {
		t.Fatalf("http override: %q", got)
	}
}

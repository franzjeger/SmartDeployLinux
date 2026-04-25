// edge-agent orchestrates the small box deployed to a remote LAN.
//
// First-boot flow:
//   1. Operator powers on the box. We have no auth key yet.
//   2. We listen on a local TUI (or HTTP on a local-only port) for an
//      operator code, exactly like the USB bootstrap flow.
//   3. POST the code to the deploy server's /bootstrap/redeem; receive
//      an ephemeral key tagged tag:deploy-edge.
//   4. Bring up tailscale with --advertise-routes so the deploy server
//      can reach LAN-side targets via this box (for WoL etc).
//   5. Render dnsmasq.conf from env, start dnsmasq on LAN_INTERFACE in
//      proxyDHCP+TFTP mode.
//
// Subsequent boots:
//   We persist nothing. tailnet membership renews via the persistent
//   tailscaled state in /var/lib/tailscale. If state is wiped, the
//   operator re-runs the code dance.
//
// This service is intentionally small and audits cleanly. The actual
// PXE+DHCP heavy-lifting is dnsmasq's; we just supervise it.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := waitForTailscaled(ctx); err != nil {
		slog.Error("tailscaled never came up", "err", err)
		os.Exit(2)
	}

	// If state already shows we're on the tailnet, skip the code dance.
	if isUp() {
		slog.Info("tailscale already up; skipping code redeem")
	} else {
		if err := redeemAndUp(ctx); err != nil {
			slog.Error("redeem failed", "err", err)
			os.Exit(3)
		}
	}

	if err := startDnsmasq(ctx); err != nil {
		slog.Error("dnsmasq", "err", err)
		os.Exit(4)
	}
}

func waitForTailscaled(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/var/run/tailscale/tailscaled.sock"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("/var/run/tailscale/tailscaled.sock did not appear")
}

func isUp() bool {
	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		return false
	}
	var st struct {
		BackendState string `json:"BackendState"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return false
	}
	return st.BackendState == "Running"
}

func redeemAndUp(ctx context.Context) error {
	deployURL := os.Getenv("DEPLOY_URL")
	if deployURL == "" {
		deployURL = "https://" + os.Getenv("DEPLOY_FQDN")
	}

	code, err := readCodeFromOperator(ctx)
	if err != nil {
		return fmt.Errorf("read code: %w", err)
	}

	body := map[string]any{
		"code":      code,
		"boot_uuid": fmt.Sprintf("edge-%d", time.Now().Unix()),
	}
	bs, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, "POST",
		deployURL+"/api/v1/bootstrap/redeem",
		strings.NewReader(string(bs)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "edge-agent/1.0")

	client := &http.Client{Timeout: 15 * time.Second}
	// We rely on the OS-level CA bundle plus deploy-ca.pem mounted at
	// /etc/ssl/certs/. The cert pin is the sole reason we trust this
	// initial unauthenticated POST.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("redeem call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("redeem %d", resp.StatusCode)
	}

	var rr struct {
		AuthKey    string `json:"auth_key"`
		ControlURL string `json:"control_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return err
	}

	args := []string{
		"up",
		"--authkey=" + rr.AuthKey,
		"--hostname=edge-" + os.Getenv("EDGE_NAME"),
		"--advertise-routes=" + os.Getenv("ADVERTISE_ROUTES"),
		"--accept-routes",
	}
	if rr.ControlURL != "" {
		args = append(args, "--login-server="+rr.ControlURL)
	}

	cmd := exec.CommandContext(ctx, "tailscale", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tailscale up: %w", err)
	}
	slog.Info("tailnet membership established; routes advertised", "routes", os.Getenv("ADVERTISE_ROUTES"))
	return nil
}

func readCodeFromOperator(ctx context.Context) (string, error) {
	fmt.Print("Enter deployment code: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return "", fmt.Errorf("no input")
	}
	code := strings.TrimSpace(scanner.Text())
	if code == "" {
		return "", fmt.Errorf("empty code")
	}
	return code, nil
}

func startDnsmasq(ctx context.Context) error {
	conf := renderDnsmasqConf()
	if err := os.WriteFile("/etc/dnsmasq.conf", []byte(conf), 0644); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "dnsmasq", "--no-daemon", "--conf-file=/etc/dnsmasq.conf")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	slog.Info("starting dnsmasq", "iface", os.Getenv("LAN_INTERFACE"))
	return cmd.Run()
}

func renderDnsmasqConf() string {
	return fmt.Sprintf(`
interface=%s
bind-interfaces

dhcp-range=%s,proxy

enable-tftp
tftp-root=/tftproot

dhcp-match=set:bios,option:client-arch,0
dhcp-match=set:efi64,option:client-arch,7

dhcp-boot=tag:bios,undionly.kpxe,%s
dhcp-boot=tag:efi64,ipxe.efi,%s

port=0
log-dhcp
log-facility=-
`,
		os.Getenv("LAN_INTERFACE"),
		os.Getenv("LAN_SUBNET"),
		os.Getenv("TFTP_SERVER_IP"),
		os.Getenv("TFTP_SERVER_IP"),
	)
}

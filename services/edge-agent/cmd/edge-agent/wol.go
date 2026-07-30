// Wake-on-LAN: drain the deploy server's wake queue for this site and
// broadcast magic packets on the LAN this box fronts.
//
// Enabled when EDGE_WAKE_TOKEN is set (must match the API's env). The
// poller runs alongside dnsmasq; a failed poll logs and retries — the
// queue is durable server-side, so nothing is lost across restarts.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// buildMagicPacket returns the WoL frame payload for a MAC address:
// 6 bytes of 0xFF followed by the MAC repeated 16 times.
func buildMagicPacket(mac string) ([]byte, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil {
		return nil, fmt.Errorf("bad MAC %q: %w", mac, err)
	}
	if len(hw) != 6 {
		return nil, fmt.Errorf("MAC %q is not 48-bit", mac)
	}
	pkt := make([]byte, 0, 102)
	pkt = append(pkt, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF)
	for i := 0; i < 16; i++ {
		pkt = append(pkt, hw...)
	}
	return pkt, nil
}

// sendMagicPacket broadcasts the packet on UDP port 9 (discard). The
// broadcast address defaults to the limited broadcast; set
// LAN_BROADCAST to the subnet's directed broadcast when the box has
// multiple interfaces.
func sendMagicPacket(mac, broadcast string) error {
	pkt, err := buildMagicPacket(mac)
	if err != nil {
		return err
	}
	if broadcast == "" {
		broadcast = "255.255.255.255"
	}
	conn, err := net.Dial("udp", net.JoinHostPort(broadcast, "9"))
	if err != nil {
		return fmt.Errorf("dial broadcast: %w", err)
	}
	defer conn.Close()
	// Send a small burst — WoL frames are fire-and-forget and NIC
	// firmware occasionally misses a lone packet during link flap.
	for i := 0; i < 3; i++ {
		if _, err := conn.Write(pkt); err != nil {
			return fmt.Errorf("send: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// runWakePoller polls the deploy server's wake queue until ctx is done.
func runWakePoller(ctx context.Context) {
	token := os.Getenv("EDGE_WAKE_TOKEN")
	if token == "" {
		slog.Info("EDGE_WAKE_TOKEN not set; wake-on-LAN poller disabled")
		return
	}
	deployURL := os.Getenv("DEPLOY_URL")
	if deployURL == "" {
		deployURL = "https://" + os.Getenv("DEPLOY_FQDN")
	}
	site := os.Getenv("EDGE_SITE")
	if site == "" {
		site = "default"
	}
	agent := os.Getenv("EDGE_NAME")
	broadcast := os.Getenv("LAN_BROADCAST")

	url := fmt.Sprintf("%s/v1/edge/wake-queue?site=%s&agent=edge-%s", deployURL, site, agent)
	client := &http.Client{Timeout: 10 * time.Second}
	slog.Info("wake-on-LAN poller started", "site", site)

	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			slog.Warn("wake-queue poll", "err", err)
			continue
		}
		var out struct {
			Wakes []struct {
				WakeID    string `json:"wake_id"`
				MachineID string `json:"machine_id"`
				MAC       string `json:"mac"`
			} `json:"wakes"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || err != nil {
			slog.Warn("wake-queue poll", "status", resp.StatusCode, "err", err)
			continue
		}
		for _, wk := range out.Wakes {
			if err := sendMagicPacket(wk.MAC, broadcast); err != nil {
				slog.Error("wake send", "mac", wk.MAC, "err", err)
				continue
			}
			slog.Info("magic packet sent", "mac", wk.MAC, "machine", wk.MachineID, "wake", wk.WakeID)
		}
	}
}

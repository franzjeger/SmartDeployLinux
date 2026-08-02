// render is the per-machine iPXE script + answer-file renderer.
// nginx fronts it for static blob delivery; render handles the dynamic
// per-machine endpoints by calling the API for machine + profile data
// and templating the result.
//
// This binary trusts the API. mTLS between render and API is enforced
// via the `api` service binding; render is on the same docker network.

package main

import (
	"encoding/json"
	"fmt"
	"text/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/your-org/deployserver/http-boot/internal/mtls"
)

func main() {
	apiURL := getenv("API_INTERNAL_URL", "https://api:8443")
	listen := getenv("RENDER_LISTEN", ":8444")

	httpClient := &http.Client{Timeout: 5 * time.Second}
	caPath := getenv("INTERNAL_CA_CERT_PATH", "/secrets/internal-ca.pem")
	certPath := getenv("INTERNAL_TLS_CERT", "/secrets/http-boot.pem")
	keyPath := getenv("INTERNAL_TLS_KEY", "/secrets/http-boot-key.pem")
	if _, err := os.Stat(certPath); err == nil {
		bundle, err := mtls.Load(caPath, certPath, keyPath)
		if err != nil {
			slog.Error("mtls load", "err", err)
			os.Exit(2)
		}
		httpClient.Transport = &http.Transport{
			TLSClientConfig: bundle.ClientConfig(),
		}
		slog.Info("mTLS to api enabled", "api", apiURL)
	} else {
		slog.Warn("INTERNAL_TLS_CERT not present; calling api in plaintext (dev-mode)")
	}

	r := chi.NewRouter()
	h := &handlers{apiURL: apiURL, http: httpClient}

	r.Get("/render/by-token/{token}", h.renderIPXEByToken)
	r.Get("/render/by-token/{token}/user-data", h.renderUserDataByToken)
	r.Get("/render/by-token/{token}/meta-data", h.renderMetaDataByToken)
	r.Get("/render/by-token/{token}/vendor-data", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	})
	// Interactive PXE menu (Phase 23): pure pass-through to the api.
	r.Get("/render/menu/by-mac/{mac}", h.proxyMenu("/internal/render/menu/by-mac/"))
	r.Get("/render/menu/deploy/{mac}/{profile}", h.proxyMenuDeploy)

	// Legacy:
	r.Get("/render/by-id/{id}", h.renderIPXEByID)
	r.Get("/render/by-mac/{mac}", h.renderIPXEByMAC)
	r.Get("/render/{id}/user-data", h.renderUserData)
	r.Get("/render/{id}/meta-data", h.renderMetaData)
	r.Get("/render/{id}/vendor-data", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	})
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	slog.Info("render listening", "addr", listen, "api", apiURL)
	if err := http.ListenAndServe(listen, r); err != nil {
		slog.Error("listen", "err", err)
		os.Exit(1)
	}
}

type handlers struct {
	apiURL string
	http   *http.Client
}

type machineProfileResp struct {
	Machine struct {
		ID          string `json:"id"`
		AssetTag    string `json:"asset_tag"`
		MAC         string `json:"mac_primary"`
	} `json:"machine"`
	Profile struct {
		ID         string                 `json:"id"`
		Name       string                 `json:"name"`
		Vars       map[string]interface{} `json:"vars"`
	} `json:"profile"`
	Image struct {
		OSFamily   string `json:"os_family"`
		OSVersion  string `json:"os_version"`
		Arch       string `json:"arch"`
		ChainURL   string `json:"chain_url,omitempty"`
		KernelURL  string `json:"kernel_url,omitempty"`
		InitrdURL  string `json:"initrd_url,omitempty"`
		KernelArgs string `json:"kernel_args,omitempty"`
		WimbootURL string `json:"wimboot_url,omitempty"`
		BootWimURL string `json:"bootwim_url,omitempty"`
		WimURL     string `json:"wim_url,omitempty"`
	} `json:"image"`
	OneShotToken string `json:"one_shot_token,omitempty"`
	JobID        string `json:"job_id,omitempty"`
}

func (h *handlers) fetch(path string) (*machineProfileResp, error) {
	resp, err := h.http.Get(h.apiURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("api %d: %s", resp.StatusCode, string(b))
	}
	var out machineProfileResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *handlers) renderIPXEByToken(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	data, err := h.fetch("/internal/render/by-token/" + tok)
	if err != nil {
		slog.ErrorContext(r.Context(), "fetch by token", "err", err)
		http.Error(w, "token invalid or consumed", 410)
		return
	}
	h.writeIPXE(w, r, data)
}

func (h *handlers) renderUserDataByToken(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	resp, err := h.http.Get(h.apiURL + "/internal/render/by-token/" + tok + "/user-data")
	if err != nil || resp.StatusCode != 200 {
		http.Error(w, "user-data unavailable", 502)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "text/cloud-config")
	_, _ = io.Copy(w, resp.Body)
}

func (h *handlers) renderMetaDataByToken(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	resp, err := h.http.Get(h.apiURL + "/internal/render/by-token/" + tok + "/meta-data")
	if err != nil || resp.StatusCode != 200 {
		http.Error(w, "meta-data unavailable", 502)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = io.Copy(w, resp.Body)
}

func (h *handlers) renderIPXEByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	data, err := h.fetch("/internal/render/machine/" + id)
	if err != nil {
		slog.ErrorContext(r.Context(), "fetch machine", "id", id, "err", err)
		http.Error(w, "machine not found", 404)
		return
	}
	h.writeIPXE(w, r, data)
}

func (h *handlers) renderIPXEByMAC(w http.ResponseWriter, r *http.Request) {
	mac := chi.URLParam(r, "mac")
	data, err := h.fetch("/internal/render/by-mac/" + mac)
	if err != nil {
		slog.ErrorContext(r.Context(), "fetch by mac", "mac", mac, "err", err)
		http.Error(w, "machine not found", 404)
		return
	}
	h.writeIPXE(w, r, data)
}

// NOTE: iPXE has no backslash line-continuation syntax — every command
// must be a single line — and these are text/template (NOT html/template)
// so URLs with & are not entity-escaped into non-bootable kernel args.
//
// The nocloud datasource URL uses the one-shot boot token, matching
// nginx's /boot/<token>/user-data route and the API's token-gated
// render endpoints. The machine UUID is intentionally never used in
// boot URLs (SECURITY.md §4 #1).
// Two Linux shapes. chain_url (netboot.xyz-style menus, live ISOs,
// rolling distros without split kernel+initrd artifacts) hands the whole
// boot over to the chained script. kernel+initrd is the classic
// autoinstall path; media.kernel_args is appended last so an operator
// can extend the cmdline — previously that field was saved by the UI and
// then silently ignored here.
var linuxTpl = template.Must(template.New("linux").Parse(`#!ipxe
echo Deploying {{.Profile.Name}} to {{.Machine.AssetTag}} ({{.Machine.ID}})
{{if .Image.ChainURL -}}
chain {{.Image.ChainURL}}
{{- else -}}
kernel {{.Image.KernelURL}} initrd=initrd autoinstall ip=dhcp "ds=nocloud-net;s=https://{{.DeployFQDN}}/boot/{{.OneShotToken}}/"{{if .Image.KernelArgs}} {{.Image.KernelArgs}}{{end}}
initrd {{.Image.InitrdURL}}
boot
{{- end}}
`))

var windowsTpl = template.Must(template.New("windows").Parse(`#!ipxe
echo Deploying Windows ({{.Profile.Name}}) to {{.Machine.AssetTag}}
kernel {{.Image.WimbootURL}}
initrd --name boot.wim {{.Image.BootWimURL}}
imgargs wimboot _DEPLOY_JOB_ID={{.JobID}} _DEPLOY_TOKEN={{.OneShotToken}} _DEPLOY_API=https://{{.DeployFQDN}}
boot
`))

func (h *handlers) writeIPXE(w http.ResponseWriter, _ *http.Request, data *machineProfileResp) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	tplData := struct {
		*machineProfileResp
		DeployFQDN string
	}{data, getenv("DEPLOY_FQDN", "deploy.example.com")}

	switch strings.ToLower(data.Image.OSFamily) {
	case "linux":
		_ = linuxTpl.Execute(w, tplData)
	case "windows":
		_ = windowsTpl.Execute(w, tplData)
	default:
		http.Error(w, "unknown os_family: "+data.Image.OSFamily, 500)
	}
}

// proxyMenu passes menu requests through to the api unchanged. On
// upstream failure it emits an iPXE-safe fallback that degrades to the
// legacy by-mac boot, so a dead api behaves like pre-menu deployments.
func (h *handlers) proxyMenu(apiPrefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mac := chi.URLParam(r, "mac")
		resp, err := h.http.Get(h.apiURL + apiPrefix + mac)
		if err != nil || resp.StatusCode != 200 {
			if resp != nil {
				resp.Body.Close()
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintf(w, "#!ipxe\necho menu unavailable, falling back to assigned boot\nchain https://%s/boot/by-mac/%s.ipxe || shell\n",
				getenv("DEPLOY_FQDN", "deploy.example.com"), mac)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.Copy(w, resp.Body)
	}
}

func (h *handlers) proxyMenuDeploy(w http.ResponseWriter, r *http.Request) {
	mac := chi.URLParam(r, "mac")
	profile := chi.URLParam(r, "profile")
	resp, err := h.http.Get(h.apiURL + "/internal/render/menu/deploy/" + mac + "/" + profile)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "#!ipxe\necho deploy endpoint unavailable\nsleep 3\nexit 1\n")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (h *handlers) renderUserData(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	resp, err := h.http.Get(h.apiURL + "/internal/render/" + id + "/user-data")
	if err != nil || resp.StatusCode != 200 {
		http.Error(w, "user-data unavailable", 502)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "text/cloud-config")
	_, _ = io.Copy(w, resp.Body)
}

func (h *handlers) renderMetaData(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	resp, err := h.http.Get(h.apiURL + "/internal/render/" + id + "/meta-data")
	if err != nil || resp.StatusCode != 200 {
		http.Error(w, "meta-data unavailable", 502)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = io.Copy(w, resp.Body)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// deployserver UI — vanilla JS, no framework, no build step.
// Architecture: hash-router, view modules, API client, modal+toast helpers.

// ---------- helpers ----------

const $  = (q, root = document) => root.querySelector(q);
const $$ = (q, root = document) => Array.from(root.querySelectorAll(q));

const escapeHTML = s => {
  if (s === undefined || s === null) return "";
  return String(s).replace(/[&<>"']/g, c => ({
    "&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"
  }[c]));
};
const trunc = (s, n = 8) => {
  if (!s) return "—";
  s = String(s);
  return s.length > n ? s.slice(0, n) + "…" : s;
};
const fmtTime = iso => {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "—";
  const now = Date.now();
  const diff = now - d.getTime();
  const sec = Math.round(diff / 1000);
  if (sec < 60) return sec + "s ago";
  if (sec < 3600) return Math.round(sec / 60) + "m ago";
  if (sec < 86400) return Math.round(sec / 3600) + "h ago";
  return d.toLocaleDateString() + " " + d.toLocaleTimeString([], {hour:"2-digit", minute:"2-digit"});
};
const fmtAbsolute = iso => {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleString();
};

const stateBadge = state => {
  const cfg = {
    "pending":      ["info", "pending"],
    "bootstrapped": ["info", "bootstrapped"],
    "imaging":      ["warn", "imaging"],
    "post_install": ["warn", "post-install"],
    "completed":    ["ok",   "completed"],
    "failed":       ["err",  "failed"],
    "cancelled":    ["",     "cancelled"],
  }[state] || ["", state || "—"];
  const live = ["pending","bootstrapped","imaging","post_install"].includes(state);
  return `<span class="badge ${cfg[0]} ${live ? "pulsing" : ""}"><span class="dot"></span>${cfg[1]}</span>`;
};

// ---------- streaming SHA-256 ----------
//
// crypto.subtle.digest needs the whole payload in memory, which rules it
// out for multi-GB install.wim uploads. This is a compact incremental
// SHA-256 (FIPS 180-4) fed 8 MiB file slices at a time.

class SHA256 {
  constructor() {
    this.h = new Uint32Array([0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19]);
    this.buf = new Uint8Array(64);
    this.bufLen = 0;
    this.bytes = 0;
  }
  static K = new Uint32Array([
    0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,
    0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,
    0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,
    0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,
    0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,
    0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,
    0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,
    0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2]);
  _block(p, off) {
    const w = new Uint32Array(64), h = this.h, K = SHA256.K;
    for (let i = 0; i < 16; i++) {
      w[i] = (p[off]<<24)|(p[off+1]<<16)|(p[off+2]<<8)|p[off+3]; off += 4;
    }
    for (let i = 16; i < 64; i++) {
      const a = w[i-15], b = w[i-2];
      const s0 = ((a>>>7)|(a<<25)) ^ ((a>>>18)|(a<<14)) ^ (a>>>3);
      const s1 = ((b>>>17)|(b<<15)) ^ ((b>>>19)|(b<<13)) ^ (b>>>10);
      w[i] = (w[i-16] + s0 + w[i-7] + s1) >>> 0;
    }
    let [a,b,c,d,e,f,g,hh] = h;
    for (let i = 0; i < 64; i++) {
      const S1 = ((e>>>6)|(e<<26)) ^ ((e>>>11)|(e<<21)) ^ ((e>>>25)|(e<<7));
      const ch = (e & f) ^ (~e & g);
      const t1 = (hh + S1 + ch + K[i] + w[i]) >>> 0;
      const S0 = ((a>>>2)|(a<<30)) ^ ((a>>>13)|(a<<19)) ^ ((a>>>22)|(a<<10));
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const t2 = (S0 + maj) >>> 0;
      hh = g; g = f; f = e; e = (d + t1) >>> 0;
      d = c; c = b; b = a; a = (t1 + t2) >>> 0;
    }
    h[0]=(h[0]+a)>>>0; h[1]=(h[1]+b)>>>0; h[2]=(h[2]+c)>>>0; h[3]=(h[3]+d)>>>0;
    h[4]=(h[4]+e)>>>0; h[5]=(h[5]+f)>>>0; h[6]=(h[6]+g)>>>0; h[7]=(h[7]+hh)>>>0;
  }
  update(chunk) {
    this.bytes += chunk.length;
    let off = 0;
    if (this.bufLen) {
      const need = Math.min(64 - this.bufLen, chunk.length);
      this.buf.set(chunk.subarray(0, need), this.bufLen);
      this.bufLen += need; off = need;
      if (this.bufLen === 64) { this._block(this.buf, 0); this.bufLen = 0; }
    }
    while (off + 64 <= chunk.length) { this._block(chunk, off); off += 64; }
    if (off < chunk.length) {
      this.buf.set(chunk.subarray(off), 0);
      this.bufLen = chunk.length - off;
    }
  }
  hex() {
    const padLen = (this.bufLen < 56 ? 56 : 120) - this.bufLen;
    const pad = new Uint8Array(padLen + 8);
    pad[0] = 0x80;
    const bits = this.bytes * 8;
    // 64-bit big-endian length (files < 2^53 bytes; high word via /2^32).
    const hi = Math.floor(bits / 4294967296), lo = bits >>> 0;
    pad[padLen]   = hi >>> 24; pad[padLen+1] = hi >>> 16;
    pad[padLen+2] = hi >>> 8;  pad[padLen+3] = hi;
    pad[padLen+4] = lo >>> 24; pad[padLen+5] = lo >>> 16;
    pad[padLen+6] = lo >>> 8;  pad[padLen+7] = lo;
    this.update(pad);
    return Array.from(this.h, x => x.toString(16).padStart(8, "0")).join("");
  }
}

async function sha256File(file, onProgress) {
  const hasher = new SHA256();
  const CHUNK = 8 * 1024 * 1024;
  for (let off = 0; off < file.size; off += CHUNK) {
    const buf = await file.slice(off, off + CHUNK).arrayBuffer();
    hasher.update(new Uint8Array(buf));
    if (onProgress) onProgress(Math.min(off + CHUNK, file.size) / file.size);
  }
  return hasher.hex();
}

// ---------- auth (OIDC authorization-code + PKCE) ----------
//
// Pre-auth, the SPA asks /api/v1/auth/config for the issuer + client_id.
// In dev mode (no OIDC configured server-side) everything is open and
// none of this runs. Otherwise: standard code+PKCE against the issuer,
// ID token kept in sessionStorage, sent as the bearer on every call.

const TOKEN_KEY = "deploy_id_token";
let authCfg = null; // {issuer, client_id, dev_mode}

const idToken = () => sessionStorage.getItem(TOKEN_KEY) || "";

async function loadAuthConfig() {
  if (authCfg) return authCfg;
  const r = await fetch("/api/v1/auth/config");
  authCfg = r.ok ? await r.json() : { dev_mode: true };
  return authCfg;
}

async function oidcMeta() {
  const cfg = await loadAuthConfig();
  const r = await fetch(cfg.issuer.replace(/\/$/, "") + "/.well-known/openid-configuration");
  if (!r.ok) throw new Error("OIDC discovery failed: HTTP " + r.status);
  return r.json();
}

const b64url = bytes =>
  btoa(String.fromCharCode(...new Uint8Array(bytes)))
    .replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");

function randomString(len = 64) {
  const bytes = new Uint8Array(len);
  crypto.getRandomValues(bytes);
  return b64url(bytes).slice(0, len);
}

async function startLogin() {
  const cfg = await loadAuthConfig();
  if (cfg.dev_mode) return;
  const meta = await oidcMeta();
  const verifier = randomString(64);
  const state = randomString(32);
  sessionStorage.setItem("pkce_verifier", verifier);
  sessionStorage.setItem("oauth_state", state);
  const challenge = b64url(await crypto.subtle.digest("SHA-256",
    new TextEncoder().encode(verifier)));
  const q = new URLSearchParams({
    response_type: "code",
    client_id: cfg.client_id,
    redirect_uri: location.origin + "/",
    scope: "openid email profile",
    state,
    code_challenge: challenge,
    code_challenge_method: "S256",
  });
  location.assign(meta.authorization_endpoint + "?" + q);
}

// completeLogin handles the redirect back from the IdP (?code=...).
// Returns true when it consumed a code (the URL is cleaned either way).
async function completeLogin() {
  const params = new URLSearchParams(location.search);
  const code = params.get("code");
  if (!code) return false;
  const cleanURL = location.origin + location.pathname + location.hash;
  try {
    if (params.get("state") !== sessionStorage.getItem("oauth_state")) {
      throw new Error("state mismatch");
    }
    const cfg = await loadAuthConfig();
    const meta = await oidcMeta();
    const r = await fetch(meta.token_endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        grant_type: "authorization_code",
        code,
        client_id: cfg.client_id,
        redirect_uri: location.origin + "/",
        code_verifier: sessionStorage.getItem("pkce_verifier") || "",
      }),
    });
    const tok = await r.json();
    if (!r.ok || !tok.id_token) {
      throw new Error(tok.error_description || tok.error || "token exchange failed");
    }
    sessionStorage.setItem(TOKEN_KEY, tok.id_token);
    // Mirror into a cookie for EventSource (which can't set headers).
    // Read only by the SSE route's cookie fallback on the server.
    document.cookie = "deploy_session=" + tok.id_token +
      "; path=/; SameSite=Strict" + (location.protocol === "https:" ? "; Secure" : "");
  } catch (e) {
    toast("Login failed: " + e.message, "err");
  } finally {
    sessionStorage.removeItem("pkce_verifier");
    sessionStorage.removeItem("oauth_state");
    history.replaceState(null, "", cleanURL);
  }
  return true;
}

function logout() {
  sessionStorage.removeItem(TOKEN_KEY);
  document.cookie = "deploy_session=; path=/; Max-Age=0";
  location.reload();
}

// ---------- API client ----------

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const tok = idToken();
  if (tok) opts.headers["Authorization"] = "Bearer " + tok;
  const r = await fetch(path, opts);
  if (r.status === 401 && authCfg && !authCfg.dev_mode) {
    // Expired or missing session: clear and restart the login flow.
    sessionStorage.removeItem(TOKEN_KEY);
    await startLogin();
    throw new Error("session expired; redirecting to login");
  }
  let data = null;
  if (r.status !== 204) {
    const text = await r.text();
    try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  }
  if (!r.ok) {
    const msg = (data && data.error) || data || `HTTP ${r.status}`;
    throw new Error(typeof msg === "string" ? msg : JSON.stringify(msg));
  }
  return data;
}

// ---------- toast ----------

function toast(message, kind = "") {
  const el = document.createElement("div");
  el.className = "toast " + kind;
  el.textContent = message;
  $("#toast-root").appendChild(el);
  setTimeout(() => {
    el.style.opacity = "0";
    el.style.transition = "opacity 0.2s";
    setTimeout(() => el.remove(), 220);
  }, 4500);
}

// ---------- modal ----------

function openModal({ title, body, primary, secondary, onPrimary, onClose }) {
  const root = $("#modal-root");
  const html = `
    <div class="modal" role="dialog" aria-modal="true">
      <div class="modal-header">
        <h2>${escapeHTML(title)}</h2>
        <button class="modal-close" aria-label="Close">×</button>
      </div>
      <div class="modal-body">${body}</div>
      <div class="modal-footer">
        ${secondary ? `<button class="btn secondary" data-act="secondary">${escapeHTML(secondary)}</button>` : ""}
        ${primary ? `<button class="btn ${primary.danger ? "danger" : ""}" data-act="primary">${escapeHTML(primary.label)}</button>` : ""}
      </div>
    </div>`;
  root.innerHTML = html;
  root.classList.add("active");
  const close = () => {
    root.classList.remove("active");
    root.innerHTML = "";
    onClose && onClose();
  };
  $(".modal-close", root).addEventListener("click", close);
  root.addEventListener("click", e => { if (e.target === root) close(); });
  if (primary) {
    $('[data-act="primary"]', root).addEventListener("click", async () => {
      try {
        const ok = await onPrimary?.($(".modal", root));
        if (ok !== false) close();
      } catch (e) { toast(e.message, "err"); }
    });
  }
  const sec = $('[data-act="secondary"]', root);
  if (sec) sec.addEventListener("click", close);
  return { close };
}

function confirmModal({ title, message, danger, primaryLabel = "Confirm" }) {
  return new Promise(resolve => {
    let result = false;
    openModal({
      title,
      body: `<p>${escapeHTML(message)}</p>`,
      secondary: "Cancel",
      primary: { label: primaryLabel, danger },
      onPrimary: () => { result = true; },
      onClose: () => resolve(result),
    });
  });
}

// ---------- router ----------

const routes = {};
function route(path, handler) { routes[path] = handler; }

function matchRoute(hash) {
  let path = (hash || "#/").slice(1) || "/";
  // strip query string for matching
  const qi = path.indexOf("?");
  if (qi >= 0) path = path.slice(0, qi);
  if (routes[path]) return { handler: routes[path], params: {} };
  for (const [pattern, handler] of Object.entries(routes)) {
    if (!pattern.includes(":")) continue;
    const re = new RegExp("^" + pattern.replace(/:([a-z]+)/g, "(?<$1>[^/]+)") + "$");
    const m = path.match(re);
    if (m) return { handler, params: m.groups || {} };
  }
  return null;
}

let currentCleanup = null;

async function navigate() {
  const hash = location.hash || "#/";
  const m = matchRoute(hash);
  const path = (hash.slice(1).split("?")[0]) || "/";
  $$(".nav-item").forEach(a => a.classList.toggle("active", a.dataset.route === path));
  if (!$$(".nav-item.active").length) {
    const seg = "/" + (path.split("/")[1] || "");
    $$(`.nav-item[data-route="${seg}"]`).forEach(a => a.classList.add("active"));
  }
  if (currentCleanup) { try { currentCleanup(); } catch {} currentCleanup = null; }
  $("#content").innerHTML = `<div class="page"><div class="empty"><div class="spinner"></div></div></div>`;
  if (!m) {
    $("#content").innerHTML = `<div class="page"><h1>404</h1><p class="muted">No route for <code>${escapeHTML(hash)}</code></p></div>`;
    return;
  }
  try {
    const result = await m.handler(m.params);
    if (typeof result === "function") currentCleanup = result;
  } catch (e) {
    $("#content").innerHTML = `<div class="page"><div class="banner err">${escapeHTML(e.message)}</div></div>`;
  }
}

window.addEventListener("hashchange", navigate);
window.addEventListener("DOMContentLoaded", boot);
$("#refresh-btn")?.addEventListener("click", navigate);

function setBreadcrumb(parts) {
  $("#breadcrumb").innerHTML = parts.map((p, i) => {
    const isLast = i === parts.length - 1;
    return p.href && !isLast
      ? `<a href="${escapeHTML(p.href)}">${escapeHTML(p.label)}</a>`
      : `<span class="${isLast ? "crumb-current" : ""}">${escapeHTML(p.label)}</span>`;
  }).join('<span class="sep">›</span>');
}

// ---------- views: Dashboard ----------

route("/", async () => {
  setBreadcrumb([{label: "Dashboard"}]);
  const [machines, jobs, audit, profiles, images] = await Promise.all([
    api("GET", "/api/v1/machines").catch(() => []),
    api("GET", "/api/v1/jobs?limit=10").catch(() => []),
    api("GET", "/api/v1/audit?since=24h&limit=10").catch(() => []),
    api("GET", "/api/v1/profiles").catch(() => []),
    api("GET", "/api/v1/images").catch(() => []),
  ]);

  const liveJobs = jobs.filter(j => ["pending","bootstrapped","imaging","post_install"].includes(j.state));
  const recentDone = jobs.filter(j => ["completed","failed","cancelled"].includes(j.state));

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div>
          <h1>Dashboard</h1>
          <p class="subtitle">Active deployments and recent activity.</p>
        </div>
        <div class="page-actions"><a class="btn" href="#/deploy">+ New deployment</a></div>
      </div>

      <div class="cards" style="margin-top: 16px;">
        <div class="card compact"><h3>Machines</h3><div class="stat">${machines.length}</div><div class="stat-sub">registered</div></div>
        <div class="card compact"><h3>Live deployments</h3><div class="stat">${liveJobs.length}</div><div class="stat-sub">in progress</div></div>
        <div class="card compact"><h3>Images</h3><div class="stat">${images.length}</div><div class="stat-sub">in library</div></div>
        <div class="card compact"><h3>Profiles</h3><div class="stat">${profiles.length}</div><div class="stat-sub">configured</div></div>
      </div>

      <div class="split" style="margin-top: 24px;">
        <div>
          <div class="section-title">Live deployments</div>
          ${liveJobs.length ? jobsTable(liveJobs) : `
            <div class="card"><div class="empty">
              <div class="empty-icon">▢</div>
              <div>No deployments running.</div>
              <a class="btn" href="#/deploy">Start one</a>
            </div></div>`}
          <div class="section-title">Recent deployments</div>
          ${recentDone.length ? jobsTable(recentDone) : `<div class="card"><div class="empty muted">No recent deployments.</div></div>`}
        </div>
        <div>
          <div class="section-title">Recent activity</div>
          <div class="card compact">
            ${audit.length ? `
              <table>
                <tbody>
                  ${audit.map(e => `
                    <tr class="no-hover">
                      <td>${stateBadgeForAction(e.action)}<span style="margin-left:8px;">${escapeHTML(e.action)}</span></td>
                      <td class="mono muted small" style="text-align:right;">${escapeHTML(fmtTime(e.at))}</td>
                    </tr>`).join("")}
                </tbody>
              </table>` : `<div class="empty muted">No recent activity.</div>`}
          </div>
          <p class="small muted" style="margin-top: 8px;"><a href="#/audit">View full audit log →</a></p>
        </div>
      </div>
    </div>`;

  if (liveJobs.length) {
    const t = setTimeout(navigate, 5000);
    return () => clearTimeout(t);
  }
});

function stateBadgeForAction(action) {
  if (!action) return "";
  if (action.endsWith(".redeem_failed") || action.endsWith(".failed")) return `<span class="badge err"><span class="dot"></span>fail</span>`;
  if (action.endsWith(".completed") || action.endsWith(".redeemed")) return `<span class="badge ok"><span class="dot"></span>ok</span>`;
  if (action.endsWith(".issued") || action.endsWith(".created")) return `<span class="badge info"><span class="dot"></span>+</span>`;
  if (action.endsWith(".deleted")) return `<span class="badge warn"><span class="dot"></span>-</span>`;
  return `<span class="badge"><span class="dot"></span>·</span>`;
}

function jobsTable(jobs) {
  return `<div class="table-wrap"><table>
    <thead><tr><th>Status</th><th>Machine</th><th>Profile</th><th>Started</th><th></th></tr></thead>
    <tbody>
      ${jobs.map(j => `
        <tr onclick="location.hash='#/jobs/${j.id}'">
          <td>${stateBadge(j.state)}</td>
          <td>${escapeHTML(j.machine_asset_tag || trunc(j.machine_id))}</td>
          <td>${escapeHTML(j.profile_name)}</td>
          <td class="mono small muted">${fmtTime(j.started_at || j.created_at)}</td>
          <td class="actions"><a class="btn small secondary" href="#/jobs/${j.id}">Open</a></td>
        </tr>`).join("")}
    </tbody>
  </table></div>`;
}

// ---------- views: Machines list ----------

let machinesState = { search: "" };

route("/machines", async () => {
  setBreadcrumb([{label: "Machines"}]);
  const [machines, profiles] = await Promise.all([
    api("GET", "/api/v1/machines"),
    api("GET", "/api/v1/profiles").catch(() => []),
  ]);
  const profById = Object.fromEntries(profiles.map(p => [p.id, p.name]));

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div><h1>Machines</h1><p class="subtitle">Registered targets. Click a row for detail.</p></div>
        <div class="page-actions"><button class="btn" id="register-btn">+ Register machine</button></div>
      </div>
      <div class="search">
        <input id="machine-search" placeholder="Search asset tag, MAC, vendor…" value="${escapeHTML(machinesState.search)}">
        <span class="muted small" id="machines-count"></span>
      </div>
      <div class="table-wrap" id="machines-table-wrap"></div>
    </div>`;

  const render = () => {
    const q = machinesState.search.toLowerCase();
    const filtered = machines.filter(m => {
      if (!q) return true;
      const hay = [m.AssetTag, m.MACPrimary, m.Vendor, m.Model, m.ID].filter(Boolean).join(" ").toLowerCase();
      return hay.includes(q);
    });
    $("#machines-count").textContent = `${filtered.length} of ${machines.length}`;
    $("#machines-table-wrap").innerHTML = filtered.length ? `
      <table>
        <thead><tr><th>Asset tag</th><th>MAC</th><th>Vendor / Model</th><th>Profile</th><th>Created</th><th></th></tr></thead>
        <tbody>
          ${filtered.map(m => `
            <tr onclick="location.hash='#/machines/${m.ID}'">
              <td><strong>${escapeHTML(m.AssetTag || "—")}</strong></td>
              <td class="mono">${escapeHTML(m.MACPrimary || "—")}</td>
              <td>${escapeHTML([m.Vendor, m.Model].filter(Boolean).join(" / ") || "—")}</td>
              <td>${escapeHTML(profById[m.DefaultProfileID] || "—")}</td>
              <td class="mono small muted">${fmtTime(m.CreatedAt)}</td>
              <td class="actions"><a class="btn small secondary" href="#/deploy?machine=${m.ID}">Deploy</a></td>
            </tr>`).join("")}
        </tbody>
      </table>` : `<div class="empty"><div class="empty-icon">▢</div>No machines match.</div>`;
  };
  render();
  $("#machine-search").addEventListener("input", e => { machinesState.search = e.target.value; render(); });
  $("#register-btn").addEventListener("click", () => openMachineCreateModal(profiles));
});

function openMachineCreateModal(profiles) {
  const profOptions = `<option value="">(none — assign at deploy time)</option>` +
    profiles.map(p => `<option value="${escapeHTML(p.id)}">${escapeHTML(p.name)}</option>`).join("");
  openModal({
    title: "Register machine",
    body: `
      <form id="machine-form">
        <div class="row">
          <label>Asset tag * <input name="asset_tag" required placeholder="lab-01"></label>
          <label>MAC address <input name="mac_primary" placeholder="aa:bb:cc:dd:ee:ff"></label>
        </div>
        <div class="row">
          <label>Vendor <input name="vendor" placeholder="Dell Inc."></label>
          <label>Model <input name="model" placeholder="Latitude 7440"></label>
        </div>
        <div class="row">
          <label class="full">Default profile
            <select name="default_profile_id">${profOptions}</select>
          </label>
        </div>
      </form>`,
    primary: { label: "Register" },
    secondary: "Cancel",
    onPrimary: async modal => {
      const fd = new FormData($("#machine-form", modal));
      const body = {};
      for (const [k, v] of fd) if (v) body[k] = v;
      await api("POST", "/api/v1/machines", body);
      toast(`Registered ${body.asset_tag}`, "ok");
      navigate();
    },
  });
}

// ---------- views: Machine detail ----------

route("/machines/:id", async ({ id }) => {
  setBreadcrumb([{label:"Machines", href:"#/machines"}, {label: id.slice(0,8)+"…"}]);
  const [m, jobs, profiles] = await Promise.all([
    api("GET", `/api/v1/machines/${id}`),
    api("GET", `/api/v1/jobs?machine=${id}`).catch(() => []),
    api("GET", "/api/v1/profiles").catch(() => []),
  ]);
  const profById = Object.fromEntries(profiles.map(p => [p.id, p.name]));

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div>
          <h1>${escapeHTML(m.AssetTag || trunc(m.ID))}</h1>
          <p class="subtitle mono small">${escapeHTML(m.ID)}</p>
        </div>
        <div class="page-actions">
          <a class="btn" href="#/deploy?machine=${id}">Deploy</a>
          <button class="btn danger" id="delete-machine">Delete</button>
        </div>
      </div>

      <div class="card" style="margin-top:16px;">
        <dl class="detail-grid">
          <dt>Asset tag</dt>      <dd>${escapeHTML(m.AssetTag || "—")}</dd>
          <dt>MAC (primary)</dt>  <dd class="mono">${escapeHTML(m.MACPrimary || "—")}</dd>
          <dt>SMBIOS UUID</dt>    <dd class="mono">${escapeHTML(m.UUIDSMBIOS || "—")}</dd>
          <dt>Vendor</dt>         <dd>${escapeHTML(m.Vendor || "—")}</dd>
          <dt>Model</dt>          <dd>${escapeHTML(m.Model || "—")}</dd>
          <dt>Default profile</dt><dd>${escapeHTML(profById[m.DefaultProfileID] || "—")}</dd>
          <dt>Registered</dt>     <dd class="mono">${fmtAbsolute(m.CreatedAt)}</dd>
        </dl>
      </div>

      <div class="section-title">Deployment history</div>
      ${jobs.length ? jobsTable(jobs) : `<div class="card"><div class="empty muted">No deployments yet for this machine.</div></div>`}
    </div>`;

  $("#delete-machine").addEventListener("click", async () => {
    const ok = await confirmModal({
      title: "Delete machine",
      message: `Delete ${m.AssetTag || trunc(m.ID)}? This is permanent.`,
      danger: true, primaryLabel: "Delete",
    });
    if (!ok) return;
    try {
      await api("DELETE", `/api/v1/machines/${id}`);
      toast("Machine deleted", "ok");
      location.hash = "#/machines";
    } catch (e) { toast(e.message, "err"); }
  });
});

// ---------- views: Deployment wizard ----------

route("/deploy", async () => {
  const params = new URLSearchParams(location.hash.split("?")[1] || "");
  const preselectMachine = params.get("machine") || "";

  setBreadcrumb([{label:"New deployment"}]);

  const [machines, profiles] = await Promise.all([
    api("GET", "/api/v1/machines"),
    api("GET", "/api/v1/profiles"),
  ]);

  if (!machines.length) {
    $("#content").innerHTML = `
      <div class="page">
        <div class="page-title"><h1>New deployment</h1></div>
        <div class="card"><div class="empty">
          <div class="empty-icon">▢</div>
          <div>Register at least one machine first.</div>
          <a class="btn" href="#/machines">Go to Machines</a>
        </div></div>
      </div>`;
    return;
  }

  const wizardState = {
    step: 1,
    machine_id: preselectMachine || machines[0].ID,
    profile_id: profiles[0]?.id || "",
    ttl_hours: 4,
    label: "",
  };

  const renderWizard = () => {
    $("#content").innerHTML = `
      <div class="page">
        <div class="page-title"><h1>New deployment</h1>
          <p class="subtitle">Issue a single-use code that the onsite operator types into the bootstrap stick.</p></div>

        <div class="wizard-steps">
          <div class="wizard-step ${wizardState.step >= 1 ? "active" : ""} ${wizardState.step > 1 ? "done" : ""}"><span class="step-num">1</span>Target</div>
          <div class="wizard-step ${wizardState.step >= 2 ? "active" : ""} ${wizardState.step > 2 ? "done" : ""}"><span class="step-num">2</span>Profile</div>
          <div class="wizard-step ${wizardState.step >= 3 ? "active" : ""} ${wizardState.step > 3 ? "done" : ""}"><span class="step-num">3</span>Options</div>
          <div class="wizard-step ${wizardState.step >= 4 ? "active" : ""}"><span class="step-num">4</span>Review</div>
        </div>

        <div class="wizard-pane card" id="wizard-pane">
          ${renderWizardStep(wizardState, machines, profiles)}
        </div>

        <div class="wizard-actions">
          <button class="btn secondary" id="wiz-prev" ${wizardState.step === 1 ? "disabled" : ""}>← Back</button>
          ${wizardState.step < 4
            ? `<button class="btn" id="wiz-next">Next →</button>`
            : `<button class="btn" id="wiz-issue">Issue code</button>`}
        </div>
      </div>`;

    $("#wiz-next")?.addEventListener("click", () => { wizardState.step++; renderWizard(); });
    $("#wiz-prev")?.addEventListener("click", () => { wizardState.step--; renderWizard(); });
    $("#wiz-issue")?.addEventListener("click", () => issueDeployment(wizardState));

    if (wizardState.step === 1) {
      $$("#machine-list .machine-row").forEach(row => {
        row.addEventListener("click", () => { wizardState.machine_id = row.dataset.id; renderWizard(); });
      });
    } else if (wizardState.step === 2) {
      $$("#profile-list .profile-row").forEach(row => {
        row.addEventListener("click", () => { wizardState.profile_id = row.dataset.id; renderWizard(); });
      });
    } else if (wizardState.step === 3) {
      $("#wiz-ttl")?.addEventListener("input", e => wizardState.ttl_hours = parseInt(e.target.value)||4);
      $("#wiz-label")?.addEventListener("input", e => wizardState.label = e.target.value);
    }
  };
  renderWizard();
});

function renderWizardStep(s, machines, profiles) {
  if (s.step === 1) {
    return `
      <p class="muted">Select the machine to deploy.</p>
      <div id="machine-list" class="table-wrap"><table>
        <thead><tr><th></th><th>Asset tag</th><th>MAC</th><th>Vendor / Model</th></tr></thead>
        <tbody>
        ${machines.map(m => `
          <tr class="machine-row" data-id="${m.ID}">
            <td><input type="radio" name="m" ${m.ID === s.machine_id ? "checked" : ""}></td>
            <td><strong>${escapeHTML(m.AssetTag || trunc(m.ID))}</strong></td>
            <td class="mono">${escapeHTML(m.MACPrimary || "—")}</td>
            <td>${escapeHTML([m.Vendor, m.Model].filter(Boolean).join(" / ") || "—")}</td>
          </tr>`).join("")}
        </tbody>
      </table></div>`;
  }
  if (s.step === 2) {
    if (!profiles.length) return `<div class="banner err">No deployment profiles configured.</div>`;
    return `
      <p class="muted">Select the deployment profile (image + answer file template).</p>
      <div id="profile-list" class="table-wrap"><table>
        <thead><tr><th></th><th>Profile</th><th>OS</th><th>Image</th></tr></thead>
        <tbody>
        ${profiles.map(p => `
          <tr class="profile-row" data-id="${p.id}">
            <td><input type="radio" name="p" ${p.id === s.profile_id ? "checked" : ""}></td>
            <td><strong>${escapeHTML(p.name)}</strong></td>
            <td>${escapeHTML(p.os_family)} ${escapeHTML(p.os_version)}</td>
            <td>${escapeHTML(p.image_name)}</td>
          </tr>`).join("")}
        </tbody>
      </table></div>`;
  }
  if (s.step === 3) {
    return `
      <p class="muted">Optional code parameters.</p>
      <div class="row">
        <label>TTL (hours) <input type="number" id="wiz-ttl" value="${s.ttl_hours}" min="1" max="168"></label>
        <label class="full">Label (audit trail) <input id="wiz-label" value="${escapeHTML(s.label)}" placeholder="e.g. sent to alice@branch"></label>
      </div>
      <div class="banner info small">Codes are single-use, expire on first redeem, and rate-limited (5 attempts before lock).</div>`;
  }
  if (s.step === 4) {
    const m = machines.find(x => x.ID === s.machine_id);
    const p = profiles.find(x => x.id === s.profile_id);
    return `
      <p class="muted">Review and issue.</p>
      <dl class="detail-grid">
        <dt>Machine</dt>     <dd><strong>${escapeHTML(m.AssetTag || trunc(m.ID))}</strong> <span class="muted small">(${escapeHTML(m.MACPrimary || "no mac")})</span></dd>
        <dt>Profile</dt>     <dd><strong>${escapeHTML(p.name)}</strong></dd>
        <dt>OS</dt>          <dd>${escapeHTML(p.os_family)} ${escapeHTML(p.os_version)}</dd>
        <dt>TTL</dt>         <dd>${s.ttl_hours} hours</dd>
        <dt>Label</dt>       <dd>${escapeHTML(s.label || "(none)")}</dd>
      </dl>`;
  }
  return "";
}

async function issueDeployment(s) {
  try {
    const r = await api("POST", "/api/v1/deployments/issue", {
      machine_id: s.machine_id,
      profile_id: s.profile_id,
      ttl_seconds: s.ttl_hours * 3600,
      issued_for: s.label || undefined,
    });
    $("#content").innerHTML = `
      <div class="page">
        <div class="page-title"><h1>Code issued</h1></div>
        <div class="code-display">
          <div class="label">Hand this to the onsite operator</div>
          <div class="code-big">${escapeHTML(r.code)}</div>
          <div class="meta">Expires ${escapeHTML(fmtAbsolute(r.expires_at))}</div>
        </div>
        <div class="banner info">
          The operator types this code into the USB bootstrap stick's TUI prompt.
          The code redeems for a single-use Tailscale ephemeral auth key (1h TTL)
          plus a token-bound iPXE chainload URL. Single-use; treat as a credential.
        </div>
        <div class="btn-row">
          <a class="btn" href="#/jobs">View jobs</a>
          <a class="btn secondary" href="#/deploy">Issue another</a>
        </div>
      </div>`;
    toast(`Code issued: ${r.code}`, "ok");
  } catch (e) { toast(e.message, "err"); }
}

// ---------- views: Jobs ----------

route("/jobs", async () => {
  setBreadcrumb([{label:"Jobs"}]);
  const params = new URLSearchParams(location.hash.split("?")[1] || "");
  const stateFilter = params.get("state") || "";

  const jobs = await api("GET", "/api/v1/jobs?limit=200" + (stateFilter ? `&state=${encodeURIComponent(stateFilter)}` : ""));

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div><h1>Deployment jobs</h1><p class="subtitle">All issued + in-flight + completed deployments.</p></div>
        <div class="page-actions"><a class="btn" href="#/deploy">+ New deployment</a></div>
      </div>
      <div class="filter-chips" style="margin: 0 0 12px 0;">
        ${["", "pending","bootstrapped","imaging","post_install","completed","failed","cancelled"].map(s => `
          <a class="chip ${stateFilter === s ? "active" : ""}" href="#/jobs${s ? `?state=${s}` : ""}">${s || "all"}</a>
        `).join("")}
      </div>
      ${jobs.length ? jobsTable(jobs) : `<div class="card"><div class="empty muted">No jobs match.</div></div>`}
    </div>`;

  if (jobs.some(j => ["pending","bootstrapped","imaging","post_install"].includes(j.state))) {
    const t = setTimeout(navigate, 5000);
    return () => clearTimeout(t);
  }
});

route("/jobs/:id", async ({ id }) => {
  setBreadcrumb([{label:"Jobs", href:"#/jobs"}, {label: id.slice(0,8)+"…"}]);
  const data = await api("GET", `/api/v1/jobs/${id}`);
  const j = data.job;
  const events = data.events || [];

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div>
          <h1>Deployment <span id="job-state-badge">${stateBadge(j.state)}</span></h1>
          <p class="subtitle mono small">${escapeHTML(j.id)}</p>
        </div>
        <div class="page-actions">
          ${["pending","bootstrapped","imaging"].includes(j.state)
            ? `<button class="btn danger" id="cancel-job-btn">Cancel deployment</button>` : ""}
          <a class="btn secondary" href="#/machines/${j.machine_id}">View machine</a>
        </div>
      </div>

      <div class="card" style="margin-top:16px;">
        <dl class="detail-grid">
          <dt>Machine</dt>    <dd><a href="#/machines/${j.machine_id}">${escapeHTML(j.machine_asset_tag || trunc(j.machine_id))}</a></dd>
          <dt>Profile</dt>    <dd>${escapeHTML(j.profile_name)}</dd>
          <dt>State</dt>      <dd id="job-state-cell">${stateBadge(j.state)}</dd>
          <dt>Created</dt>    <dd class="mono">${fmtAbsolute(j.created_at)}</dd>
          <dt>Started</dt>    <dd class="mono">${fmtAbsolute(j.started_at)}</dd>
          <dt>Finished</dt>   <dd id="job-finished-cell" class="mono">${fmtAbsolute(j.finished_at)}</dd>
        </dl>
      </div>

      <div class="section-title">Event timeline <span id="stream-status" class="muted small" style="font-weight:normal;text-transform:none;letter-spacing:0;"></span></div>
      <div class="card">
        <div class="timeline" id="event-timeline">
          ${events.map(e => timelineItem(e)).join("") || `<div class="empty muted">Waiting for first event…</div>`}
        </div>
      </div>
    </div>`;

  // Subscribe to live event stream. SSE pushes new events instantly so
  // we don't have to poll. If SSE isn't supported (or fails), we
  // gracefully degrade to a 4s polling fallback.
  let currentState = j.state;
  const isLive = s => ["pending","bootstrapped","imaging","post_install"].includes(s);

  const tlEl = $("#event-timeline");
  const statusEl = $("#stream-status");
  const stateBadgeEl = $("#job-state-badge");
  const stateCellEl = $("#job-state-cell");
  const finishedCellEl = $("#job-finished-cell");

  // Clean DOM if there were no events (we showed an "empty" message).
  if (events.length === 0) tlEl.innerHTML = "";

  const cancelBtn = $("#cancel-job-btn");
  if (cancelBtn) cancelBtn.addEventListener("click", async () => {
    const ok = await confirmModal({
      title: "Cancel deployment?",
      message: "The job moves to cancelled and its boot tokens are revoked. A machine already imaging will fail its next server call.",
      danger: true, primaryLabel: "Cancel deployment",
    });
    if (!ok) return;
    try {
      await api("POST", `/api/v1/jobs/${id}/cancel`);
      toast("Deployment cancelled");
      navigate();
    } catch (e) {
      toast("Cancel failed: " + e.message, "error");
    }
  });

  let es;
  if (typeof EventSource === "function") {
    es = new EventSource(`/api/v1/jobs/${id}/events/stream`);
    es.addEventListener("synced", () => {
      statusEl.innerHTML = `<span class="badge ok pulsing"><span class="dot"></span>live</span>`;
    });
    es.addEventListener("event", e => {
      const ev = JSON.parse(e.data);
      // Don't double-add events that came via the initial REST fetch.
      if (ev.id && tlEl.querySelector(`[data-evid="${ev.id}"]`)) return;
      if (tlEl.querySelector(".empty")) tlEl.innerHTML = "";
      tlEl.insertAdjacentHTML("beforeend", timelineItem(ev));
      // Update state badges if a state transition rode along.
      if (ev.state && ev.state !== currentState) {
        currentState = ev.state;
        stateBadgeEl.innerHTML = stateBadge(currentState);
        stateCellEl.innerHTML = stateBadge(currentState);
        if (!isLive(currentState)) {
          finishedCellEl.textContent = fmtAbsolute(new Date().toISOString());
        }
      }
    });
    es.addEventListener("timeout", () => {
      statusEl.innerHTML = `<span class="muted small">stream idle, reconnecting…</span>`;
    });
    es.addEventListener("error", () => {
      statusEl.innerHTML = `<span class="muted small">disconnected (auto-reconnects)</span>`;
    });
  } else {
    // Polling fallback for browsers without EventSource.
    statusEl.innerHTML = `<span class="muted small">polling (no SSE)</span>`;
    if (isLive(j.state)) {
      const t = setTimeout(navigate, 4000);
      return () => clearTimeout(t);
    }
  }

  return () => { if (es) es.close(); };
});

function timelineItem(e) {
  const cls = e.phase === "completed" ? "ok" : (e.phase === "failed" ? "err" : "");
  return `<div class="timeline-item ${cls}" data-evid="${e.id || ''}">
    <div class="when">${fmtAbsolute(e.at)} · <span class="muted">${escapeHTML(e.phase)}</span></div>
    <div class="what">${escapeHTML(e.message)}</div>
  </div>`;
}

// ---------- views: Profiles ----------

route("/profiles", async () => {
  setBreadcrumb([{label: "Profiles"}]);
  const [profiles, images] = await Promise.all([
    api("GET", "/api/v1/profiles"),
    api("GET", "/api/v1/images"),
  ]);
  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div><h1>Deployment profiles</h1>
          <p class="subtitle">A profile binds an image to an answer-file template (autoinstall, kickstart, unattend, etc.) and per-deploy variables.</p></div>
        <div class="page-actions"><button class="btn" id="new-profile-btn">+ New profile</button></div>
      </div>
      ${profiles.length ? `
        <div class="table-wrap"><table>
          <thead><tr><th>Name</th><th>Image</th><th>OS</th><th>Created</th><th></th></tr></thead>
          <tbody>
          ${profiles.map(p => `
            <tr onclick="location.hash='#/profiles/${p.id}'">
              <td><strong>${escapeHTML(p.name)}</strong></td>
              <td>${escapeHTML(p.image_name)}</td>
              <td>${escapeHTML(p.os_family)} ${escapeHTML(p.os_version)}</td>
              <td class="mono small muted">${fmtTime(p.created_at)}</td>
              <td class="actions"><a class="btn small secondary" href="#/profiles/${p.id}">Edit</a></td>
            </tr>`).join("")}
          </tbody>
        </table></div>` : `<div class="card"><div class="empty muted">No profiles yet.</div></div>`}
    </div>`;

  $("#new-profile-btn").addEventListener("click", () => openProfileCreateModal(images));
});

function openProfileCreateModal(images) {
  if (!images.length) {
    toast("Upload an image first via the Images tab", "err");
    return;
  }
  const imgOptions = images.map(i =>
    `<option value="${escapeHTML(i.id)}">${escapeHTML(i.name)} — ${escapeHTML(i.os_family)} ${escapeHTML(i.os_version)} ${escapeHTML(i.arch)}</option>`).join("");
  openModal({
    title: "New deployment profile",
    body: `
      <form id="profile-form">
        <div class="row">
          <label class="full">Name <input name="name" required placeholder="ubuntu-2404-engineering"></label>
        </div>
        <div class="row">
          <label class="full">Image
            <select name="image_id" required>${imgOptions}</select>
          </label>
        </div>
        <div class="row">
          <label class="full">Variables (JSON, optional)
            <textarea name="vars" rows="4" placeholder='{"hostname_template":"{{asset_tag}}","timezone":"UTC"}'>{}</textarea>
          </label>
        </div>
      </form>`,
    primary: { label: "Create" },
    secondary: "Cancel",
    onPrimary: async modal => {
      const fd = new FormData($("#profile-form", modal));
      let vars = {};
      const v = fd.get("vars");
      if (v) {
        try { vars = JSON.parse(v); }
        catch (e) { throw new Error("Variables: invalid JSON"); }
      }
      const created = await api("POST", "/api/v1/profiles", {
        name: fd.get("name"),
        image_id: fd.get("image_id"),
        answer_file_vars: vars,
      });
      toast(`Created profile ${created.name}`, "ok");
      location.hash = "#/profiles/" + created.id;
    },
  });
}

// ---------- views: Profile detail / editor ----------

const TEMPLATE_KINDS = [
  { id: "autoinstall", label: "Ubuntu autoinstall (cloud-init)", os: ["linux"] },
  { id: "kickstart",   label: "Kickstart (RHEL/Rocky/Alma/Fedora)", os: ["linux"] },
  { id: "preseed",     label: "Debian preseed", os: ["linux"] },
  { id: "cloud-init",  label: "Generic cloud-init user-data", os: ["linux"] },
  { id: "ignition",    label: "Ignition (Fedora CoreOS / Flatcar)", os: ["linux"] },
  { id: "unattend",    label: "Windows unattend.xml", os: ["windows"] },
];

route("/profiles/:id", async ({ id }) => {
  setBreadcrumb([{label:"Profiles", href:"#/profiles"}, {label: id.slice(0,8)+"…"}]);
  const [data, images] = await Promise.all([
    api("GET", `/api/v1/profiles/${id}`),
    api("GET", "/api/v1/images"),
  ]);
  const p = data.profile;
  const vars = data.answer_file_vars || {};
  const templates = data.templates || [];
  const tplByKind = Object.fromEntries(templates.map(t => [t.kind, t]));

  // Filter applicable kinds by OS family.
  const applicable = TEMPLATE_KINDS.filter(k => k.os.includes(p.os_family));

  let activeKind = applicable[0]?.id || "autoinstall";
  let editingMeta = false;

  const render = () => {
    $("#content").innerHTML = `
      <div class="page">
        <div class="page-title">
          <div>
            <h1>${escapeHTML(p.name)}</h1>
            <p class="subtitle">${escapeHTML(p.image_name)} · ${escapeHTML(p.os_family)} ${escapeHTML(p.os_version)} · created ${fmtTime(p.created_at)}</p>
          </div>
          <div class="page-actions">
            <button class="btn secondary" id="edit-meta">Edit metadata</button>
            <button class="btn danger" id="delete-profile">Delete</button>
          </div>
        </div>

        <div class="section-title">Variables</div>
        <div class="card">
          <p class="hint muted small">Available in templates as <code>{{ vars.&lt;key&gt; }}</code>. Plus built-ins:
            <code>{{ asset_tag }}</code>, <code>{{ machine_id }}</code>, <code>{{ deploy_fqdn }}</code>, <code>{{ job_id }}</code>, <code>{{ one_shot_token }}</code>.</p>
          <textarea id="vars-editor" rows="6" style="width:100%;">${escapeHTML(JSON.stringify(vars, null, 2))}</textarea>
          <div class="btn-row" style="margin-top: 8px;">
            <button class="btn small" id="vars-save">Save vars</button>
            <span id="vars-status" class="muted small"></span>
          </div>
        </div>

        <div class="section-title">Answer file templates</div>
        <div class="card" style="padding: 0;">
          <div class="filter-chips" style="padding: 12px 16px; border-bottom: 1px solid var(--border-soft);">
            ${applicable.map(k => `
              <a class="chip ${k.id === activeKind ? "active" : ""} tpl-tab" data-kind="${k.id}" href="#">
                ${escapeHTML(k.label)}${tplByKind[k.id] ? " ●" : ""}
              </a>`).join("")}
          </div>
          <div style="padding: 16px;">
            <textarea id="tpl-editor" rows="22" style="width:100%; font-family: var(--mono); font-size: 12.5px;">${escapeHTML(tplByKind[activeKind]?.body || defaultTemplateFor(activeKind))}</textarea>
            <div class="btn-row" style="margin-top: 8px; justify-content: space-between;">
              <div>
                <button class="btn small" id="tpl-save">Save template</button>
                ${tplByKind[activeKind] ? `<button class="btn small danger" id="tpl-delete">Delete template</button>` : ""}
              </div>
              <span id="tpl-status" class="muted small">${tplByKind[activeKind] ? `Last saved ${fmtTime(tplByKind[activeKind].updated_at)}` : "Not yet saved"}</span>
            </div>
          </div>
        </div>
      </div>`;

    $$("#content .tpl-tab").forEach(t => t.addEventListener("click", e => {
      e.preventDefault();
      activeKind = t.dataset.kind;
      render();
    }));

    $("#vars-save").addEventListener("click", async () => {
      const status = $("#vars-status");
      status.textContent = "Saving…"; status.className = "muted small";
      try {
        const parsed = JSON.parse($("#vars-editor").value);
        await api("PATCH", `/api/v1/profiles/${id}`, { answer_file_vars: parsed });
        status.textContent = "Saved"; status.className = "small";
        toast("Variables saved", "ok");
      } catch (e) {
        status.textContent = "Error: " + e.message; status.className = "err small";
      }
    });

    $("#tpl-save").addEventListener("click", async () => {
      const body = $("#tpl-editor").value;
      const status = $("#tpl-status");
      status.textContent = "Saving…";
      try {
        await api("PUT", `/api/v1/profiles/${id}/templates`, { kind: activeKind, body });
        toast(`Saved ${activeKind} template`, "ok");
        navigate(); // reload to refresh tplByKind state
      } catch (e) {
        status.textContent = "Error: " + e.message;
        toast(e.message, "err");
      }
    });

    const del = $("#tpl-delete");
    if (del) del.addEventListener("click", async () => {
      const ok = await confirmModal({
        title: "Delete template",
        message: `Delete the ${activeKind} template for ${p.name}?`,
        danger: true, primaryLabel: "Delete",
      });
      if (!ok) return;
      try {
        await api("DELETE", `/api/v1/profiles/${id}/templates/${activeKind}`);
        toast("Template deleted", "ok");
        navigate();
      } catch (e) { toast(e.message, "err"); }
    });

    $("#delete-profile").addEventListener("click", async () => {
      const ok = await confirmModal({
        title: "Delete profile",
        message: `Delete profile "${p.name}"? This is permanent.`,
        danger: true, primaryLabel: "Delete",
      });
      if (!ok) return;
      try {
        await api("DELETE", `/api/v1/profiles/${id}`);
        toast("Profile deleted", "ok");
        location.hash = "#/profiles";
      } catch (e) { toast(e.message, "err"); }
    });

    $("#edit-meta").addEventListener("click", () => openProfileEditMetaModal(p, images, () => navigate()));
  };
  render();
});

function openProfileEditMetaModal(profile, images, onSaved) {
  const imgOptions = images.map(i =>
    `<option value="${escapeHTML(i.id)}" ${i.id===profile.image_id?"selected":""}>${escapeHTML(i.name)} — ${escapeHTML(i.os_family)} ${escapeHTML(i.os_version)} ${escapeHTML(i.arch)}</option>`).join("");
  openModal({
    title: "Edit profile metadata",
    body: `
      <form id="profile-meta-form">
        <div class="row">
          <label class="full">Name <input name="name" value="${escapeHTML(profile.name)}" required></label>
        </div>
        <div class="row">
          <label class="full">Image
            <select name="image_id">${imgOptions}</select>
          </label>
        </div>
      </form>`,
    primary: { label: "Save" },
    secondary: "Cancel",
    onPrimary: async modal => {
      const fd = new FormData($("#profile-meta-form", modal));
      const body = {};
      if (fd.get("name") !== profile.name) body.name = fd.get("name");
      if (fd.get("image_id") !== profile.image_id) body.image_id = fd.get("image_id");
      if (Object.keys(body).length === 0) { toast("No changes", ""); return; }
      await api("PATCH", `/api/v1/profiles/${profile.id}`, body);
      toast("Profile updated", "ok");
      onSaved && onSaved();
    },
  });
}

function defaultTemplateFor(kind) {
  switch (kind) {
    case "autoinstall":
      return `#cloud-config
autoinstall:
  version: 1
  identity:
    hostname: {{ vars.hostname_template | default(asset_tag) }}
    username: ubuntu
    # mkpasswd -m sha-512
    password: '$6$rounds=4096$REPLACE_ME'
  ssh:
    install-server: yes
    authorized-keys: {{ vars.authorized_keys | default([]) | toJSON }}
  storage:
    layout:
      name: lvm
  packages:
    - openssh-server
    - python3-minimal
  late-commands:
    - curtin in-target -- bash -c 'curl -sf {{ deploy_fqdn }}/v1/jobs/{{ job_id }}/events -X POST -H "Authorization: Bearer {{ one_shot_token }}" -H "Content-Type: application/json" --data "{\\"phase\\":\\"completed\\",\\"message\\":\\"autoinstall finished\\"}" || true'
`;
    case "kickstart":
      return `text
lang en_US.UTF-8
keyboard us
timezone {{ vars.timezone | default("UTC") }}
network --hostname={{ asset_tag }} --bootproto=dhcp
rootpw --iscrypted REPLACE_ME
authselect select sssd
%post
curl -sf "{{ deploy_fqdn }}/v1/jobs/{{ job_id }}/events" \\
  -H "Authorization: Bearer {{ one_shot_token }}" \\
  -H "Content-Type: application/json" \\
  --data '{"phase":"completed","message":"kickstart done"}' || true
%end
`;
    case "preseed":
      return `d-i debian-installer/locale string en_US.UTF-8
d-i netcfg/get_hostname string {{ asset_tag }}
d-i passwd/user-fullname string {{ vars.full_name | default("Operator") }}
d-i passwd/username string {{ vars.username | default("ubuntu") }}
d-i passwd/user-password-crypted password REPLACE_ME
d-i preseed/late_command string in-target curl -sf \\
  -H "Authorization: Bearer {{ one_shot_token }}" \\
  -H "Content-Type: application/json" \\
  --data '{"phase":"completed","message":"preseed done"}' \\
  "{{ deploy_fqdn }}/v1/jobs/{{ job_id }}/events" || true
`;
    case "cloud-init":
      return `#cloud-config
hostname: {{ asset_tag }}
timezone: {{ vars.timezone | default("UTC") }}
runcmd:
  - curl -sf "{{ deploy_fqdn }}/v1/jobs/{{ job_id }}/events" -H "Authorization: Bearer {{ one_shot_token }}" -H "Content-Type: application/json" --data '{"phase":"completed","message":"cloud-init done"}' || true
`;
    case "ignition":
      return `{
  "ignition": { "version": "3.4.0" },
  "passwd": {
    "users": [
      { "name": "core", "sshAuthorizedKeys": {{ vars.authorized_keys | default([]) | toJSON }} }
    ]
  },
  "storage": {},
  "systemd": {}
}
`;
    case "unattend":
      return `<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <ComputerName>{{ asset_tag | upper }}</ComputerName>
      <TimeZone>{{ vars.timezone | default("UTC") }}</TimeZone>
      <UserAccounts>
        <LocalAccounts>
          <LocalAccount wcm:action="add">
            <Name>Administrator</Name>
            <Password>
              <Value>REPLACE_ME_BASE64_UTF16LE</Value>
              <PlainText>false</PlainText>
            </Password>
          </LocalAccount>
        </LocalAccounts>
      </UserAccounts>
    </component>
  </settings>
</unattend>
`;
  }
  return "";
}

// ---------- views: Images ----------

route("/images", async () => {
  setBreadcrumb([{label:"Images"}]);
  const imgs = await api("GET", "/api/v1/images");
  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div><h1>Images</h1>
          <p class="subtitle">Install media — kernel/initrd URLs (Linux) or wimboot/boot.wim/install.wim URLs (Windows). Point at upstream mirrors or your own HTTP storage; the iPXE chain at deploy time fetches directly from these URLs.</p></div>
        <div class="page-actions">
          <button class="btn secondary" id="browse-catalog-btn">⊞ Browse catalog</button>
          <button class="btn" id="register-image-btn">+ Register image</button>
        </div>
      </div>
      ${imgs.length ? `
        <div class="table-wrap"><table>
          <thead><tr>
            <th>Name</th><th>OS</th><th>Arch</th><th>Media URLs</th><th>Description</th>
          </tr></thead>
          <tbody>
          ${imgs.map(i => {
            const m = i.media || {};
            const urls = i.os_family === "linux"
              ? [["kernel", m.kernel_url], ["initrd", m.initrd_url]]
              : [["wimboot", m.wimboot_url], ["boot.wim", m.bootwim_url], ["install.wim", m.wim_url]];
            const populated = urls.filter(([_, u]) => u).length;
            return `
            <tr onclick="location.hash='#/images/${i.id}'">
              <td><strong>${escapeHTML(i.name)}</strong></td>
              <td>${escapeHTML(i.os_family)} ${escapeHTML(i.os_version)}</td>
              <td><span class="badge">${escapeHTML(i.arch)}</span></td>
              <td>${populated > 0
                ? `<span class="badge ok"><span class="dot"></span>${populated}/${urls.length} set</span>`
                : `<span class="badge warn"><span class="dot"></span>not configured</span>`}</td>
              <td class="muted small">${escapeHTML(i.description || "—")}</td>
            </tr>`;
          }).join("")}
          </tbody>
        </table></div>` : `<div class="card"><div class="empty muted">No images. Click "+ Register image" to add one.</div></div>`}
    </div>`;

  $("#register-image-btn").addEventListener("click", () => openImageRegisterModal());
  $("#browse-catalog-btn").addEventListener("click", () => openCatalogBrowser());
});

async function openCatalogBrowser() {
  let catalog;
  try {
    catalog = await api("GET", "/api/v1/catalog");
  } catch (e) {
    toast("Failed to load catalog: " + e.message, "err");
    return;
  }
  let selectedEntry = null;
  let searchTerm = "";

  const renderBody = () => {
    const term = searchTerm.toLowerCase();
    const filteredCats = catalog.categories.map(cat => ({
      ...cat,
      entries: cat.entries.filter(e =>
        !term ||
        e.name.toLowerCase().includes(term) ||
        e.id.toLowerCase().includes(term) ||
        (e.description || "").toLowerCase().includes(term))
    })).filter(c => c.entries.length);

    return `
      <div class="search" style="margin-bottom:12px;">
        <input id="cat-search" placeholder="Search distros…" autofocus value="${escapeHTML(searchTerm)}" autocomplete="off">
        <span class="muted small">${filteredCats.reduce((n,c)=>n+c.entries.length,0)} of ${catalog.categories.reduce((n,c)=>n+c.entries.length,0)} · catalog v${escapeHTML(catalog.version)}</span>
      </div>
      <div style="max-height: 50vh; overflow-y: auto; margin: 0 -4px;">
      ${filteredCats.map(cat => `
        <div class="section-title" style="margin: 12px 4px 6px;">${escapeHTML(cat.name)}</div>
        <div style="display: grid; grid-template-columns: 1fr; gap: 4px; margin: 0 4px;">
        ${cat.entries.map(e => `
          <label class="cat-entry" data-id="${escapeHTML(e.id)}" style="display:flex; gap:10px; align-items:flex-start; padding: 10px 12px; border:1px solid var(--border); border-radius: var(--radius); cursor: pointer; ${selectedEntry === e.id ? "border-color: var(--accent); background: var(--panel-2);" : ""}">
            <input type="radio" name="cat-entry" value="${escapeHTML(e.id)}" ${selectedEntry === e.id ? "checked" : ""} style="margin-top: 2px;">
            <div style="flex:1;">
              <div style="font-weight: 500;">${escapeHTML(e.name)} <span class="badge" style="margin-left: 6px;">${escapeHTML(e.os_family)} ${escapeHTML(e.arch)}</span></div>
              <div class="muted small" style="margin-top: 4px;">${escapeHTML(e.description || "")}</div>
              ${(!e.media || !Object.values(e.media).filter(Boolean).length) ? `<div class="small" style="color: var(--warn); margin-top: 4px;">⚠ stub — URLs need manual completion after install</div>` : ""}
            </div>
          </label>`).join("")}
        </div>
      `).join("") || `<div class="empty muted">No matches.</div>`}
      </div>
      <div class="row" style="margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--border-soft);">
        <label class="full">Image name in your library (optional)
          <input id="cat-name" placeholder="leave blank to use catalog id">
        </label>
      </div>`;
  };

  openModal({
    title: "Browse catalog",
    body: renderBody(),
    primary: { label: "Install" },
    secondary: "Cancel",
    onPrimary: async modal => {
      if (!selectedEntry) {
        toast("Select an entry first", "err");
        return false;
      }
      const customName = $("#cat-name", modal).value.trim();
      const created = await api("POST", "/api/v1/catalog/install", {
        entry_id: selectedEntry,
        name: customName || undefined,
      });
      toast(`Installed ${created.name}`, "ok");
      location.hash = "#/images/" + created.id;
    },
  });

  // Wire up search + selection AFTER the modal renders.
  const root = $("#modal-root");
  const wireUp = () => {
    const search = $("#cat-search", root);
    if (search) {
      search.addEventListener("input", e => {
        searchTerm = e.target.value;
        const body = $(".modal-body", root);
        body.innerHTML = renderBody();
        wireUp();
        const s2 = $("#cat-search", root);
        if (s2) {
          s2.focus();
          s2.setSelectionRange(s2.value.length, s2.value.length);
        }
      });
    }
    $$(".cat-entry", root).forEach(el => {
      el.addEventListener("click", () => {
        selectedEntry = el.dataset.id;
        $$(".cat-entry", root).forEach(x => {
          x.style.borderColor = "var(--border)";
          x.style.background = "";
          const r = $("input[type=radio]", x);
          if (r) r.checked = (x === el);
        });
        el.style.borderColor = "var(--accent)";
        el.style.background = "var(--panel-2)";
      });
    });
  };
  wireUp();
}

function openImageRegisterModal() {
  openModal({
    title: "Register image",
    body: `
      <form id="image-form">
        <div class="row">
          <label>Name * <input name="name" required placeholder="ubuntu-2404-engineering"></label>
          <label>OS family
            <select name="os_family">
              <option value="linux" selected>linux</option>
              <option value="windows">windows</option>
            </select>
          </label>
        </div>
        <div class="row">
          <label>OS version * <input name="os_version" required placeholder="24.04"></label>
          <label>Arch
            <select name="arch">
              <option value="amd64" selected>amd64</option>
              <option value="arm64">arm64</option>
            </select>
          </label>
        </div>
        <div class="row">
          <label class="full">Description <input name="description" placeholder="Ubuntu 24.04 LTS netboot from upstream mirror"></label>
        </div>
        <p class="hint small muted" style="margin-top: 4px;">Media URLs (kernel, initrd, etc.) are configured on the image's detail page after creation.</p>
      </form>`,
    primary: { label: "Create" },
    secondary: "Cancel",
    onPrimary: async modal => {
      const fd = new FormData($("#image-form", modal));
      const body = {
        name: fd.get("name"),
        os_family: fd.get("os_family"),
        os_version: fd.get("os_version"),
        arch: fd.get("arch"),
      };
      const desc = fd.get("description");
      if (desc) body.description = desc;
      const created = await api("POST", "/api/v1/images", body);
      toast(`Created image ${created.name}`, "ok");
      location.hash = "#/images/" + created.id;
    },
  });
}

// ---------- views: Image detail ----------

route("/images/:id", async ({ id }) => {
  setBreadcrumb([{label:"Images", href:"#/images"}, {label: id.slice(0,8)+"…"}]);
  const img = await api("GET", `/api/v1/images/${id}`);
  const m = img.media || {};
  const isLinux = img.os_family === "linux";

  const fields = isLinux
    ? [
        { key: "kernel_url",  label: "Kernel URL",  hint: "iPXE 'kernel' line. e.g. https://releases.ubuntu.com/24.04/.../linux" },
        { key: "initrd_url",  label: "Initrd URL",  hint: "iPXE 'initrd' line. e.g. .../initrd" },
        { key: "kernel_args", label: "Kernel args (optional)", hint: "Appended to cmdline. e.g. 'autoinstall ds=nocloud-net'" },
      ]
    : [
        { key: "wimboot_url", label: "wimboot URL",        hint: "Pinned iPXE wimboot binary." },
        { key: "bootwim_url", label: "WinPE boot.wim URL", hint: "Customized WinPE that fetches install.wim and runs deploy.cmd." },
        { key: "wim_url",     label: "Install WIM URL",    hint: "The hardware-independent Windows image." },
      ];

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div>
          <h1>${escapeHTML(img.name)}</h1>
          <p class="subtitle">${escapeHTML(img.os_family)} ${escapeHTML(img.os_version)} · ${escapeHTML(img.arch)} · ${img.versions_count} version(s)</p>
        </div>
        <div class="page-actions">
          <button class="btn secondary" id="edit-meta">Edit metadata</button>
          <button class="btn danger" id="delete-image">Delete</button>
        </div>
      </div>

      ${img.description ? `<div class="banner info" style="margin-top:16px;">${escapeHTML(img.description)}</div>` : ""}

      <div class="section-title">Media URLs</div>
      <div class="card">
        <p class="hint muted small">These URLs are baked into the iPXE chain script at deploy time. Tailnet-internal URLs work; pointing at the public internet is also fine because the bootstrap stick is already on the tailnet by design.</p>
        <form id="media-form">
          ${fields.map(f => `
            <div class="row">
              <label class="full">
                ${escapeHTML(f.label)}
                <input name="${f.key}" value="${escapeHTML(m[f.key] || "")}" placeholder="${escapeHTML(f.hint)}" autocomplete="off">
              </label>
            </div>
          `).join("")}
          <div class="btn-row" style="margin-top: 8px;">
            <button class="btn small" type="submit">Save URLs</button>
            <span id="media-status" class="muted small"></span>
          </div>
        </form>
      </div>

      <div class="section-title">Versions</div>
      <div class="card">
        <div id="versions-list"><div class="muted small">Loading…</div></div>
        <div class="btn-row" style="margin-top:12px;">
          <label class="btn small secondary" style="cursor:pointer;">
            Upload new version
            <input type="file" id="version-file" style="display:none;">
          </label>
          <span id="upload-status" class="muted small"></span>
        </div>
      </div>

      <div class="section-title">Used by profiles</div>
      <div id="profiles-using" class="card"><div class="muted small">Loading…</div></div>
    </div>`;

  const renderVersions = async () => {
    const versions = await api("GET", `/api/v1/images/${id}/versions`).catch(() => []);
    $("#versions-list").innerHTML = versions.length ? `
      <table><thead><tr><th>Tag</th><th>SHA-256</th><th>Size</th><th>Added</th></tr></thead><tbody>
        ${versions.map(v => `
          <tr class="no-hover">
            <td><code>${escapeHTML(v.version_tag)}</code></td>
            <td class="mono small">${escapeHTML(trunc(v.blob_sha256, 16))}</td>
            <td class="mono small">${(v.size_bytes / 1048576).toFixed(1)} MiB</td>
            <td class="muted small">${fmtTime(v.created_at)}</td>
          </tr>`).join("")}
      </tbody></table>` :
      `<div class="muted small">No versions uploaded. The deploy pipeline falls back to the media URLs above.</div>`;
  };
  renderVersions();

  $("#version-file").addEventListener("change", async e => {
    const file = e.target.files[0];
    if (!file) return;
    const status = $("#upload-status");
    try {
      status.textContent = "Hashing…";
      const sha = await sha256File(file, p => {
        status.textContent = `Hashing… ${Math.round(p * 100)}%`;
      });
      status.textContent = "Registering blob…";
      const blob = await api("POST", "/api/v1/blobs", {
        sha256: sha, size_bytes: file.size, filename: file.name, role: "images",
      });
      status.textContent = "Uploading…";
      const put = await fetch(blob.upload_url, { method: "PUT", body: file });
      if (!put.ok) throw new Error("upload failed: HTTP " + put.status);
      status.textContent = "Linking version…";
      await api("POST", `/api/v1/images/${id}/versions`, { blob_id: blob.blob_id });
      status.textContent = "";
      toast("Version uploaded", "ok");
      renderVersions();
    } catch (err) {
      status.textContent = "";
      toast("Upload failed: " + err.message, "err");
    } finally {
      e.target.value = "";
    }
  });

  const profiles = await api("GET", "/api/v1/profiles").catch(() => []);
  const using = profiles.filter(p => p.image_id === id);
  $("#profiles-using").innerHTML = using.length ? `
    <table><tbody>
      ${using.map(p => `
        <tr onclick="location.hash='#/profiles/${p.id}'">
          <td><strong>${escapeHTML(p.name)}</strong></td>
          <td class="muted small">created ${fmtTime(p.created_at)}</td>
        </tr>`).join("")}
    </tbody></table>` : `<div class="muted small">No profiles use this image yet.</div>`;

  $("#media-form").addEventListener("submit", async e => {
    e.preventDefault();
    const fd = new FormData(e.target);
    const media = {};
    for (const [k, v] of fd) if (v) media[k] = v;
    const status = $("#media-status");
    status.textContent = "Saving…";
    try {
      await api("PATCH", `/api/v1/images/${id}`, { media });
      status.textContent = "Saved";
      toast("Media URLs saved", "ok");
    } catch (err) { status.textContent = "Error: " + err.message; toast(err.message, "err"); }
  });

  $("#edit-meta").addEventListener("click", () => {
    openModal({
      title: "Edit image metadata",
      body: `
        <form id="meta-form">
          <div class="row"><label class="full">Name <input name="name" value="${escapeHTML(img.name)}" required></label></div>
          <div class="row"><label class="full">Description <input name="description" value="${escapeHTML(img.description||"")}"></label></div>
        </form>`,
      primary: { label: "Save" },
      secondary: "Cancel",
      onPrimary: async modal => {
        const fd = new FormData($("#meta-form", modal));
        await api("PATCH", `/api/v1/images/${id}`, {
          name: fd.get("name"),
          description: fd.get("description") || "",
        });
        toast("Image updated", "ok");
        navigate();
      },
    });
  });

  $("#delete-image").addEventListener("click", async () => {
    const ok = await confirmModal({
      title: "Delete image",
      message: `Delete image "${img.name}"? This is permanent.`,
      danger: true, primaryLabel: "Delete",
    });
    if (!ok) return;
    try {
      await api("DELETE", `/api/v1/images/${id}`);
      toast("Image deleted", "ok");
      location.hash = "#/images";
    } catch (e) { toast(e.message, "err"); }
  });
});

// ---------- views: Sticks ----------

route("/sticks", async () => {
  setBreadcrumb([{label:"Bootstrap sticks"}]);
  const ss = await api("GET", "/api/v1/bootstrap-sticks");
  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title">
        <div><h1>Bootstrap sticks</h1>
        <p class="subtitle">Inventory of physical USB sticks. Register after building with <code>make-stick.sh</code>.</p></div>
        <div class="page-actions"><button class="btn" id="gen-stick-config">Generate stick config</button></div>
      </div>
      ${ss.length ? `
        <div class="table-wrap"><table>
          <thead><tr>
            <th>Built</th><th>SHA-256 (img)</th><th>Tailnet</th><th>Deploy URL</th><th>CA fingerprint</th><th>Label</th>
          </tr></thead>
          <tbody>
          ${ss.map(s => `
            <tr class="no-hover">
              <td class="mono small">${fmtAbsolute(s.BuiltAt)}</td>
              <td class="mono small">${escapeHTML(trunc(s.ImageSHA256, 14))}</td>
              <td>${escapeHTML(s.Tailnet)}</td>
              <td><a href="${escapeHTML(s.DeployURL)}" target="_blank">${escapeHTML(s.DeployURL)}</a></td>
              <td class="mono small">${escapeHTML(trunc(s.CAFingerprint, 18))}</td>
              <td>${escapeHTML((s.Label && (s.Label.String || s.Label)) || "—")}</td>
            </tr>`).join("")}
          </tbody>
        </table></div>` : `
        <div class="card"><div class="empty">
          <div class="empty-icon">∎</div>
          <div>No sticks registered.</div>
          <p class="small muted">Build with <code>make -C bootstrap</code>, customize with <code>bootstrap/make-stick.sh</code>, then register via the deployctl CLI.</p>
        </div></div>`}
    </div>`;

  $("#gen-stick-config").addEventListener("click", async () => {
    let tailnet = "";
    const ask = () => new Promise(resolve => {
      let confirmed = false;
      openModal({
        title: "Generate stick config",
        body: `
          <p class="small muted">Produces the exact <code>make-stick.sh</code> invocation and the CA
          certificate for this deployment. Build happens on your workstation (needs root for losetup).</p>
          <form id="stick-config-form">
            <div class="row"><label class="full">Tailnet name
              <input name="tailnet" placeholder="acmecorp.headscale.example.com" autocomplete="off">
            </label></div>
          </form>`,
        primary: { label: "Generate" },
        secondary: "Cancel",
        onPrimary: modal => {
          tailnet = new FormData($("#stick-config-form", modal)).get("tailnet") || "";
          confirmed = true;
        },
        onClose: () => resolve(confirmed),
      });
    });
    if (!(await ask())) return;
    try {
      const cfg = await api("GET", "/api/v1/bootstrap-sticks/config" +
        (tailnet ? "?tailnet=" + encodeURIComponent(tailnet) : ""));
      openModal({
        title: "Stick build config",
        body: `
          <p class="small muted">Run this from a checkout of the deployserver repo after
          <code>make -C bootstrap</code>:</p>
          <pre class="mono small" style="white-space:pre-wrap;overflow-x:auto;">${escapeHTML(cfg.make_stick_command)}</pre>
          ${cfg.ca_pem ? `
            <p class="small muted">Save this as <code>deploy-ca.pem</code> next to the command:</p>
            <pre class="mono small" style="max-height:180px;overflow:auto;">${escapeHTML(cfg.ca_pem)}</pre>`
          : `<p class="small muted">⚠ No CA certificate found on the server
             (DEPLOY_CA_CERT_PATH). Point --ca-cert at the CA that signs
             ${escapeHTML(cfg.deploy_url)}.</p>`}
        `,
        primary: { label: "Copy command" },
        secondary: "Close",
        onPrimary: async () => {
          await navigator.clipboard.writeText(cfg.make_stick_command);
          toast("Command copied", "ok");
        },
      });
    } catch (e) { toast(e.message, "err"); }
  });
});

// ---------- views: Audit ----------

let auditState = { since: "24h", action: "" };

route("/audit", async () => {
  setBreadcrumb([{label:"Audit log"}]);
  const params = new URLSearchParams();
  params.set("since", auditState.since);
  if (auditState.action) params.set("action", auditState.action);
  const events = await api("GET", "/api/v1/audit?" + params);

  $("#content").innerHTML = `
    <div class="page">
      <div class="page-title"><h1>Audit log</h1>
        <p class="subtitle">Append-only record of every operator and stick action. Mirrored to <code>/var/log/deployserver/audit.log</code>.</p></div>

      <form id="audit-form" style="margin-bottom: 16px;">
        <div class="row">
          <label>Since
            <select name="since">
              ${["1h","6h","24h","168h","720h"].map(v => `<option value="${v}" ${v===auditState.since?"selected":""}>${v}</option>`).join("")}
            </select>
          </label>
          <label>Action prefix <input name="action" value="${escapeHTML(auditState.action)}" placeholder="auth_code"></label>
          <label>&nbsp;<button type="submit">Apply</button></label>
        </div>
      </form>

      ${events.length ? `
        <div class="table-wrap"><table>
          <thead><tr>
            <th></th><th>Action</th><th>Actor</th><th>Subject</th><th>Source IP</th><th>When</th>
          </tr></thead>
          <tbody>
          ${events.map(e => `
            <tr class="no-hover">
              <td>${stateBadgeForAction(e.action)}</td>
              <td><code>${escapeHTML(e.action)}</code></td>
              <td class="muted small">${escapeHTML(e.actor_kind || "")}<br><span class="mono">${escapeHTML(trunc(e.actor_id))}</span></td>
              <td class="muted small">${escapeHTML(e.subject_kind || "")}<br><span class="mono">${escapeHTML(trunc(e.subject_id))}</span></td>
              <td class="mono small">${escapeHTML(e.source_ip || "—")}</td>
              <td class="mono small muted">${fmtAbsolute(e.at)}</td>
            </tr>`).join("")}
          </tbody>
        </table></div>` : `<div class="card"><div class="empty muted">No events match.</div></div>`}
    </div>`;

  $("#audit-form").addEventListener("submit", e => {
    e.preventDefault();
    const fd = new FormData(e.target);
    auditState.since = fd.get("since");
    auditState.action = fd.get("action") || "";
    navigate();
  });
});

// ---------- boot ----------

async function boot() {
  await loadAuthConfig();
  await completeLogin();
  if (!authCfg.dev_mode && !idToken()) {
    await startLogin();
    return; // navigating away to the IdP
  }
  try {
    const me = await api("GET", "/api/v1/me");
    if (me.dev_mode) $("#dev-banner").classList.remove("hidden");
    const info = $("#user-info");
    info.textContent = me.user_id && me.user_id !== "00000000-0000-0000-0000-000000000000"
      ? `user ${trunc(me.user_id, 8)}`
      : "anonymous (dev mode)";
    if (!authCfg.dev_mode) {
      info.insertAdjacentHTML("afterend",
        ` <a href="#" id="logout-link" class="small">log out</a>`);
      $("#logout-link").addEventListener("click", e => { e.preventDefault(); logout(); });
    }
  } catch {}
  navigate();
}

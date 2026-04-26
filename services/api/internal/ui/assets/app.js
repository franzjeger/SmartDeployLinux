// Vanilla JS — no framework, no build step. Calls /api/v1/* and renders.

const $ = (q, root = document) => root.querySelector(q);
const $$ = (q, root = document) => Array.from(root.querySelectorAll(q));

async function api(method, path, body) {
  const opts = { method, headers: { "Content-Type": "application/json" } };
  if (body !== undefined) opts.body = JSON.stringify(body);
  const r = await fetch(path, opts);
  const text = await r.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  if (!r.ok) {
    const msg = (data && data.error) || data || `HTTP ${r.status}`;
    throw new Error(typeof msg === "string" ? msg : JSON.stringify(msg));
  }
  return data;
}

const fmtTime = iso => {
  if (!iso) return "—";
  const d = new Date(iso);
  return d.toLocaleString();
};
const trunc = (s, n = 8) => s ? (s.length > n ? s.slice(0, n) + "…" : s) : "—";

// --- tabs ---

function showTab(name) {
  $$(".tab").forEach(a => a.classList.toggle("active", a.dataset.tab === name));
  $$(".view").forEach(v => v.classList.toggle("active", v.id === name));
  if (name === "dash") loadDashboard();
  if (name === "machines") loadMachines();
  if (name === "deploy") loadDeployForm();
  if (name === "audit") loadAudit();
  if (name === "sticks") loadSticks();
}

document.addEventListener("click", e => {
  const tab = e.target.closest(".tab");
  if (tab && tab.dataset.tab) {
    e.preventDefault();
    location.hash = "#" + tab.dataset.tab;
    showTab(tab.dataset.tab);
  }
});

// --- dashboard ---

async function loadDashboard() {
  try {
    const ms = await api("GET", "/api/v1/machines");
    $("#m-count").textContent = ms.length;
    $("#dash-machines tbody").innerHTML = ms.slice(0, 5).map(m => `
      <tr>
        <td>${escape(m.AssetTag || m.asset_tag || "—")}</td>
        <td class="mono">${escape(trunc(m.ID || m.id, 8))}</td>
      </tr>`).join("") || `<tr><td class="muted">No machines yet. Use the Machines tab to register one.</td></tr>`;
  } catch (e) { $("#dash-machines tbody").innerHTML = `<tr><td class="err">${escape(e.message)}</td></tr>`; }

  try {
    const a = await api("GET", "/api/v1/audit?since=24h&limit=10");
    $("#dash-audit tbody").innerHTML = a.length
      ? a.map(e => `<tr><td class="mono">${fmtTime(e.at)}</td><td>${escape(e.action)}</td></tr>`).join("")
      : `<tr><td class="muted">No activity in last 24h.</td></tr>`;
  } catch (e) { $("#dash-audit tbody").innerHTML = `<tr><td class="err">${escape(e.message)}</td></tr>`; }
}

// --- machines ---

async function loadMachines() {
  await populateProfileSelect("#machine-profile-select", true);
  try {
    const ms = await api("GET", "/api/v1/machines");
    const profs = await api("GET", "/api/v1/profiles").catch(() => []);
    const profById = Object.fromEntries(profs.map(p => [p.id, p.name]));
    $("#machines-table tbody").innerHTML = ms.length ? ms.map(m => {
      const id = m.ID || m.id;
      const tag = m.AssetTag || m.asset_tag;
      const mac = m.MACPrimary || m.mac_primary;
      const v = m.Vendor || m.vendor;
      const mod = m.Model || m.model;
      const pid = m.DefaultProfileID || m.default_profile_id;
      return `<tr>
        <td>${escape(tag || "—")}</td>
        <td class="mono">${escape(mac || "—")}</td>
        <td>${escape([v, mod].filter(Boolean).join(" / ") || "—")}</td>
        <td>${escape(profById[pid] || (pid ? trunc(pid) : "—"))}</td>
        <td class="mono">${fmtTime(m.CreatedAt || m.created_at)}</td>
        <td class="mono">${escape(trunc(id, 8))}</td>
      </tr>`;
    }).join("") : `<tr><td colspan="6" class="muted">No machines yet.</td></tr>`;
  } catch (e) { $("#machines-table tbody").innerHTML = `<tr><td colspan="6" class="err">${escape(e.message)}</td></tr>`; }
}

$("#create-machine").addEventListener("submit", async e => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const body = {};
  for (const [k, v] of fd) if (v) body[k] = v;
  const result = $("#create-result");
  result.textContent = "Creating…"; result.className = "muted";
  try {
    const m = await api("POST", "/api/v1/machines", body);
    result.textContent = `Created ${m.AssetTag || m.asset_tag}`;
    result.className = "muted";
    e.target.reset();
    loadMachines();
  } catch (err) {
    result.textContent = err.message; result.className = "err";
  }
});

// --- profiles helper ---

async function populateProfileSelect(sel, allowEmpty = false) {
  const node = $(sel);
  if (!node) return;
  try {
    const profs = await api("GET", "/api/v1/profiles");
    node.innerHTML = (allowEmpty ? `<option value="">(none — assign later)</option>` : "")
      + profs.map(p => `<option value="${escape(p.id)}">${escape(p.name)}</option>`).join("");
    if (!profs.length) node.innerHTML = `<option value="">(no profiles configured)</option>`;
  } catch (e) {
    node.innerHTML = `<option value="">${escape(e.message)}</option>`;
  }
}

async function populateMachineSelect(sel) {
  const node = $(sel);
  try {
    const ms = await api("GET", "/api/v1/machines");
    node.innerHTML = ms.length
      ? ms.map(m => {
          const id = m.ID || m.id;
          const tag = m.AssetTag || m.asset_tag || trunc(id, 8);
          return `<option value="${escape(id)}">${escape(tag)}</option>`;
        }).join("")
      : `<option value="">(no machines registered yet)</option>`;
  } catch (e) {
    node.innerHTML = `<option value="">${escape(e.message)}</option>`;
  }
}

// --- deploy / issue code ---

async function loadDeployForm() {
  await Promise.all([
    populateMachineSelect("#deploy-machine-select"),
    populateProfileSelect("#deploy-profile-select"),
  ]);
}

$("#issue-form").addEventListener("submit", async e => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const ttl = parseInt(fd.get("ttl_hours") || "4", 10);
  const body = {
    machine_id: fd.get("machine_id"),
    profile_id: fd.get("profile_id"),
    ttl_seconds: ttl * 3600,
  };
  const issuedFor = fd.get("issued_for");
  if (issuedFor) body.issued_for = issuedFor;

  const code = $("#code-result");
  code.classList.remove("hidden");
  $("#issued-code").textContent = "…";
  $("#issued-expires").textContent = "…";
  try {
    const r = await api("POST", "/api/v1/deployments/issue", body);
    $("#issued-code").textContent = r.code || "—";
    $("#issued-expires").textContent = fmtTime(r.expires_at);
  } catch (err) {
    $("#issued-code").textContent = "ERROR";
    $("#issued-expires").textContent = err.message;
    code.style.borderColor = "var(--err)";
  }
});

// --- audit ---

async function loadAudit() {
  const fd = new FormData($("#audit-filters"));
  const params = new URLSearchParams();
  if (fd.get("since")) params.set("since", fd.get("since"));
  if (fd.get("action")) params.set("action", fd.get("action"));
  try {
    const events = await api("GET", "/api/v1/audit?" + params.toString());
    $("#audit-table tbody").innerHTML = events.length ? events.map(e => `
      <tr>
        <td class="mono">${fmtTime(e.at)}</td>
        <td>${escape(e.action)}</td>
        <td class="mono">${escape(e.actor_kind || "—")} ${escape(trunc(e.actor_id))}</td>
        <td class="mono">${escape(e.subject_kind || "")} ${escape(trunc(e.subject_id))}</td>
        <td class="mono">${escape(e.source_ip || "—")}</td>
      </tr>`).join("") : `<tr><td colspan="5" class="muted">No events match.</td></tr>`;
  } catch (e) { $("#audit-table tbody").innerHTML = `<tr><td colspan="5" class="err">${escape(e.message)}</td></tr>`; }
}
$("#audit-filters").addEventListener("submit", e => { e.preventDefault(); loadAudit(); });

// --- sticks ---

async function loadSticks() {
  try {
    const ss = await api("GET", "/api/v1/bootstrap-sticks");
    $("#sticks-table tbody").innerHTML = ss.length ? ss.map(s => `
      <tr>
        <td class="mono">${fmtTime(s.BuiltAt || s.built_at)}</td>
        <td class="mono">${escape(trunc(s.ImageSHA256 || s.image_sha256, 12))}</td>
        <td>${escape(s.Tailnet || s.tailnet)}</td>
        <td>${escape(s.DeployURL || s.deploy_url)}</td>
        <td class="mono">${escape(trunc(s.CAFingerprint || s.ca_fingerprint, 16))}</td>
        <td>${escape((s.Label && s.Label.String) || s.label || "—")}</td>
      </tr>`).join("") : `<tr><td colspan="6" class="muted">No sticks registered yet. Build one with <code>make-stick.sh</code> then <code>deployctl bootstrap-sticks register …</code>.</td></tr>`;
  } catch (e) { $("#sticks-table tbody").innerHTML = `<tr><td colspan="6" class="err">${escape(e.message)}</td></tr>`; }
}

// --- footer warn banner ---

(async () => {
  try {
    const r = await fetch("/api/v1/me");
    if (r.status === 200) {
      const me = await r.json();
      if (me.dev_mode) $("#warn-banner").classList.remove("hidden");
    } else if (r.status === 404) {
      // /me not present (older build) — treat as dev-mode warning suppressed
    }
  } catch { /* network error: ignore */ }
})();

// --- xss safety ---

function escape(s) {
  if (s === undefined || s === null) return "";
  return String(s).replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
}

// --- boot ---

const initial = (location.hash || "#dash").slice(1);
showTab(["dash","machines","deploy","audit","sticks"].includes(initial) ? initial : "dash");

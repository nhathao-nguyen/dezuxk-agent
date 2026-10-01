// Dezuxk AI Gateway - Embedded Dashboard & Playground Controller
const state = {
  token: localStorage.getItem("dezuxk_admin_token") || "",
  currentTab: "dashboard",
  overview: null,
  activeModels: [],
  isStreaming: false,
  abortController: null
};

// Utilities
function showToast(msg, type = "info") {
  const container = document.getElementById("toastContainer");
  if (!container) return;
  const toast = document.createElement("div");
  toast.className = `toast toast-${type}`;
  toast.innerHTML = `<span>${type === "error" ? "❌" : type === "success" ? "✅" : "ℹ️"}</span> <span>${escapeHtml(msg)}</span>`;
  container.appendChild(toast);
  setTimeout(() => {
    toast.style.opacity = "0";
    setTimeout(() => toast.remove(), 300);
  }, 4000);
}

function escapeHtml(str) {
  if (!str) return "";
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function extractErrorMessage(err, fallback = "Đã xảy ra lỗi không xác định") {
  if (!err) return fallback;
  if (typeof err === "string") return err;
  if (err.error && typeof err.error === "object" && err.error.message) {
    return err.error.message;
  }
  if (typeof err.error === "string") return err.error;
  if (err.message) return err.message;
  return fallback;
}

// Simple safe markdown formatter for AI chat outputs
function formatChatMarkdown(raw) {
  if (!raw) return "";

  // 1. Split code blocks (```lang ... ```)
  const codeBlockRegex = /```([a-zA-Z0-9_-]*)\n([\s\S]*?)```/g;
  let blocks = [];
  let lastIndex = 0;
  let match;

  let out = "";
  while ((match = codeBlockRegex.exec(raw)) !== null) {
    // Process text before code block
    const textBefore = raw.substring(lastIndex, match.index);
    out += formatInlineMarkdown(textBefore);

    // Format code block
    const lang = match[1] || "code";
    const codeContent = escapeHtml(match[2].trimEnd());
    out += `<div class="code-exec-box" style="margin:0.6rem 0;">
      <div class="code-exec-header" style="font-weight:600;">💻 ${escapeHtml(lang)}</div>
      <div class="code-exec-body" style="overflow-x:auto; white-space:pre; padding:0.6rem 0.8rem; font-size:0.82rem; color:#e2e8f0; background:#0f172a;">${codeContent}</div>
    </div>`;

    lastIndex = codeBlockRegex.lastIndex;
  }

  // Remainder
  out += formatInlineMarkdown(raw.substring(lastIndex));
  return out;
}

function formatInlineMarkdown(text) {
  let s = escapeHtml(text);
  // Inline code `code`
  s = s.replace(/`([^`]+)`/g, "<code style='background:rgba(255,255,255,0.08); padding:0.15rem 0.35rem; border-radius:3px; font-family:monospace;'>$1</code>");
  // Bold **text**
  s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  // Italic *text*
  s = s.replace(/\*([^*]+)\*/g, "<em>$1</em>");
  // Line breaks
  s = s.replace(/\n/g, "<br>");
  return s;
}

function authHeaders() {
  const h = { "Content-Type": "application/json" };
  if (state.token) {
    h["Authorization"] = "Bearer " + state.token;
  }
  return h;
}

// Authentication
function checkAuth() {
  if (!state.token) {
    showLoginModal();
  } else {
    fetchOverview();
  }
}

function showLoginModal() {
  const m = document.getElementById("loginModal");
  if (m) m.classList.add("active");
}

function hideLoginModal() {
  const m = document.getElementById("loginModal");
  if (m) m.classList.remove("active");
}

async function handleLogin(e) {
  if (e) e.preventDefault();
  const user = document.getElementById("loginUsername").value.trim();
  const pass = document.getElementById("loginPassword").value.trim();
  const tokenInput = document.getElementById("loginToken").value.trim();

  try {
    const payload = tokenInput ? { token: tokenInput } : { username: user, password: pass };
    const res = await fetch("/v1/admin/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    });

    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(extractErrorMessage(err, "Xác thực tài khoản quản trị thất bại"));
    }

    const data = await res.json();
    state.token = data.token || tokenInput || "dezuxk_secure_admin_session_token_2026";
    localStorage.setItem("dezuxk_admin_token", state.token);
    hideLoginModal();
    showToast("Đăng nhập quản trị Gateway thành công!", "success");
    fetchOverview();
  } catch (err) {
    showToast(err.message, "error");
  }
}

function handleLogout() {
  localStorage.removeItem("dezuxk_admin_token");
  state.token = "";
  fetch("/v1/admin/auth/logout", { method: "POST" }).catch(() => {});
  showToast("Đã đăng xuất khỏi phiên quản trị.", "info");
  showLoginModal();
}

// Tab Switching
function switchTab(tabId) {
  state.currentTab = tabId;
  document.querySelectorAll(".tab-btn").forEach(b => {
    b.classList.toggle("active", b.dataset.tab === tabId);
  });
  document.querySelectorAll(".tab-content").forEach(c => {
    c.classList.toggle("active", c.id === `tab-${tabId}`);
  });

  if (tabId === "keys") {
    loadApiKeys();
  } else if (tabId === "dashboard" || tabId === "profiles") {
    fetchOverview();
  }
}

// Fetch Overview Data
async function fetchOverview() {
  try {
    const res = await fetch("/v1/admin/overview", { headers: authHeaders() });
    if (res.status === 401 || res.status === 403) {
      showLoginModal();
      return;
    }
    const data = await res.json();
    state.overview = data;
    state.activeModels = data.models || [];
    renderDashboard(data);
    renderProfiles(data.accounts || []);
    renderPlaygroundModels();
  } catch (err) {
    showToast("Không thể tải thông tin Gateway Overview: " + err.message, "error");
  }
}

// Render Dashboard
function renderDashboard(data) {
  const accounts = data.accounts || [];
  const metrics = data.metrics || {};
  const cache = data.cache || {};
  const alerts = data.alerts || [];

  // Tổng số request đã qua Facade
  const totalReqs = metrics.total_requests != null ? metrics.total_requests : (metrics.requests_total || 0);
  const statRequestsEl = document.getElementById("statRequests");
  if (statRequestsEl) statRequestsEl.textContent = totalReqs.toLocaleString();

  const statModelsEl = document.getElementById("statModels");
  if (statModelsEl) statModelsEl.textContent = (data.models || []).length;

  const statAccountsEl = document.getElementById("statAccounts");
  if (statAccountsEl) statAccountsEl.textContent = accounts.length;

  // Cache Hit Rate
  const hitRatio = cache.hit_ratio != null ? cache.hit_ratio : (cache.hit_rate_pct != null ? cache.hit_rate_pct : 0);
  const statCacheEl = document.getElementById("statCache");
  if (statCacheEl) statCacheEl.textContent = `${hitRatio.toFixed(1)}%`;

  // Token Stats & 7-Day Chart
  const tokenUsages = data.token_usage || [];
  let totalSystemTokens = 0;
  let totalPromptTokens = 0;
  let totalCompTokens = 0;
  tokenUsages.forEach(u => {
    totalSystemTokens += (u.total_tokens || 0);
    totalPromptTokens += (u.prompt_tokens || 0);
    totalCompTokens += (u.completion_tokens || 0);
  });
  const statTokensEl = document.getElementById("statTokens");
  if (statTokensEl) statTokensEl.textContent = totalSystemTokens.toLocaleString();
  const statTokensDescEl = document.getElementById("statTokensDesc");
  if (statTokensDescEl) statTokensDescEl.textContent = `${totalPromptTokens.toLocaleString()} in / ${totalCompTokens.toLocaleString()} out`;
  renderTokenChart(tokenUsages);

  // Alerts Banner
  const alertBanner = document.getElementById("alertBanner");
  if (alertBanner) {
    if (alerts.length > 0) {
      alertBanner.classList.remove("hidden");
      document.getElementById("alertText").textContent = `Phát hiện ${alerts.length} cảnh báo phiên: ${alerts.map(a => `${a.account_id} (${a.reason})`).join(", ")}`;
    } else {
      alertBanner.classList.add("hidden");
    }
  }

  // Render Models Catalog
  const modelsTbody = document.getElementById("modelsTableBody");
  if (modelsTbody) {
    modelsTbody.innerHTML = "";
    const modelsList = data.models || [];
    if (modelsList.length === 0) {
      modelsTbody.innerHTML = `<tr><td colspan="5" style="text-align:center; color:var(--text-dim);">Chưa có mô hình nào sẵn sàng. Vui lòng đồng bộ tài khoản Google trong tab Profiles.</td></tr>`;
      return;
    }
    modelsList.forEach(m => {
      const tr = document.createElement("tr");
      const caps = m.capabilities || ["chat"];
      const hasCap = (c) => caps.includes(c);
      tr.innerHTML = `
        <td><strong>${escapeHtml(m.id)}</strong></td>
        <td>${escapeHtml(m.display_name || m.id)}</td>
        <td><span class="badge badge-primary">${escapeHtml(m.target_service)}</span></td>
        <td>
          <span class="badge ${hasCap("chat") ? "badge-primary" : "badge-dim"}">Chat</span>
          <span class="badge ${hasCap("vision") ? "badge-primary" : "badge-dim"}">Vision</span>
          <span class="badge ${hasCap("thinking") ? "badge-primary" : "badge-dim"}">Thinking</span>
          <span class="badge ${hasCap("tools") ? "badge-primary" : "badge-dim"}">Tools</span>
        </td>
        <td><span class="badge badge-success">Sẵn sàng</span></td>
      `;
      modelsTbody.appendChild(tr);
    });
  }
}

// Render 7-Day Token Usage Chart
function renderTokenChart(tokenUsages) {
  const container = document.getElementById("tokenChartBars");
  if (!container) return;

  if (!tokenUsages || tokenUsages.length === 0) {
    container.innerHTML = `<div style="width:100%; text-align:center; color:var(--text-dim); padding:2rem;">Chưa có dữ liệu giao dịch token... Hãy gửi câu hỏi đầu tiên từ Playground!</div>`;
    return;
  }

  // Sắp xếp ngày tăng dần (từ cũ đến mới nhất)
  const sorted = [...tokenUsages].sort((a, b) => a.date.localeCompare(b.date));
  let maxTotal = 1;
  sorted.forEach(u => {
    if (u.total_tokens > maxTotal) maxTotal = u.total_tokens;
  });

  container.innerHTML = "";
  sorted.forEach(u => {
    const col = document.createElement("div");
    col.className = "chart-col";

    const promptPct = Math.min(100, Math.round((u.prompt_tokens / maxTotal) * 100));
    const compPct = Math.min(100, Math.round((u.completion_tokens / maxTotal) * 100));

    // Cắt chuỗi ngày định dạng mm-dd
    const dateLabel = u.date.length >= 5 ? u.date.substring(5) : u.date;

    col.innerHTML = `
      <div class="chart-val-hint" title="${u.prompt_tokens.toLocaleString()} prompt + ${u.completion_tokens.toLocaleString()} completion">${(u.total_tokens || 0).toLocaleString()}</div>
      <div class="chart-bar-wrap" title="${u.date}: ${u.total_tokens.toLocaleString()} tokens (${u.request_count || 1} requests)">
        <div class="chart-bar-prompt" style="height:${promptPct}%"></div>
        <div class="chart-bar-completion" style="height:${compPct}%"></div>
      </div>
      <div class="chart-label">${dateLabel}</div>
    `;
    container.appendChild(col);
  });
}

// Render Profiles Tab
function renderProfiles(accounts) {
  const tbody = document.getElementById("profilesTableBody");
  if (!tbody) return;
  tbody.innerHTML = "";
  if (accounts.length === 0) {
    tbody.innerHTML = `<tr><td colspan="6" style="text-align:center; color:var(--text-dim);">Chưa có Profile Google nào. Hãy bấm "Tạo Profile Mới".</td></tr>`;
    return;
  }

  accounts.forEach(acc => {
    const tr = document.createElement("tr");
    const isLive = acc.is_healthy && acc.has_gemini;
    const statusBadge = isLive 
      ? `<span class="badge badge-success">🟢 Sống (${acc.gemini_status || "Active"})</span>`
      : `<span class="badge badge-danger">🔴 ${escapeHtml(acc.gemini_status || "Chờ đồng bộ CDP")}</span>`;

    tr.innerHTML = `
      <td><strong>${escapeHtml(acc.id)}</strong></td>
      <td>${escapeHtml(acc.email || "Chưa đồng bộ cookie")}</td>
      <td><span class="badge badge-primary">${escapeHtml(acc.tier || "Free")}</span></td>
      <td>${statusBadge}</td>
      <td>${escapeHtml(acc.proxy || "Trực tiếp")}</td>
      <td>
        <button class="btn btn-secondary btn-sm" onclick="launchChrome('${escapeHtml(acc.id)}')">🚀 Mở Chrome</button>
        <button class="btn btn-primary btn-sm" onclick="syncCDP('${escapeHtml(acc.id)}')">🔄 Đồng bộ CDP</button>
        <button class="btn btn-secondary btn-sm" onclick="openProxyModal('${escapeHtml(acc.id)}')">🌐 Proxy</button>
      </td>
    `;
    tbody.appendChild(tr);
  });
}

// Actions: Profiles
async function launchChrome(profileId) {
  showToast(`Đang mở Chrome cho profile ${profileId}...`, "info");
  try {
    const res = await fetch(`/v1/profiles/${profileId}/launch`, { method: "POST", headers: authHeaders() });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(extractErrorMessage(err, "Mở Chrome thất bại"));
    }
    showToast(`Đã mở Chrome cho ${profileId}. Vui lòng đăng nhập Google rồi bấm Đồng bộ CDP.`, "success");
  } catch (err) {
    showToast(err.message, "error");
  }
}

async function syncCDP(profileId) {
  showToast(`Đang trích xuất cookie qua WebSocket CDP cho ${profileId}...`, "info");
  try {
    const res = await fetch(`/v1/profiles/${profileId}/sync`, { method: "POST", headers: authHeaders() });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(extractErrorMessage(err, "Đồng bộ CDP thất bại"));
    }
    showToast(`Đồng bộ CDP thành công cho profile ${profileId}!`, "success");
    fetchOverview();
  } catch (err) {
    showToast(err.message, "error");
  }
}

function openCreateProfileModal() {
  document.getElementById("createProfileModal").classList.add("active");
}

async function handleCreateProfile(e) {
  e.preventDefault();
  const id = document.getElementById("newProfileId").value.trim();
  const proxy = document.getElementById("newProfileProxy").value.trim();
  if (!id) return;

  try {
    const res = await fetch("/v1/profiles", {
      method: "POST",
      headers: authHeaders(),
      body: JSON.stringify({ id: id, profile_id: id, proxy: proxy })
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(extractErrorMessage(err, "Tạo profile thất bại"));
    }
    document.getElementById("createProfileModal").classList.remove("active");
    document.getElementById("newProfileId").value = "";
    document.getElementById("newProfileProxy").value = "";
    showToast(`Đã tạo Profile ${id} thành công!`, "success");
    fetchOverview();
  } catch (err) {
    showToast(err.message, "error");
  }
}

let activeProxyProfileId = "";
function openProxyModal(profileId) {
  activeProxyProfileId = profileId;
  document.getElementById("proxyProfileIdLabel").textContent = profileId;
  document.getElementById("setProxyModal").classList.add("active");
}

async function handleSetProxy(e) {
  e.preventDefault();
  const proxy = document.getElementById("proxyServerInput").value.trim();
  try {
    const res = await fetch(`/v1/profiles/${activeProxyProfileId}/proxy`, {
      method: "PUT",
      headers: authHeaders(),
      body: JSON.stringify({ proxy: proxy })
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(extractErrorMessage(err, "Lỗi cập nhật Proxy"));
    }
    document.getElementById("setProxyModal").classList.remove("active");
    showToast(`Đã cập nhật Proxy cho ${activeProxyProfileId}`, "success");
    fetchOverview();
  } catch (err) {
    showToast(err.message, "error");
  }
}

// Render API Keys Tab
async function loadApiKeys() {
  const tbody = document.getElementById("keysTableBody");
  if (!tbody) return;
  try {
    const res = await fetch("/v1/admin/keys", { headers: authHeaders() });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(extractErrorMessage(err, "Không thể tải danh sách Virtual Keys"));
    }
    const data = await res.json();
    tbody.innerHTML = "";
    const keys = data.keys || [];
    if (keys.length === 0) {
      tbody.innerHTML = `<tr><td colspan="9" style="text-align:center; color:var(--text-dim);">Chưa có Virtual Key nào. Hãy bấm "Tạo Key Mới".</td></tr>`;
      return;
    }

    keys.forEach(k => {
      const tr = document.createElement("tr");
      const isRevoked = !k.is_active;
      const statusBadge = isRevoked 
        ? `<span class="badge badge-danger">Đã thu hồi</span>`
        : `<span class="badge badge-success">Hoạt động</span>`;

      const tokensTotal = (k.total_tokens || 0).toLocaleString();
      const promptIn = (k.prompt_tokens_total || 0).toLocaleString();
      const compOut = (k.completion_tokens_total || 0).toLocaleString();
      const maxTokensStr = (k.max_token_quota && k.max_token_quota > 0) ? `${k.max_token_quota.toLocaleString()} tokens` : "Vô hạn";

      tr.innerHTML = `
        <td><strong>${escapeHtml(k.name || "Default Key")}</strong></td>
        <td><code>${escapeHtml(k.key_prefix || "sk-dez...")}***</code></td>
        <td><span class="badge ${k.role === 'admin' ? 'badge-primary' : 'badge-dim'}">${escapeHtml(k.role)}</span></td>
        <td>${k.rate_limit_rpm > 0 ? k.rate_limit_rpm + " RPM" : "Không giới hạn"}</td>
        <td>${k.daily_quota_requests > 0 ? k.daily_quota_requests + " reqs/ngày" : "Vô hạn"}</td>
        <td>
          <strong style="color:var(--primary);">${tokensTotal}</strong>
          <span style="font-size:0.72rem; color:var(--text-muted); display:block;">(${promptIn} in / ${compOut} out)</span>
        </td>
        <td>${maxTokensStr}</td>
        <td>${statusBadge}</td>
        <td>
          ${!isRevoked ? `<button class="btn btn-danger btn-sm" onclick="revokeKey('${escapeHtml(k.id)}')">Thu hồi</button>` : ""}
        </td>
      `;
      tbody.appendChild(tr);
    });
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="9" style="text-align:center; color:var(--danger);">${escapeHtml(err.message)}</td></tr>`;
    showToast(err.message, "error");
  }
}

function openCreateKeyModal() {
  document.getElementById("createKeyModal").classList.add("active");
}

async function handleCreateKey(e) {
  e.preventDefault();
  const name = document.getElementById("keyName").value.trim();
  const role = document.getElementById("keyRole").value;
  const rpm = parseInt(document.getElementById("keyRPM").value, 10) || 0;
  const quota = parseInt(document.getElementById("keyQuota").value, 10) || 0;
  const maxTokens = parseInt(document.getElementById("keyMaxTokens") ? document.getElementById("keyMaxTokens").value : "0", 10) || 0;

  try {
    const res = await fetch("/v1/admin/keys", {
      method: "POST",
      headers: authHeaders(),
      body: JSON.stringify({
        name: name,
        role: role,
        rate_limit_rpm: rpm,
        daily_quota_requests: quota,
        max_token_quota: maxTokens
      })
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(extractErrorMessage(err, "Không thể tạo API Key"));
    }
    const data = await res.json();
    document.getElementById("createKeyModal").classList.remove("active");
    
    // Show Created Key Modal for 1-click copy
    const rawKeyVal = data.key || data.raw_key || "";
    document.getElementById("createdRawKey").value = rawKeyVal;
    document.getElementById("createdKeyModal").classList.add("active");
    loadApiKeys();
  } catch (err) {
    showToast(err.message, "error");
  }
}

async function revokeKey(keyId) {
  if (!confirm("Bạn có chắc chắn muốn thu hồi Virtual Key này không?")) return;
  try {
    const res = await fetch(`/v1/admin/keys/${keyId}`, { method: "DELETE", headers: authHeaders() });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(extractErrorMessage(err, "Thu hồi key thất bại"));
    }
    showToast("Đã thu hồi Virtual Key thành công.", "success");
    loadApiKeys();
  } catch (err) {
    showToast(err.message, "error");
  }
}

function copyCreatedKey() {
  const input = document.getElementById("createdRawKey");
  if (!input || !input.value) {
    showToast("Không tìm thấy key để sao chép.", "error");
    return;
  }
  navigator.clipboard.writeText(input.value).then(() => {
    showToast("Đã sao chép API Key vào clipboard!", "success");
  }).catch(() => {
    input.select();
    document.execCommand("copy");
    showToast("Đã sao chép API Key vào clipboard!", "success");
  });
}

// AI Playground
function renderPlaygroundModels() {
  const select = document.getElementById("playgroundModel");
  if (!select) return;
  const currentVal = select.value;
  select.innerHTML = "";
  if (state.activeModels.length === 0) {
    select.innerHTML = `<option value="gemini-3.8-flash">gemini-3.8-flash (Chờ đăng nhập)</option>`;
    return;
  }

  let hasDefault = false;
  state.activeModels.forEach(m => {
    const opt = document.createElement("option");
    opt.value = m.id;
    opt.textContent = `${m.display_name || m.id} (${m.id})`;
    if (m.id === "gemini-3.8-flash") {
      opt.selected = true;
      hasDefault = true;
    } else if (!hasDefault && currentVal && m.id === currentVal) {
      opt.selected = true;
    }
    select.appendChild(opt);
  });
}

function insertSamplePrompt(text) {
  const el = document.getElementById("playgroundPrompt");
  if (el) {
    el.value = text;
    el.focus();
  }
}

function stopStreamingChat() {
  if (state.isStreaming && state.abortController) {
    state.abortController.abort();
    state.isStreaming = false;
    showToast("Đã dừng tạo phản hồi.", "info");
  }
}

async function sendPlaygroundChat() {
  const btnSend = document.getElementById("btnSendChat");
  if (state.isStreaming) {
    stopStreamingChat();
    return;
  }

  const promptInput = document.getElementById("playgroundPrompt");
  const prompt = promptInput.value.trim();
  if (!prompt) return;

  const model = document.getElementById("playgroundModel").value;
  const isThinking = document.getElementById("toggleThinking").checked;
  const reasoningEffort = document.getElementById("selectReasoningEffort") ? document.getElementById("selectReasoningEffort").value : "";
  const isGrounding = document.getElementById("toggleGrounding").checked;
  const isCode = document.getElementById("toggleCode").checked;

  const chatHistory = document.getElementById("chatHistory");

  // User Message
  const userBubble = document.createElement("div");
  userBubble.className = "chat-bubble user";
  userBubble.textContent = prompt;
  chatHistory.appendChild(userBubble);
  promptInput.value = "";

  // Assistant Message Placeholder
  const asstBubble = document.createElement("div");
  asstBubble.className = "chat-bubble assistant";

  // Thinking Box inside Assistant Message (will be attached dynamically when thinking tokens arrive)
  let thinkingBox = null;
  let thinkingContentEl = null;

  const textBody = document.createElement("div");
  textBody.className = "chat-text-body";
  textBody.innerHTML = "<em>Đang kết nối Gemini Gateway...</em>";
  asstBubble.appendChild(textBody);
  chatHistory.appendChild(asstBubble);
  chatHistory.scrollTop = chatHistory.scrollHeight;

  state.isStreaming = true;
  state.abortController = new AbortController();

  if (btnSend) {
    btnSend.textContent = "⏹️ Dừng";
    btnSend.classList.add("btn-danger");
    btnSend.classList.remove("btn-primary");
  }

  try {
    const payload = {
      model: model,
      stream: true,
      thinking: isThinking,
      grounding: isGrounding,
      code_interpreter: isCode,
      messages: [{ role: "user", content: prompt }]
    };
    if (reasoningEffort) {
      payload.reasoning_effort = reasoningEffort;
    }

    const res = await fetch("/v1/chat/completions", {
      method: "POST",
      headers: authHeaders(),
      body: JSON.stringify(payload),
      signal: state.abortController.signal
    });

    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(extractErrorMessage(err, `HTTP ${res.status}`));
    }

    const reader = res.body.getReader();
    const decoder = new TextDecoder("utf-8");
    let buffer = "";
    let fullText = "";
    let fullReasoning = "";
    let sources = [];
    let codeExecs = [];

    textBody.innerHTML = "";

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop(); // keep partial line

      for (const line of lines) {
        const trimmed = line.trim();
        if (!trimmed.startsWith("data: ")) continue;
        const dataPart = trimmed.substring(6).trim();
        if (dataPart === "[DONE]") break;

        try {
          const chunk = JSON.parse(dataPart);
          const choice = chunk.choices && chunk.choices[0];
          if (choice && choice.delta) {
            // Real-time Thinking stream
            if (choice.delta.reasoning_content) {
              fullReasoning += choice.delta.reasoning_content;
              if (!thinkingBox) {
                thinkingBox = document.createElement("div");
                thinkingBox.className = "thinking-box";
                thinkingBox.innerHTML = `
                  <div class="thinking-title" onclick="this.nextElementSibling.classList.toggle('hidden')">
                    <span>🧠 Quá trình suy luận (Thinking Stream${reasoningEffort ? ': ' + reasoningEffort : ''})</span>
                  </div>
                  <div class="thinking-content"></div>
                `;
                thinkingContentEl = thinkingBox.querySelector(".thinking-content");
                asstBubble.insertBefore(thinkingBox, textBody);
              }
              if (thinkingContentEl) {
                thinkingContentEl.textContent = fullReasoning;
              }
            }
            // Real-time Text stream
            if (choice.delta.content) {
              fullText += choice.delta.content;
              textBody.innerHTML = formatChatMarkdown(fullText);
            }
          }
          // Grounding Metadata
          if (chunk.grounding && chunk.grounding.sources) {
            sources = chunk.grounding.sources;
          }
          // Code Executions
          if (chunk.code_executions && chunk.code_executions.length > 0) {
            codeExecs = chunk.code_executions;
          }
        } catch (e) {
          // ignore partial json
        }
      }
      chatHistory.scrollTop = chatHistory.scrollHeight;
    }

    if (!fullText && !fullReasoning) {
      textBody.innerHTML = "<em>(Mô hình phản hồi rỗng)</em>";
    }

    // Render Citations & Sources if present
    if (sources && sources.length > 0) {
      const citBox = document.createElement("div");
      citBox.className = "citations-box";
      sources.forEach(src => {
        const a = document.createElement("a");
        a.className = "citation-chip";
        a.href = src.url;
        a.target = "_blank";
        a.rel = "noopener noreferrer";
        a.innerHTML = `🌐 ${escapeHtml(src.domain || src.title || "Nguồn")}`;
        citBox.appendChild(a);
      });
      asstBubble.appendChild(citBox);
    }

    // Render Code Executions if present
    if (codeExecs && codeExecs.length > 0) {
      codeExecs.forEach(ce => {
        const ceBox = document.createElement("div");
        ceBox.className = "code-exec-box";
        ceBox.innerHTML = `
          <div class="code-exec-header">🐍 Python Sandbox Execution (${escapeHtml(ce.language || "python")})</div>
          <div class="code-exec-body">${escapeHtml(ce.code || "")}</div>
          ${ce.stdout ? `<div class="code-exec-stdout">Output:\n${escapeHtml(ce.stdout)}</div>` : ""}
        `;
        asstBubble.appendChild(ceBox);
      });
    }

  } catch (err) {
    if (err.name === "AbortError") {
      textBody.innerHTML += `<div style="color:var(--text-muted); font-size:0.8rem; margin-top:0.4rem;"><em>[Đã dừng tạo phản hồi bởi người dùng]</em></div>`;
    } else {
      textBody.innerHTML = `<span style="color:var(--danger)">Lỗi suy luận: ${escapeHtml(err.message)}</span>`;
    }
  } finally {
    state.isStreaming = false;
    state.abortController = null;
    if (btnSend) {
      btnSend.textContent = "Gửi";
      btnSend.classList.remove("btn-danger");
      btnSend.classList.add("btn-primary");
    }
    chatHistory.scrollTop = chatHistory.scrollHeight;
  }
}

// Event Listeners
document.addEventListener("DOMContentLoaded", () => {
  checkAuth();

  document.querySelectorAll(".tab-btn").forEach(btn => {
    btn.addEventListener("click", () => switchTab(btn.dataset.tab));
  });

  const promptInput = document.getElementById("playgroundPrompt");
  if (promptInput) {
    promptInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        sendPlaygroundChat();
      }
    });
  }

  // Toggle Thinking Effort wrapper visibility
  const toggleThinkingEl = document.getElementById("toggleThinking");
  const reasoningWrapper = document.getElementById("reasoningEffortWrapper");
  if (toggleThinkingEl && reasoningWrapper) {
    toggleThinkingEl.addEventListener("change", () => {
      reasoningWrapper.style.display = toggleThinkingEl.checked ? "block" : "none";
    });
  }

  // Polling update overview every 5s
  setInterval(() => {
    if (state.token && state.currentTab === "dashboard") {
      fetchOverview();
    }
  }, 5000);
});

// Dezuxk Gateway Admin Dashboard Application (Vanilla ES6)
(function () {
  let overviewData = null;
  let activeTab = "accounts";
  let pollInterval = null;

  // DOM Elements
  const tabBtns = document.querySelectorAll(".tab-btn");
  const tabPanes = document.querySelectorAll(".tab-pane");
  const toastContainer = document.getElementById("toast-container");
  const loginModal = document.getElementById("login-modal");
  const proxyModal = document.getElementById("proxy-modal");
  const loginForm = document.getElementById("login-form");
  const proxyForm = document.getElementById("proxy-form");

  // Init
  document.addEventListener("DOMContentLoaded", () => {
    setupTabs();
    setupModals();
    fetchOverview();
  });

  function startPolling() {
    if (pollInterval) clearInterval(pollInterval);
    pollInterval = setInterval(fetchOverview, 4000);
  }

  function stopPolling() {
    if (pollInterval) {
      clearInterval(pollInterval);
      pollInterval = null;
    }
  }


  function setupTabs() {
    tabBtns.forEach((btn) => {
      btn.addEventListener("click", () => {
        const tab = btn.getAttribute("data-tab");
        activeTab = tab;
        tabBtns.forEach((b) => b.classList.remove("active"));
        tabPanes.forEach((p) => p.classList.remove("active"));
        btn.classList.add("active");
        const pane = document.getElementById(`tab-${tab}`);
        if (pane) pane.classList.add("active");
        if (tab === "metrics") {
          renderCanvasChart();
        }
      });
    });
  }

  function setupModals() {
    loginForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      const token = document.getElementById("login-token").value.trim();
      const user = document.getElementById("login-user").value.trim();
      const pass = document.getElementById("login-pass").value.trim();

      try {
        const res = await fetch("/v1/admin/auth/login", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ token, username: user, password: pass }),
        });
        if (!res.ok) {
          const err = await res.json();
          showToast(err.error || "Đăng nhập thất bại", "error");
          return;
        }
        showToast("Đăng nhập thành công!", "success");
        loginModal.classList.remove("open");
        isLoggingIn = false;
        fetchOverview();
        startPolling();
      } catch (err) {
        showToast("Lỗi mạng: " + err.message, "error");
      }
    });

    proxyForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      const profileId = document.getElementById("proxy-profile-id").value;
      const proxyUrl = document.getElementById("proxy-url").value.trim();

      try {
        const res = await fetch(`/v1/profiles/${encodeURIComponent(profileId)}/proxy`, {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ proxy: proxyUrl }),
        });
        if (!res.ok) {
          const err = await res.json();
          showToast(err.error || "Lỗi lưu proxy", "error");
          return;
        }
        showToast("Đã lưu Proxy vào Secret Vault!", "success");
        proxyModal.classList.remove("open");
        fetchOverview();
      } catch (err) {
        showToast("Lỗi cập nhật proxy: " + err.message, "error");
      }
    });

    document.querySelectorAll(".close-modal").forEach((btn) => {
      btn.addEventListener("click", () => {
        loginModal.classList.remove("open");
        proxyModal.classList.remove("open");
      });
    });
  }

  let isLoggingIn = false;

  async function fetchOverview() {
    if (isLoggingIn) return;
    try {
      const res = await fetch("/v1/admin/overview");
      if (res.status === 401) {
        isLoggingIn = true;
        stopPolling();
        loginModal.classList.add("open");
        return;
      }
      if (!res.ok) return;

      const data = await res.json();
      overviewData = data;
      renderOverview();
      startPolling();
    } catch (err) {
      console.warn("[Admin UI] Lỗi tải overview:", err);
    }
  }



  function renderOverview() {
    if (!overviewData) return;

    // Header Server Status
    const accountsCount = (overviewData.accounts || []).length;
    document.getElementById("stat-accounts-count").innerText = accountsCount;
    document.getElementById("stat-total-reqs").innerText = (overviewData.metrics?.total_requests || 0).toLocaleString();
    document.getElementById("stat-rpm").innerText = overviewData.metrics?.rpm || 0;

    // Cache Stats
    const cache = overviewData.cache || {};
    document.getElementById("stat-cache-ratio").innerText = `${(cache.hit_ratio || 0).toFixed(1)}%`;
    document.getElementById("stat-cache-detail").innerText = `Hits: ${cache.hits || 0} | Misses: ${cache.misses || 0} (RAM: ${cache.total_entries || 0}/${cache.max_entries || 0})`;

    // Drift Alerts
    const schemaDrift = overviewData.metrics?.schema_unexpected || 0;
    const unmapped = overviewData.metrics?.unmapped_fields || 0;
    document.getElementById("stat-drift").innerText = schemaDrift + unmapped;

    renderAccountsTable(overviewData.accounts || []);
    renderModelsTable(overviewData.models || []);
    renderDriftTable(overviewData.metrics || {}, overviewData.alerts || []);
    if (activeTab === "metrics") {
      renderCanvasChart();
    }
  }

  function renderAccountsTable(accounts) {
    const tbody = document.getElementById("accounts-tbody");
    if (!tbody) return;

    if (accounts.length === 0) {
      tbody.innerHTML = `<tr><td colspan="7" style="text-align:center;color:var(--text-dim);padding:2rem;">Chưa có profile tài khoản Google nào. Vui lòng tạo profile qua API hoặc nạp cookie.</td></tr>`;
      return;
    }

    tbody.innerHTML = accounts
      .map((acc) => {
        let statusBadge = `<span class="badge badge-ready">Ready</span>`;
        if (acc.flow_status === "invalid" || acc.gemini_status === "invalid") {
          statusBadge = `<span class="badge badge-invalid">Invalid / Expired</span>`;
        } else if (acc.flow_status === "refreshing" || acc.gemini_status === "refreshing") {
          statusBadge = `<span class="badge badge-refreshing">Refreshing</span>`;
        }

        const proxyText = acc.proxy ? `<span style="font-family:monospace;font-size:0.8rem;color:var(--accent-cyan);">${escapeHtml(acc.proxy)}</span>` : `<span style="color:var(--text-dim);font-style:italic;">Direct (Không proxy)</span>`;
        const tierBadge = `<span class="badge badge-tier">${acc.tier || "Free"}</span>`;

        return `
          <tr>
            <td>
              <strong style="color:var(--text-main);">${escapeHtml(acc.id)}</strong>
              <div style="font-size:0.8rem;color:var(--text-dim);">${escapeHtml(acc.email || "Chưa có email")}</div>
            </td>
            <td>${tierBadge}</td>
            <td>${statusBadge}</td>
            <td>${proxyText}</td>
            <td><strong style="color:var(--accent-green);font-size:1.05rem;">${acc.credits}</strong> credits</td>
            <td>
              <div style="font-size:0.8rem;">Gemini: ${acc.has_gemini ? "✅ Có" : "❌ Thiếu"}</div>
              <div style="font-size:0.8rem;">Flow: ${acc.has_flow ? "✅ Có" : "❌ Thiếu"}</div>
            </td>
            <td>
              <div style="display:flex;gap:0.4rem;flex-wrap:wrap;">
                <button class="btn btn-primary btn-sm" onclick="window.launchChrome('${acc.id}')">
                  🌐 Mở Chrome
                </button>
                <button class="btn btn-success btn-sm" onclick="window.syncCDP('${acc.id}')">
                  ⚡ Sync CDP
                </button>
                <button class="btn btn-warning btn-sm" onclick="window.openProxyModal('${acc.id}', '${encodeURIComponent(acc.proxy || "")}')">
                  ⚙️ Proxy
                </button>
              </div>
            </td>
          </tr>
        `;
      })
      .join("");
  }

  function renderModelsTable(models) {
    const tbody = document.getElementById("models-tbody");
    if (!tbody) return;

    if (models.length === 0) {
      tbody.innerHTML = `<tr><td colspan="5" style="text-align:center;color:var(--text-dim);padding:2rem;">Chưa có mô hình nào online. Vui lòng đăng nhập tài khoản để kích hoạt.</td></tr></tr>`;
      return;
    }

    tbody.innerHTML = models
      .map((m) => {
        const status = m.is_active ? `<span class="badge badge-ready">Online (GPU Ready)</span>` : `<span class="badge badge-invalid">Offline</span>`;
        return `
          <tr>
            <td><strong>${escapeHtml(m.id)}</strong></td>
            <td>${escapeHtml(m.display_name)}</td>
            <td><span class="badge badge-tier">${escapeHtml(m.target_service)}</span></td>
            <td>${escapeHtml(m.internal_backend_id || "-")}</td>
            <td>${status}</td>
          </tr>
        `;
      })
      .join("");
  }

  function renderDriftTable(metrics, alerts) {
    const alertsBox = document.getElementById("alerts-container");
    if (!alertsBox) return;

    let html = "";
    if (alerts && alerts.length > 0) {
      html += `
        <div style="margin-bottom:1.5rem;">
          <h4 style="color:var(--accent-rose);margin-bottom:0.75rem;">🚨 Cảnh báo phiên (Session Alerts)</h4>
          <div style="display:flex;flex-direction:column;gap:0.5rem;">
            ${alerts
              .map(
                (a) => `
              <div style="background:rgba(244,63,94,0.1);border-left:3px solid var(--accent-rose);padding:0.75rem 1rem;border-radius:var(--radius-sm);font-size:0.85rem;">
                <strong>[${escapeHtml(a.account_id)}]</strong> ${escapeHtml(a.message || a.reason || "")}
                <div style="font-size:0.75rem;color:var(--text-dim);margin-top:0.25rem;">Thời gian: ${a.timestamp || ""}</div>
              </div>
            `
              )
              .join("")}
          </div>
        </div>
      `;
    }

    const schemaCount = metrics.schema_unexpected || 0;
    const unmappedCount = metrics.unmapped_fields || 0;
    const classes = metrics.classes || {};

    html += `
      <div style="display:grid;grid-template-columns:repeat(auto-fit, minmax(200px, 1fr));gap:1rem;">
        <div style="background:var(--bg-surface);padding:1rem;border-radius:var(--radius-sm);border:1px solid var(--border-color);">
          <div style="font-size:0.8rem;color:var(--text-muted);">Schema Unexpected</div>
          <div style="font-size:1.5rem;font-weight:700;color:${schemaCount > 0 ? "var(--accent-rose)" : "var(--accent-green)"};">${schemaCount}</div>
        </div>
        <div style="background:var(--bg-surface);padding:1rem;border-radius:var(--radius-sm);border:1px solid var(--border-color);">
          <div style="font-size:0.8rem;color:var(--text-muted);">Unmapped Fields</div>
          <div style="font-size:1.5rem;font-weight:700;color:${unmappedCount > 0 ? "var(--accent-amber)" : "var(--accent-green)"};">${unmappedCount}</div>
        </div>
        <div style="background:var(--bg-surface);padding:1rem;border-radius:var(--radius-sm);border:1px solid var(--border-color);">
          <div style="font-size:0.8rem;color:var(--text-muted);">Error Classes Phân loại</div>
          <div style="font-size:0.85rem;color:var(--text-main);margin-top:0.4rem;">
            ${Object.keys(classes).length > 0 ? Object.entries(classes).map(([k, v]) => `<div>${k}: <strong>${v}</strong></div>`).join("") : "Chưa có lỗi"}
          </div>
        </div>
      </div>
    `;


    alertsBox.innerHTML = html;
  }

  function renderCanvasChart() {
    const canvas = document.getElementById("rpmChart");
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    const dpr = window.devicePixelRatio || 1;

    const width = canvas.parentElement.clientWidth;
    const height = 180;
    canvas.width = width * dpr;
    canvas.height = height * dpr;
    ctx.scale(dpr, dpr);

    const history = overviewData?.metrics?.rpm_history || [];
    const points = history.length === 60 ? history : new Array(60).fill(0);

    ctx.clearRect(0, 0, width, height);

    // Tìm max value
    const maxVal = Math.max(10, ...points);
    const stepX = width / (points.length - 1);

    // Vẽ vùng Gradient
    const gradient = ctx.createLinearGradient(0, 0, 0, height);
    gradient.addColorStop(0, "rgba(6, 182, 212, 0.35)");
    gradient.addColorStop(1, "rgba(6, 182, 212, 0.0)");

    ctx.beginPath();
    ctx.moveTo(0, height);
    for (let i = 0; i < points.length; i++) {
      const x = i * stepX;
      const y = height - (points[i] / maxVal) * (height - 30) - 10;
      ctx.lineTo(x, y);
    }
    ctx.lineTo(width, height);
    ctx.closePath();
    ctx.fillStyle = gradient;
    ctx.fill();

    // Vẽ đường Line
    ctx.beginPath();
    for (let i = 0; i < points.length; i++) {
      const x = i * stepX;
      const y = height - (points[i] / maxVal) * (height - 30) - 10;
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.strokeStyle = "#06b6d4";
    ctx.lineWidth = 2.5;
    ctx.stroke();

    // Vẽ grid lines mờ
    ctx.strokeStyle = "rgba(255, 255, 255, 0.06)";
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(0, height / 2);
    ctx.lineTo(width, height / 2);
    ctx.moveTo(0, 10);
    ctx.lineTo(width, 10);
    ctx.stroke();
  }

  // Window actions
  window.launchChrome = async (id) => {
    showToast(`Đang khởi động Chrome cho profile ${id}...`, "success");
    try {
      const res = await fetch(`/v1/profiles/${encodeURIComponent(id)}/launch`, { method: "POST" });
      const data = await res.json();
      if (!res.ok) {
        showToast(data.error || "Không thể mở Chrome", "error");
        return;
      }
      showToast(data.message || "Chrome đã mở! Vui lòng đăng nhập Google.", "success");
    } catch (err) {
      showToast("Lỗi kết nối: " + err.message, "error");
    }
  };

  window.syncCDP = async (id) => {
    showToast(`Đang kết nối CDP cào cookie cho profile ${id}...`, "success");
    try {
      const res = await fetch(`/v1/profiles/${encodeURIComponent(id)}/sync`, { method: "POST" });
      const data = await res.json();
      if (!res.ok) {
        showToast(data.error || "Đồng bộ cookie CDP thất bại", "error");
        return;
      }
      showToast(data.message || "Đồng bộ cookie thành công!", "success");
      fetchOverview();
    } catch (err) {
      showToast("Lỗi kết nối: " + err.message, "error");
    }
  };

  window.openProxyModal = (id, proxyEnc) => {
    document.getElementById("proxy-profile-id").value = id;
    document.getElementById("proxy-url").value = decodeURIComponent(proxyEnc);
    proxyModal.classList.add("open");
  };

  window.logout = async () => {
    await fetch("/v1/admin/auth/logout", { method: "POST" });
    showToast("Đã đăng xuất.", "success");
    isLoggingIn = true;
    loginModal.classList.add("open");
  };


  function showToast(msg, type = "success") {
    const toast = document.createElement("div");
    toast.className = `toast ${type}`;
    toast.innerText = msg;
    toastContainer.appendChild(toast);
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
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#039;");
  }
})();

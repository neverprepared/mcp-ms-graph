document.addEventListener("DOMContentLoaded", () => {
  const profileEl = document.getElementById("profile");
  const toggleEl = document.getElementById("toggle");
  const relayStatusEl = document.getElementById("relayStatus");
  const saveBtn = document.getElementById("save");
  const saveMsgEl = document.getElementById("saveMsg");
  const settingsBtn = document.getElementById("settingsBtn");
  const backBtn = document.getElementById("backBtn");
  const mainContent = document.getElementById("mainContent");
  const settingsView = document.getElementById("settingsView");
  const keyStatusEl = document.getElementById("keyStatus");
  const keyDisplayEl = document.getElementById("keyDisplay");
  const generateKeyBtn = document.getElementById("generateKey");
  const enterKeyBtn = document.getElementById("enterKey");
  const keyInputRow = document.getElementById("keyInputRow");
  const keyInput = document.getElementById("keyInput");
  const setKeyBtn = document.getElementById("setKey");
  const peopleSearch = document.getElementById("peopleSearch");

  let accessToken = null;

  // --- Tabs ---
  document.querySelectorAll(".tab").forEach((tab) => {
    tab.addEventListener("click", () => {
      document.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"));
      document.querySelectorAll(".tab-pane").forEach((p) => p.classList.remove("active"));
      tab.classList.add("active");
      document.getElementById("tab-" + tab.dataset.tab).classList.add("active");
    });
  });

  // --- Settings view ---
  settingsBtn.addEventListener("click", () => {
    mainContent.classList.add("hide");
    settingsView.classList.add("show");
    refreshKeyStatus();
  });
  backBtn.addEventListener("click", () => {
    mainContent.classList.remove("hide");
    settingsView.classList.remove("show");
  });

  // --- Load token and populate ---
  chrome.storage.local.get("latestToken", async (data) => {
    if (!data.latestToken?.accessToken) return;
    accessToken = data.latestToken.accessToken;

    // Always show token expiry, even if API calls fail
    const tokenInfo = getTokenExpiry(accessToken);
    if (tokenInfo.html) {
      profileEl.innerHTML = `<div class="not-connected">Loading...</div>${tokenInfo.html}`;
    }

    loadProfile();
    loadCalendar();
    loadFiles();
  });

  // --- Profile ---
  async function loadProfile() {
    try {
      const me = await graphGet("/me");
      if (!me) {
        const tokenInfo = getTokenExpiry(accessToken);
        profileEl.innerHTML =
          `<div class="not-connected">Could not load profile — token may be expired</div>${tokenInfo.html}`;
        return;
      }

      let tenantName = "";
      let tenantId = "";
      try {
        const org = await graphGet("/organization");
        if (org?.value?.length > 0) {
          tenantName = org.value[0].displayName;
          tenantId = org.value[0].id;
        }
      } catch (e) {}

      const email = me.mail || me.userPrincipalName || "";
      const parts = [];
      if (me.jobTitle) parts.push(esc(me.jobTitle));
      if (me.department) parts.push(esc(me.department));

      const tokenInfo = getTokenExpiry(accessToken);

      profileEl.innerHTML = `
        <div class="name">${esc(me.displayName)}</div>
        <div class="meta">${esc(email)}${parts.length ? " · " + parts.join(" · ") : ""}</div>
        ${tenantName ? `<div class="tenant" title="${esc(tenantId)}">${esc(tenantName)}${tenantId ? ` · ${esc(tenantId)}` : ""}</div>` : ""}
        ${tokenInfo.html}
      `;
    } catch (e) {
      const tokenInfo = getTokenExpiry(accessToken);
      profileEl.innerHTML =
        `<div class="not-connected">Could not load profile</div>${tokenInfo.html}`;
    }
  }

  // --- Calendar ---
  async function loadCalendar() {
    const meetingsEl = document.getElementById("meetings");
    try {
      const now = new Date().toISOString();
      const end = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
      const data = await graphGet(
        `/me/calendarView?startDateTime=${now}&endDateTime=${end}&$orderby=start/dateTime&$top=10&$select=subject,start,end,location,onlineMeeting,webLink,isOnlineMeeting,onlineMeetingUrl`
      );
      if (!data?.value?.length) {
        meetingsEl.innerHTML = '<div class="empty">No upcoming meetings today</div>';
        return;
      }
      meetingsEl.innerHTML = data.value.map((m) => {
        const start = new Date(m.start.dateTime + "Z");
        const end = new Date(m.end.dateTime + "Z");
        const timeStr = formatTime(start) + " – " + formatTime(end);
        const location = m.location?.displayName || "";
        const joinUrl = m.onlineMeeting?.joinUrl || m.onlineMeetingUrl || "";
        return `
          <div class="meeting">
            <div class="time">${timeStr}</div>
            <div class="subject">${esc(m.subject)}</div>
            ${location ? `<div class="location">${esc(location)}</div>` : ""}
            ${joinUrl ? `<a href="${esc(joinUrl)}" target="_blank">Join meeting</a>` : ""}
          </div>
        `;
      }).join("");
    } catch (e) {
      meetingsEl.innerHTML = '<div class="empty">Could not load calendar</div>';
    }
  }

  // --- People search ---
  let searchTimeout = null;
  peopleSearch.addEventListener("input", () => {
    clearTimeout(searchTimeout);
    const query = peopleSearch.value.trim();
    if (!query) {
      document.getElementById("peopleResults").innerHTML =
        '<div class="empty">Type a name to search</div>';
      return;
    }
    searchTimeout = setTimeout(() => searchPeople(query), 300);
  });

  async function searchPeople(query) {
    const resultsEl = document.getElementById("peopleResults");
    resultsEl.innerHTML = '<div class="empty">Searching...</div>';
    try {
      const safeQuery = encodeURIComponent(query.replace(/'/g, "''"));
      const data = await graphGet(
        `/users?$filter=startswith(displayName,'${safeQuery}') or startswith(mail,'${safeQuery}')&$top=10&$select=id,displayName,mail,jobTitle,department,userPrincipalName`
      );
      if (!data?.value?.length) {
        // Try people API as fallback
        const people = await graphGet(`/me/people?$search="${encodeURIComponent(query)}"&$top=10`);
        if (!people?.value?.length) {
          resultsEl.innerHTML = '<div class="empty">No results</div>';
          return;
        }
        renderPeople(people.value, resultsEl);
        return;
      }

      // Get presence for all users
      const userIds = data.value.map((u) => u.id);
      let presenceMap = {};
      try {
        const presenceData = await graphPost(
          "/communications/getPresences",
          { ids: userIds }
        );
        if (presenceData?.value) {
          presenceData.value.forEach((p) => { presenceMap[p.id] = p.availability; });
        }
      } catch (e) {}

      resultsEl.innerHTML = data.value.map((u) => {
        const initials = (u.displayName || "?").split(" ").map((n) => n[0]).join("").substring(0, 2).toUpperCase();
        const presence = presenceMap[u.id] || "unknown";
        const email = u.mail || u.userPrincipalName || "";
        return `
          <div class="person">
            <div class="avatar">${initials}</div>
            <div class="info">
              <div class="pname"><span class="presence-dot ${presenceClass(presence)}"></span>${esc(u.displayName)}</div>
              ${u.jobTitle ? `<div class="ptitle">${esc(u.jobTitle)}</div>` : ""}
              ${email ? `<div class="pemail">${esc(email)}</div>` : ""}
            </div>
          </div>
        `;
      }).join("");
    } catch (e) {
      resultsEl.innerHTML = '<div class="empty">Search failed</div>';
    }
  }

  function renderPeople(people, container) {
    container.innerHTML = people.map((p) => {
      const initials = (p.displayName || "?").split(" ").map((n) => n[0]).join("").substring(0, 2).toUpperCase();
      const email = p.scoredEmailAddresses?.[0]?.address || "";
      return `
        <div class="person">
          <div class="avatar">${initials}</div>
          <div class="info">
            <div class="pname">${esc(p.displayName)}</div>
            ${p.jobTitle ? `<div class="ptitle">${esc(p.jobTitle)}</div>` : ""}
            ${email ? `<div class="pemail">${esc(email)}</div>` : ""}
          </div>
        </div>
      `;
    }).join("");
  }

  // --- Files ---
  async function loadFiles() {
    const filesEl = document.getElementById("recentFiles");
    try {
      const data = await graphGet(
        "/me/drive/recent?$top=10&$select=name,webUrl,lastModifiedDateTime,lastModifiedBy"
      );
      if (!data?.value?.length) {
        filesEl.innerHTML = '<div class="empty">No recent files</div>';
        return;
      }
      filesEl.innerHTML = data.value.map((f) => {
        const modified = new Date(f.lastModifiedDateTime);
        const modBy = f.lastModifiedBy?.user?.displayName || "";
        return `
          <div class="file">
            <a href="${esc(f.webUrl)}" target="_blank">${esc(f.name)}</a>
            <div class="fmeta">${formatRelative(modified)}${modBy ? " · " + esc(modBy) : ""}</div>
          </div>
        `;
      }).join("");
    } catch (e) {
      filesEl.innerHTML = '<div class="empty">Could not load files</div>';
    }
  }

  // --- Sync toggle ---
  chrome.runtime.sendMessage({ type: "GET_DECRYPTED_CONFIG" }, (data) => {
    if (data?.config) {
      document.getElementById("ablyKey").value = data.config.ablyApiKey || "";
      document.getElementById("ablyChannel").value = data.config.ablyChannel || "";
      document.getElementById("interval").value = (data.config.relayIntervalMs || 30000) / 1000;
    }
  });
  chrome.storage.local.get("relayEnabled", (data) => {
    setToggle(data.relayEnabled !== false);
  });

  chrome.runtime.sendMessage({ type: "GET_STATUS" }, (response) => {
    if (!response) return;
    setToggle(response.enabled !== false);
    updateRelayStatus(response);
  });

  toggleEl.addEventListener("click", () => {
    const newState = !toggleEl.classList.contains("on");
    setToggle(newState);
    chrome.runtime.sendMessage({ type: "SET_ENABLED", enabled: newState }, (response) => {
      updateRelayStatus({ enabled: response?.enabled, configured: true });
    });
  });

  function setToggle(on) { toggleEl.classList.toggle("on", on); }

  function updateRelayStatus(status) {
    let text = "";
    if (!status.enabled) {
      text = "Off";
    } else if (!status.configured) {
      text = "Not configured";
    } else if (status.lastRelay) {
      const ago = Math.round((Date.now() - status.lastRelay) / 1000);
      text = `Last sync: ${ago}s ago`;
    } else {
      text = "Active";
    }
    if (status.hasRefreshToken) {
      text += " · Auto-refresh ✓";
    }
    relayStatusEl.textContent = text;
  }

  // --- Encryption key ---
  function refreshKeyStatus() {
    chrome.storage.session.get("encryptionKey", (data) => {
      if (data.encryptionKey) {
        keyStatusEl.textContent = "Key active for this session";
        keyStatusEl.className = "key-status active";
      } else {
        keyStatusEl.textContent = "No key — generate or enter one";
        keyStatusEl.className = "key-status missing";
      }
    });
  }

  generateKeyBtn.addEventListener("click", () => {
    chrome.runtime.sendMessage({ type: "GENERATE_KEY" }, (response) => {
      if (response?.key) {
        keyDisplayEl.innerHTML =
          esc(response.key) +
          '<div class="hint">Click to copy. Save this key — you\'ll need it for the CLI.</div>';
        keyDisplayEl.classList.add("show");
        keyInputRow.classList.remove("show");
        refreshKeyStatus();
        keyDisplayEl.onclick = () => {
          navigator.clipboard.writeText(response.key).then(() => {
            keyDisplayEl.querySelector(".hint").textContent = "Copied!";
            setTimeout(() => {
              keyDisplayEl.querySelector(".hint").textContent =
                "Click to copy. Save this key — you'll need it for the CLI.";
            }, 2000);
          });
        };
      }
    });
  });

  enterKeyBtn.addEventListener("click", () => {
    keyInputRow.classList.toggle("show");
    keyDisplayEl.classList.remove("show");
  });

  setKeyBtn.addEventListener("click", () => {
    const key = keyInput.value.trim();
    if (!key) return;
    chrome.runtime.sendMessage({ type: "SET_KEY", key }, (response) => {
      if (response?.status === "saved") {
        keyInput.value = "";
        keyInputRow.classList.remove("show");
        refreshKeyStatus();
      }
    });
  });

  // --- Token actions ---
  const refreshTokenBtn = document.getElementById("refreshToken");
  const clearTokensBtn = document.getElementById("clearTokens");
  const clearMsgEl = document.getElementById("clearMsg");

  refreshTokenBtn.addEventListener("click", () => {
    clearMsgEl.textContent = "Refreshing...";
    clearMsgEl.className = "save-msg success";
    chrome.runtime.sendMessage({ type: "DO_REFRESH" }, (response) => {
      if (response?.refreshed) {
        clearMsgEl.textContent = "Token refreshed";
        clearMsgEl.className = "save-msg success";
      } else {
        clearMsgEl.textContent = "Refresh failed — no refresh token or expired";
        clearMsgEl.className = "save-msg error";
      }
      setTimeout(() => { clearMsgEl.className = "save-msg"; }, 3000);
    });
  });

  clearTokensBtn.addEventListener("click", () => {
    chrome.runtime.sendMessage({ type: "CLEAR_TOKENS" }, (response) => {
      if (response?.status === "cleared") {
        clearMsgEl.textContent = "All tokens cleared";
        clearMsgEl.className = "save-msg success";
        setTimeout(() => { clearMsgEl.className = "save-msg"; }, 3000);
      }
    });
  });

  // --- Save config ---
  saveBtn.addEventListener("click", () => {
    const config = {
      ablyApiKey: document.getElementById("ablyKey").value.trim(),
      ablyChannel: document.getElementById("ablyChannel").value.trim(),
      relayIntervalMs: parseInt(document.getElementById("interval").value) * 1000,
    };
    if (!config.ablyApiKey || !config.ablyChannel) {
      showSaveMsg("Service key and channel are required", "error");
      return;
    }
    chrome.runtime.sendMessage({ type: "SAVE_CONFIG", config }, (response) => {
      if (response?.status === "saved") {
        showSaveMsg("Settings saved", "success");
        setTimeout(hideSaveMsg, 3000);
      } else {
        showSaveMsg("Failed to save", "error");
      }
    });
  });

  function showSaveMsg(text, type) {
    saveMsgEl.textContent = text;
    saveMsgEl.className = "save-msg " + type;
  }
  function hideSaveMsg() { saveMsgEl.className = "save-msg"; }

  // --- Graph helpers ---
  async function graphGet(path) {
    if (!accessToken) return null;
    const resp = await fetch("https://graph.microsoft.com/v1.0" + path, {
      headers: { Authorization: `Bearer ${accessToken}` },
    });
    if (!resp.ok) return null;
    return resp.json();
  }

  async function graphPost(path, body) {
    if (!accessToken) return null;
    const resp = await fetch("https://graph.microsoft.com/v1.0" + path, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${accessToken}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    });
    if (!resp.ok) return null;
    return resp.json();
  }

  function presenceClass(availability) {
    const map = {
      Available: "available", Busy: "busy", DoNotDisturb: "dnd",
      Away: "away", BeRightBack: "away", Offline: "offline",
    };
    return map[availability] || "unknown";
  }

  function formatTime(date) {
    return date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  }

  function formatRelative(date) {
    const diff = Date.now() - date.getTime();
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return "just now";
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    const days = Math.floor(hours / 24);
    return `${days}d ago`;
  }

  function getTokenExpiry(token) {
    try {
      const parts = token.split(".");
      if (parts.length !== 3) return { html: "" };
      let payload = parts[1].replace(/-/g, "+").replace(/_/g, "/");
      while (payload.length % 4) payload += "=";
      const claims = JSON.parse(atob(payload));
      if (!claims.exp) return { html: "" };

      const expiryMs = claims.exp * 1000;
      const remaining = expiryMs - Date.now();

      if (remaining <= 0) {
        return { html: '<div class="token-expiry expired">Token expired</div>' };
      }

      const mins = Math.round(remaining / 60000);
      let cls = "ok";
      if (mins < 5) cls = "expired";
      else if (mins < 15) cls = "warn";

      const label = mins >= 60
        ? `${Math.floor(mins / 60)}h ${mins % 60}m remaining`
        : `${mins}m remaining`;

      return { html: `<div class="token-expiry ${cls}">Token: ${label}</div>` };
    } catch (e) {
      return { html: "" };
    }
  }

  function esc(s) {
    if (!s) return "";
    const el = document.createElement("span");
    el.textContent = s;
    return el.innerHTML;
  }
});

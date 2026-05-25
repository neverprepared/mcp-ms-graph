// Background service worker.
// Captures tokens, encrypts with session-only key, publishes via Ably.
// Can refresh access tokens independently using a captured refresh token.

importScripts("crypto.js");

let currentToken = null;
let relayInterval = null;
let relayEnabled = true;
let cachedConfig = null; // decrypted config held in memory only
const DEFAULT_RELAY_INTERVAL_MS = 30 * 1000;

// Decrypt config from storage using the session encryption key
async function loadConfig() {
  if (cachedConfig) return cachedConfig;

  const [localData, sessionData] = await Promise.all([
    chrome.storage.local.get(["configEncrypted", "configMeta", "config"]),
    chrome.storage.session.get("encryptionKey"),
  ]);

  // Migration: if old plaintext config exists, use it
  if (localData.config?.ablyApiKey && !localData.configEncrypted) {
    cachedConfig = localData.config;
    return cachedConfig;
  }

  if (!localData.configEncrypted || !sessionData.encryptionKey) {
    return null;
  }

  try {
    const decrypted = await teamsCLICrypto.decrypt(localData.configEncrypted, sessionData.encryptionKey);
    const sensitive = JSON.parse(decrypted);
    cachedConfig = {
      ...sensitive,
      relayIntervalMs: localData.configMeta?.relayIntervalMs || DEFAULT_RELAY_INTERVAL_MS,
    };
    return cachedConfig;
  } catch (err) {
    console.error("[sync] config decryption failed:", err);
    return null;
  }
}

// Graph Explorer's client ID
const CLIENT_ID = "de8bc8b5-d9f9-48b1-a8ad-b748da725064";

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message.type === "TOKEN_CAPTURED") {
    currentToken = message.payload;
    chrome.storage.local.set({ latestToken: currentToken });
    if (relayEnabled) {
      relayToken();
    }
    sendResponse({ status: "received" });
  }

  if (message.type === "REFRESH_TOKEN_CAPTURED") {
    chrome.storage.session.set({
      refreshToken: message.payload.refreshToken,
      refreshTokenTime: Date.now(),
    });
    console.log("[sync] refresh token stored in session");
    // Also relay the refresh token to the CLI
    if (relayEnabled) {
      relayRefreshToken(message.payload.refreshToken);
    }
    sendResponse({ status: "stored" });
  }

  if (message.type === "HAS_REFRESH_TOKEN") {
    chrome.storage.session.get("refreshToken", (data) => {
      sendResponse({ hasRefreshToken: !!data.refreshToken });
    });
    return true;
  }

  if (message.type === "DO_REFRESH") {
    refreshAccessToken().then((success) => {
      sendResponse({ refreshed: success });
    });
    return true;
  }

  if (message.type === "GET_STATUS") {
    (async () => {
      const config = await loadConfig();
      const [localData, sessionData] = await Promise.all([
        chrome.storage.local.get(["latestToken", "lastRelayTime", "relayEnabled"]),
        chrome.storage.session.get(["encryptionKey", "refreshToken"]),
      ]);
      sendResponse({
        configured: !!(config?.ablyApiKey && sessionData.encryptionKey),
        hasToken: !!localData.latestToken,
        hasRefreshToken: !!sessionData.refreshToken,
        lastRelay: localData.lastRelayTime || null,
        channelName: config?.ablyChannel || null,
        enabled: localData.relayEnabled !== false,
        hasKey: !!sessionData.encryptionKey,
      });
    })();
    return true;
  }

  if (message.type === "GET_DECRYPTED_CONFIG") {
    loadConfig().then((config) => {
      sendResponse({ config: config || null });
    });
    return true;
  }

  if (message.type === "SAVE_CONFIG") {
    // Encrypt sensitive config with the encryption key
    chrome.storage.session.get("encryptionKey", async (sessionData) => {
      const encKey = sessionData.encryptionKey;
      if (!encKey) {
        sendResponse({ status: "error", message: "No encryption key — generate or enter one first" });
        return;
      }

      try {
        const sensitiveData = JSON.stringify({
          ablyApiKey: message.config.ablyApiKey,
          ablyChannel: message.config.ablyChannel,
        });
        const encrypted = await teamsCLICrypto.encrypt(sensitiveData, encKey);

        chrome.storage.local.set({
          configEncrypted: encrypted,
          configMeta: { relayIntervalMs: message.config.relayIntervalMs },
        }, () => {
          // Cache decrypted config in memory for runtime use
          cachedConfig = {
            ablyApiKey: message.config.ablyApiKey,
            ablyChannel: message.config.ablyChannel,
            relayIntervalMs: message.config.relayIntervalMs,
          };
          if (relayEnabled) {
            startRelayLoop();
          }
          sendResponse({ status: "saved" });
        });
      } catch (err) {
        console.error("[sync] config encryption failed:", err);
        sendResponse({ status: "error", message: err.message });
      }
    });
    return true;
  }

  if (message.type === "GENERATE_KEY") {
    const key = crypto.randomUUID() + "-" + crypto.randomUUID();
    cachedConfig = null; // invalidate — new key means re-decrypt
    chrome.storage.session.set({ encryptionKey: key }, () => {
      sendResponse({ key });
    });
    return true;
  }

  if (message.type === "SET_KEY") {
    cachedConfig = null; // invalidate — new key means re-decrypt
    chrome.storage.session.set({ encryptionKey: message.key }, () => {
      // Try to decrypt existing config with the new key
      loadConfig().then(() => {
        sendResponse({ status: "saved" });
      });
    });
    return true;
  }

  if (message.type === "CLEAR_TOKENS") {
    currentToken = null;
    cachedConfig = null;
    chrome.storage.local.remove(["latestToken", "configEncrypted", "configMeta", "config"]);
    chrome.storage.session.remove(["refreshToken", "refreshTokenTime"]);
    console.log("[sync] all tokens and config cleared");
    sendResponse({ status: "cleared" });
    return false;
  }

  if (message.type === "SET_ENABLED") {
    relayEnabled = message.enabled;
    chrome.storage.local.set({ relayEnabled });
    if (relayEnabled) {
      startRelayLoop();
    } else {
      stopRelayLoop();
    }
    sendResponse({ enabled: relayEnabled });
    return false;
  }
});

// === Token refresh ===

async function refreshAccessToken() {
  const sessionData = await chrome.storage.session.get("refreshToken");
  const refreshToken = sessionData.refreshToken;
  if (!refreshToken) {
    console.log("[sync] no refresh token available");
    return false;
  }

  // Determine tenant from the current access token
  const tenant = getTenantFromToken();

  try {
    const response = await fetch(
      `https://login.microsoftonline.com/${tenant}/oauth2/v2.0/token`,
      {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({
          client_id: CLIENT_ID,
          grant_type: "refresh_token",
          refresh_token: refreshToken,
          scope: "Chat.Read Chat.ReadWrite User.Read offline_access",
        }),
      }
    );

    if (!response.ok) {
      const text = await response.text();
      console.error("[sync] refresh failed:", response.status, text);
      // If refresh token is expired/revoked, clear it
      if (response.status === 400 || response.status === 401) {
        chrome.storage.session.remove("refreshToken");
        console.log("[sync] refresh token cleared — user needs to re-authenticate");
      }
      return false;
    }

    const data = await response.json();

    // Update access token
    currentToken = {
      accessToken: data.access_token,
      timestamp: Date.now(),
    };
    chrome.storage.local.set({ latestToken: currentToken });
    console.log("[sync] access token refreshed");

    // Update refresh token if a new one was issued
    if (data.refresh_token) {
      chrome.storage.session.set({
        refreshToken: data.refresh_token,
        refreshTokenTime: Date.now(),
      });
      console.log("[sync] refresh token rotated");
    }

    // Relay the new token
    if (relayEnabled) {
      relayToken();
    }

    return true;
  } catch (err) {
    console.error("[sync] refresh error:", err);
    return false;
  }
}

function getTenantFromToken() {
  try {
    if (!currentToken?.accessToken) return "common";
    const parts = currentToken.accessToken.split(".");
    if (parts.length !== 3) return "common";
    let payload = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    while (payload.length % 4) payload += "=";
    const claims = JSON.parse(atob(payload));
    return claims.tid || "common";
  } catch (e) {
    return "common";
  }
}

function getTokenExpiry() {
  try {
    if (!currentToken?.accessToken) return 0;
    const parts = currentToken.accessToken.split(".");
    if (parts.length !== 3) return 0;
    let payload = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    while (payload.length % 4) payload += "=";
    const claims = JSON.parse(atob(payload));
    return (claims.exp || 0) * 1000;
  } catch (e) {
    return 0;
  }
}

// Proactively refresh before expiry
async function checkAndRefresh() {
  const expiry = getTokenExpiry();
  if (!expiry) return;

  const remaining = expiry - Date.now();
  // Refresh when less than 5 minutes remaining
  if (remaining > 0 && remaining < 5 * 60 * 1000) {
    const sessionData = await chrome.storage.session.get("refreshToken");
    if (sessionData.refreshToken) {
      console.log("[sync] proactive refresh — token expires in", Math.round(remaining / 60000) + "m");
      await refreshAccessToken();
    }
  }
}

// === Relay ===

async function relayToken() {
  if (!currentToken || !relayEnabled) return;

  const config = await loadConfig();
  const sessionData = await chrome.storage.session.get("encryptionKey");
  const key = sessionData.encryptionKey;

  if (!config?.ablyApiKey || !key || !config?.ablyChannel) {
    console.log("[sync] not configured — skipping");
    return;
  }

  try {
    const encrypted = await teamsCLICrypto.encrypt(currentToken.accessToken, key);

    const response = await fetch(
      `https://rest.ably.io/channels/${encodeURIComponent(config.ablyChannel)}/messages`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Basic ${btoa(config.ablyApiKey)}`,
        },
        body: JSON.stringify({
          name: "token",
          data: encrypted,
        }),
      }
    );

    if (response.ok) {
      chrome.storage.local.set({ lastRelayTime: Date.now() });
      console.log("[sync] published");
    } else {
      const text = await response.text();
      console.error("[sync] publish failed:", response.status, text);
    }
  } catch (err) {
    console.error("[sync] error:", err);
  }
}

async function relayRefreshToken(refreshToken) {
  const config = await loadConfig();
  const sessionData = await chrome.storage.session.get("encryptionKey");
  const key = sessionData.encryptionKey;

  if (!config?.ablyApiKey || !key || !config?.ablyChannel) {
    console.log("[sync] cannot relay refresh token — not configured");
    return;
  }

  try {
    const encrypted = await teamsCLICrypto.encrypt(refreshToken, key);
    const response = await fetch(
      `https://rest.ably.io/channels/${encodeURIComponent(config.ablyChannel)}/messages`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Basic ${btoa(config.ablyApiKey)}`,
        },
        body: JSON.stringify({
          name: "refresh_token",
          data: encrypted,
        }),
      }
    );
    if (response.ok) {
      console.log("[sync] refresh token relayed");
    }
  } catch (err) {
    console.error("[sync] refresh token relay error:", err);
  }
}

function startRelayLoop() {
  loadConfig().then((config) => {
    const intervalMin = (config?.relayIntervalMs || DEFAULT_RELAY_INTERVAL_MS) / 60000;
    chrome.alarms.create("relayToken", { periodInMinutes: Math.max(intervalMin, 0.15) });
  });
}

function stopRelayLoop() {
  chrome.alarms.clear("relayToken");
}

// Resume on service worker startup
chrome.storage.local.get(["latestToken", "relayEnabled"], (data) => {
  if (data.latestToken) {
    currentToken = data.latestToken;
  }
  relayEnabled = data.relayEnabled !== false;
  if (relayEnabled) {
    startRelayLoop();
  }
});

// Use chrome.alarms instead of setInterval — survives service worker restarts
chrome.alarms.create("checkRefresh", { periodInMinutes: 1 });
chrome.alarms.create("checkRefreshRequests", { delayInMinutes: 0.15, periodInMinutes: 0.15 }); // ~10 seconds

chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === "checkRefresh") {
    checkAndRefresh();
  }
  if (alarm.name === "checkRefreshRequests") {
    checkForRefreshRequests();
  }
  if (alarm.name === "relayToken") {
    relayToken();
  }
});

let lastRefreshRequestCheck = Date.now();

async function checkForRefreshRequests() {
  const config = await loadConfig();
  const sessionData = await chrome.storage.session.get("refreshToken");
  if (!config?.ablyApiKey || !config?.ablyChannel || !sessionData.refreshToken) return;

  try {
    const since = new Date(lastRefreshRequestCheck).toISOString();
    const response = await fetch(
      `https://rest.ably.io/channels/${encodeURIComponent(config.ablyChannel)}/messages?limit=5&direction=backwards&start=${encodeURIComponent(since)}`,
      {
        headers: { Authorization: `Basic ${btoa(config.ablyApiKey)}` },
      }
    );
    lastRefreshRequestCheck = Date.now();
    if (!response.ok) return;
    const messages = await response.json();
    for (const msg of messages) {
      if (msg.name === "refresh_request") {
        console.log("[sync] refresh request received from CLI");
        const success = await refreshAccessToken();
        if (success) console.log("[sync] refresh completed for CLI request");
        return;
      }
    }
  } catch (e) {}
}

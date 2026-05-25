// Token capture via three strategies:
// 1. MAIN world script injection — intercepts fetch/XHR to token endpoint,
//    captures both access + refresh tokens before MSAL encrypts them
// 2. DOM scraping of Access Token tab — fallback
// 3. Page reload on expiry — last resort if no refresh token

(function () {
  "use strict";

  console.log("[msg] content script loaded");

  let lastToken = null;
  let tokenExpiry = 0;

  // === Strategy 1: Inject fetch/XHR wrapper into page context ===

  const script = document.createElement("script");
  script.src = chrome.runtime.getURL("injector.js");
  script.onload = () => script.remove();
  (document.head || document.documentElement).appendChild(script);

  // Listen for messages from the injected script
  window.addEventListener("message", (event) => {
    if (event.source !== window) return;
    if (event.data?.type !== "__MSG_TOKEN__") return;

    const { accessToken, refreshToken } = event.data;

    if (accessToken && accessToken !== lastToken) {
      lastToken = accessToken;
      tokenExpiry = parseJwtExpiry(accessToken);
      const expiresIn = tokenExpiry ? Math.round((tokenExpiry - Date.now()) / 60000) : "?";
      console.log("[msg] token intercepted via network, expires in:", expiresIn + "m");

      chrome.runtime.sendMessage({
        type: "TOKEN_CAPTURED",
        payload: {
          accessToken: accessToken,
          timestamp: Date.now(),
        },
      });
    }

    if (refreshToken) {
      console.log("[msg] refresh token captured, length:", refreshToken.length);
      chrome.runtime.sendMessage({
        type: "REFRESH_TOKEN_CAPTURED",
        payload: { refreshToken: refreshToken },
      });
    }
  });

  // === Strategy 2: DOM scraping fallback ===

  function findToken() {
    const buttons = document.querySelectorAll("button");
    for (let i = 0; i < buttons.length; i++) {
      if (buttons[i].getAttribute("aria-label") === "Copy") {
        const container = buttons[i].parentElement?.parentElement;
        if (!container) continue;
        const text = container.innerText;
        const token = text.split("\n").find((l) => l.startsWith("eyJ"));
        if (token && token.length > 100) {
          return token.trim();
        }
      }
    }
    return null;
  }

  function checkForToken() {
    const token = findToken();
    if (token && token !== lastToken) {
      lastToken = token;
      tokenExpiry = parseJwtExpiry(token);
      const expiresIn = tokenExpiry ? Math.round((tokenExpiry - Date.now()) / 60000) : "?";
      console.log("[msg] token captured via DOM, expires in:", expiresIn + "m");
      chrome.runtime.sendMessage({
        type: "TOKEN_CAPTURED",
        payload: {
          accessToken: token,
          timestamp: Date.now(),
        },
      });
    }
  }

  // === Strategy 3: Expiry check + page reload fallback ===

  function checkExpiry() {
    if (!tokenExpiry) return;
    const remaining = tokenExpiry - Date.now();
    if (remaining <= 0) {
      chrome.runtime.sendMessage({ type: "HAS_REFRESH_TOKEN" }, (response) => {
        if (response?.hasRefreshToken) {
          console.log("[msg] token expired, background worker will refresh");
          chrome.runtime.sendMessage({ type: "DO_REFRESH" });
        } else {
          console.log("[msg] token expired, no refresh token, reloading page");
          chrome.storage.local.set({ pendingTokenCapture: true });
          location.reload();
        }
      });
    }
  }

  async function handlePostReload() {
    const data = await chrome.storage.local.get("pendingTokenCapture");
    if (!data.pendingTokenCapture) return;

    chrome.storage.local.remove("pendingTokenCapture");
    console.log("[msg] post-reload: waiting for page to settle");

    setTimeout(() => {
      const buttons = document.querySelectorAll("button");
      for (const btn of buttons) {
        if (btn.textContent.trim() === "Access token") {
          btn.click();
          console.log("[msg] clicked Access token tab");
          setTimeout(checkForToken, 2000);
          return;
        }
      }
    }, 5000);
  }

  // === Helpers ===

  function parseJwtExpiry(token) {
    try {
      const parts = token.split(".");
      if (parts.length !== 3) return 0;
      let payload = parts[1].replace(/-/g, "+").replace(/_/g, "/");
      while (payload.length % 4) payload += "=";
      const claims = JSON.parse(atob(payload));
      return (claims.exp || 0) * 1000;
    } catch (e) {
      return 0;
    }
  }

  // Poll DOM every 5 seconds (fallback)
  setInterval(checkForToken, 5000);

  // Check expiry every minute
  setInterval(checkExpiry, 60 * 1000);

  // Initial setup
  setTimeout(checkForToken, 3000);
  handlePostReload();
})();

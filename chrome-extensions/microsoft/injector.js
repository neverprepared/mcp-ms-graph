// Injected into the MAIN world to intercept fetch/XHR before MSAL loads.
// Communicates with the content script via window.postMessage.

(function () {
  "use strict";

  // Wrap fetch
  const originalFetch = window.fetch;
  window.fetch = function (...args) {
    return originalFetch.apply(this, args).then((response) => {
      try {
        const url = typeof args[0] === "string" ? args[0] : args[0]?.url || "";
        if (url.includes("login.microsoftonline.com") && url.includes("/token")) {
          response.clone().json().then((data) => {
            if (data.access_token) {
              window.postMessage({
                type: "__MSG_TOKEN__",
                accessToken: data.access_token,
                refreshToken: data.refresh_token || null,
              }, "*");
            }
          }).catch(() => {});
        }
      } catch (e) {}
      return response;
    });
  };

  // Wrap XHR
  const originalOpen = XMLHttpRequest.prototype.open;
  const originalSend = XMLHttpRequest.prototype.send;

  XMLHttpRequest.prototype.open = function (method, url, ...rest) {
    this._msLensUrl = url;
    return originalOpen.call(this, method, url, ...rest);
  };

  XMLHttpRequest.prototype.send = function (...args) {
    if (this._msLensUrl &&
        this._msLensUrl.includes("login.microsoftonline.com") &&
        this._msLensUrl.includes("/token")) {
      this.addEventListener("load", function () {
        try {
          const data = JSON.parse(this.responseText);
          if (data.access_token) {
            window.postMessage({
              type: "__MSG_TOKEN__",
              accessToken: data.access_token,
              refreshToken: data.refresh_token || null,
            }, "*");
          }
        } catch (e) {}
      });
    }
    return originalSend.apply(this, args);
  };
})();

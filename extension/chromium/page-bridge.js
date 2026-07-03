(function installSellicoPageBridge() {
  if (window.__sellicoPageBridgeInstalled) {
    return;
  }
  window.__sellicoPageBridgeInstalled = true;
  const REQUEST_BODY_LIMIT = 50_000;
  const RESPONSE_BODY_LIMIT = 200_000;
  const AUTO_PROBE_REQUEST_LIMIT = 16;
  const bridgeNonce = document.currentScript?.dataset?.sellicoNonce || "";
  const AUTO_PROBE_PATHS = [
    "/adv/v1/promotion/count",
    "/api/advert/v2/adverts",
    "/adv/v1/budget"
  ];

  function truncateBody(body) {
    if (!body) return null;
    const str = typeof body === "string" ? body : JSON.stringify(body);
    if (!str || str.length <= REQUEST_BODY_LIMIT) return body;
    return { _sellico_truncated: true, _sellico_original_size: str.length };
  }

  function emit(detail) {
    window.postMessage({
      source: "sellico-page-bridge",
      nonce: bridgeNonce,
      detail
    }, "*");
  }

  function truncateText(text, limit = RESPONSE_BODY_LIMIT) {
    if (!text || text.length <= limit) return text;
    return {
      _sellico_truncated: true,
      _sellico_original_size: text.length
    };
  }

  function parseMaybeJSON(text, contentType) {
    if (!text) return null;
    if (contentType.includes("application/json")) {
      try {
        return JSON.parse(text);
      } catch (_error) {
        return {
          _sellico_parse_error: true,
          raw_text: truncateText(text, 5000)
        };
      }
    }
    return null;
  }

  function isAllowedAutoProbeURL(url) {
    const path = String(url.pathname || "").toLowerCase();
    if (!AUTO_PROBE_PATHS.some((allowedPath) => path.includes(allowedPath))) {
      return false;
    }
    if (path.includes("/adv/v1/budget")) {
      const id = Number.parseInt(url.searchParams.get("id") || "", 10);
      return Number.isFinite(id) && id > 0;
    }
    return true;
  }

  function normalizeAutoProbeURL(value) {
    try {
      const url = new URL(String(value || ""), window.location.origin);
      if (url.origin !== window.location.origin || !isAllowedAutoProbeURL(url)) {
        return null;
      }
      return url.toString();
    } catch (_error) {
      return null;
    }
  }

  function stripSellicoFetchMetadata(init) {
    if (!init || (!init.__sellicoAutoProbe && !init.__sellicoAutoProbeReason)) {
      return init;
    }
    const nextInit = { ...init };
    delete nextInit.__sellicoAutoProbe;
    delete nextInit.__sellicoAutoProbeReason;
    return nextInit;
  }

  const originalFetch = window.fetch;
  window.fetch = async function patchedFetch(input, init) {
    const autoProbeMetadata = init?.__sellicoAutoProbe
      ? {
          sellico_auto_probe: true,
          reason: String(init.__sellicoAutoProbeReason || "")
        }
      : null;
    const response = await originalFetch.call(this, input, stripSellicoFetchMetadata(init));
    try {
      const url = typeof input === "string" ? input : input?.url;
      const clone = response.clone();
      const contentType = clone.headers.get("content-type") || "";
      const text = await clone.text();
      let responseBody = parseMaybeJSON(text, contentType);
      if (responseBody) {
        const serialized = JSON.stringify(responseBody);
        if (serialized.length > RESPONSE_BODY_LIMIT) {
          responseBody = {
            _sellico_truncated: true,
            _sellico_original_size: serialized.length
          };
        }
      }
      emit({
        channel: "fetch",
        method: init?.method || "GET",
        url,
        status: response.status,
        request: truncateBody(init?.body),
        response: responseBody,
        metadata: autoProbeMetadata
      });
    } catch (_error) {
      // Best-effort capture only.
    }
    return response;
  };

  const OriginalXHR = window.XMLHttpRequest;
  function PatchedXHR() {
    const xhr = new OriginalXHR();
    let method = "GET";
    let url = "";
    let body = null;
    let listenerAttached = false;
    const open = xhr.open;
    const send = xhr.send;

    xhr.open = function patchedOpen(nextMethod, nextURL, ...rest) {
      method = nextMethod || "GET";
      url = nextURL || "";
      return open.call(this, nextMethod, nextURL, ...rest);
    };
    xhr.send = function patchedSend(nextBody) {
      body = nextBody || null;
      if (!listenerAttached) {
        listenerAttached = true;
        this.addEventListener("load", () => {
          try {
            const contentType = xhr.getResponseHeader("content-type") || "";
            let responsePayload = parseMaybeJSON(xhr.responseText || "", contentType);
            if (responsePayload) {
              const serialized = JSON.stringify(responsePayload);
              if (serialized.length > RESPONSE_BODY_LIMIT) {
                responsePayload = {
                  _sellico_truncated: true,
                  _sellico_original_size: serialized.length
                };
              }
            }
            emit({
              channel: "xhr",
              method,
              url,
              status: xhr.status,
              request: truncateBody(body),
              response: responsePayload
            });
          } catch (_error) {
            // Ignore malformed/non-json payloads.
          }
        });
      }
      return send.call(this, nextBody);
    };
    return xhr;
  }

  window.XMLHttpRequest = PatchedXHR;

  window.addEventListener("message", async (event) => {
    if (
      event.source !== window ||
      event.data?.source !== "sellico-content-autoprobe" ||
      event.data?.nonce !== bridgeNonce
    ) {
      return;
    }
    const requests = Array.isArray(event.data?.detail?.requests) ? event.data.detail.requests : [];
    const reason = String(event.data?.detail?.reason || "auto");
    const urls = requests
      .map((request) => normalizeAutoProbeURL(request?.url || request))
      .filter(Boolean)
      .slice(0, AUTO_PROBE_REQUEST_LIMIT);
    for (const url of urls) {
      try {
        await window.fetch(url, {
          method: "GET",
          credentials: "include",
          headers: { Accept: "application/json" },
          __sellicoAutoProbe: true,
          __sellicoAutoProbeReason: reason
        });
      } catch (error) {
        emit({
          channel: "auto-probe",
          method: "GET",
          url,
          status: 0,
          request: null,
          response: { error: error?.message || "WB request failed" },
          metadata: {
            sellico_auto_probe: true,
            reason
          }
        });
      }
    }
  });
})();

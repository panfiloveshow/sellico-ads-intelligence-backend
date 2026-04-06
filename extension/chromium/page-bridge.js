(function installSellicoPageBridge() {
  if (window.__sellicoPageBridgeInstalled) {
    return;
  }
  window.__sellicoPageBridgeInstalled = true;
  const REQUEST_BODY_LIMIT = 50_000;

  function truncateBody(body) {
    if (!body) return null;
    const str = typeof body === "string" ? body : JSON.stringify(body);
    if (!str || str.length <= REQUEST_BODY_LIMIT) return body;
    return { _sellico_truncated: true, _sellico_original_size: str.length };
  }

  function emit(detail) {
    window.postMessage({
      source: "sellico-page-bridge",
      detail
    }, "*");
  }

  const originalFetch = window.fetch;
  window.fetch = async function patchedFetch(input, init) {
    const response = await originalFetch.call(this, input, init);
    try {
      const url = typeof input === "string" ? input : input?.url;
      const clone = response.clone();
      const contentType = clone.headers.get("content-type") || "";
      let responseBody = null;
      if (contentType.includes("application/json")) {
        responseBody = await clone.json();
        const serialized = JSON.stringify(responseBody);
        const limit = 200_000;
        if (serialized.length > limit) {
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
        response: responseBody
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
    const open = xhr.open;
    const send = xhr.send;

    xhr.open = function patchedOpen(nextMethod, nextURL, ...rest) {
      method = nextMethod || "GET";
      url = nextURL || "";
      return open.call(this, nextMethod, nextURL, ...rest);
    };
    xhr.send = function patchedSend(nextBody) {
      body = nextBody || null;
      this.addEventListener("load", () => {
        try {
          const contentType = xhr.getResponseHeader("content-type") || "";
          let responsePayload = null;
          if (contentType.includes("application/json")) {
            responsePayload = JSON.parse(xhr.responseText);
            const serialized = JSON.stringify(responsePayload);
            const limit = 200_000;
            if (serialized.length > limit) {
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
      return send.call(this, nextBody);
    };
    return xhr;
  }

  window.XMLHttpRequest = PatchedXHR;
})();

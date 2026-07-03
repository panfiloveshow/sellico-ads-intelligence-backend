(function initSellicoExtension() {
  const PANEL_ID = "sellico-extension-panel";
  const ROOT_ATTR = "data-sellico-extension-root";
  const INLINE_BADGE_ATTR = "data-sellico-inline-badge";
  const EXTENSION_VERSION = chrome.runtime.getManifest().version;
  const DEBUG_LOGS = false;
  const NETWORK_ENDPOINT_ALLOWLIST = new Map([
    ["/adv/v1/promotion/count", "wb.campaign.inventory"],
    ["/adv/v1/promotion/adverts", "wb.adverts"],
    ["/api/advert/v2/adverts", "wb.campaign.settings"],
    ["/api/v1/advert/", "wb.ui.advert"],
    ["/api/v5/fullstat", "wb.ui.campaign.stats"],
    ["/api/v5/analyst-info", "wb.ui.analyst"],
    ["/adv/v0/advert", "wb.adverts"],
    ["/adv/v3/fullstats", "wb.campaign.stats"],
    ["/adv/v0/stats", "wb.campaign.stats"],
    ["/adv/v1/normquery/stats", "wb.query.stats"],
    ["/adv/v0/normquery/get-bids", "wb.query.bids"],
    ["/adv/v0/normquery/bids", "wb.query.bids"],
    ["/adv/v0/normquery/set-minus", "wb.query.minus"],
    ["/adv/v0/normquery", "wb.query.clusters"],
    ["/api/advert/v1/bids/min", "wb.bid.min"],
    ["/api/advert/v1/bids", "wb.bid.product"],
    ["/api/advert/v0/bids/recommendations", "wb.bid.recommendations"],
    ["/api/v1/advert/preset-bids", "wb.bid.recommendations"],
    ["/adv/v2/recommended-bids", "wb.bid.estimate"],
    ["/adv/v1/budget", "wb.budget"],
    ["/adv/v1/balance", "wb.balance"],
    ["/adv/v1/upd", "wb.finance"],
    ["/adv/v1/payments", "wb.finance"],
    ["/api/analytics/v3/sales-funnel/products", "wb.sales_funnel.products"],
    ["/api/v2/search-report", "wb.search_report"],
    ["/adv/v1/auction/adverts", "wb.ui.auction"],
    ["/placement", "wb.ui.placement"],
    ["/search", "wb.ui.search"]
  ]);
  const BACKEND_PAGE_TYPES = new Set(["search", "product", "campaign", "query", "auction", "cabinet"]);
  const RETRY_CAPTURE_DELAYS_MS = [0, 1500, 4000];
  const FLUSH_DELAYS_MS = {
    bootstrap: 350,
    widget: 900,
    network: 900,
    domRows: 900,
    uiSignals: 700,
    bid: 500,
    position: 500,
    badges: 450
  };
  const FOCUS_REFRESH_INTERVAL_MS = 60 * 1000;
  const AUTO_PROBE_CORE_COOLDOWN_MS = 5 * 60 * 1000;
  const AUTO_PROBE_BUDGET_COOLDOWN_MS = 5 * 60 * 1000;
  const AUTO_PROBE_MAX_CAMPAIGNS = 12;
  const AUTO_PROBE_MAX_REQUESTS = 16;
  const MAX_NETWORK_BATCH_ITEMS = 50;
  const MAX_NETWORK_PAYLOAD_BYTES = 256 * 1024;
  const MAX_DOM_ROW_BATCH_ITEMS = 100;
  const pageBridgeNonce = (() => {
    try {
      const values = new Uint32Array(4);
      crypto.getRandomValues(values);
      return Array.from(values, (value) => value.toString(36)).join("-");
    } catch (_error) {
      return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
    }
  })();

  function escapeHtml(text) {
    const div = document.createElement("div");
    div.textContent = String(text);
    return div.innerHTML;
  }
  const METRIC_BLOCK_SELECTORS = [
    "[data-testid]",
    "[data-bid]",
    "[data-position]",
    "tr",
    "[role='row']",
    "section",
    "li",
    "dl",
    ".table-row",
    ".list-item",
    "[class*='row']",
    "[class*='item']",
    "[class*='card']",
    "[class*='metric']",
    "[class*='value']"
  ].join(",");
  const QUERY_TEXT_SELECTORS = [
    "[data-query]",
    "[data-phrase]",
    "[data-keyword]",
    ".query",
    ".phrase",
    ".keyword",
    ".search-query",
    "[data-testid='query']",
    "[data-testid='phrase']"
  ];
  const TITLE_SELECTORS = ["h1", "[data-testid='title']", ".page-title", ".title", ".header-title"];
  const REGION_SELECTORS = ["[data-region]", ".region-selector", ".geo-selector", "[data-testid='region']"];
  const BID_SELECTORS = {
    visible: ["[data-bid]", ".bid", ".cpm", ".price", "[data-testid='current-bid']"],
    recommended: ["[data-recommended-bid]", "[data-testid='recommended-bid']"],
    competitive: ["[data-competitive-bid]", "[data-testid='competitive-bid']"],
    leadership: ["[data-leadership-bid]", "[data-testid='leadership-bid']"],
    cpmMin: ["[data-cpm-min]", "[data-testid='min-bid']"]
  };
  const POSITION_SELECTORS = {
    visible: ["[data-position]", ".position", ".place", "[data-testid='position']"],
    page: ["[data-page]", "[data-testid='page']"]
  };
  const ROW_SELECTORS = ["tr", "[role='row']", ".table-row", ".list-item", ".ReactVirtualized__Table__row", "li", "[class*='row']"];

  let currentConfig = null;
  let currentContext = null;
  let currentWidget = null;
  let currentBootstrapToken = 0;
  let lastBootstrappedUrl = "";
  let lastBootstrapAt = 0;
  let bootstrapTimer = null;
  let widgetRefreshTimer = null;
  let badgeRefreshTimer = null;
  let badgeObserver = null;
  let captureTimeouts = [];
  let lastVisibleRefreshAt = 0;
  let lastWidgetError = null;
  let lastWidgetState = "idle";
  let workspaceEvidenceSummary = null;
  let currentEvidenceDebug = null;
  let networkCaptureQueue = new Map();
  let domRowQueue = new Map();
  let uiSignalQueue = new Map();
  let bidQueue = new Map();
  let positionQueue = new Map();
  let liveCaptureState = {
    total: 0,
    lastEndpoint: "",
    lastStatus: "",
    lastAt: null,
    campaignCandidates: [],
    productCandidates: [],
    queryCandidates: [],
    bidCandidates: [],
    domRowCount: 0
  };
  let autoProbeTimer = null;
  let autoProbeState = {
    inFlight: false,
    lastCoreAt: 0,
    budgetByCampaign: new Map()
  };
  let flushTimers = {
    network: null,
    domRows: null,
    uiSignals: null,
    bid: null,
    position: null
  };

  console.info(`[Sellico] Extension content loaded v${EXTENSION_VERSION}`);

  function debugLog(...args) {
    if (DEBUG_LOGS) {
      console.debug(...args);
    }
  }

  function isConfigurationError(message) {
    const normalized = String(message || "").toLowerCase();
    return normalized.includes("not configured") || normalized.includes("не подключено") || normalized.includes("не настроено");
  }

  function isCampaignNotSyncedError(message) {
    const normalized = String(message || "").toLowerCase();
    return normalized.includes("sellico api 404") && normalized.includes("campaign not found");
  }

  function isProductNotSyncedError(message) {
    const normalized = String(message || "").toLowerCase();
    return normalized.includes("sellico api 404") && normalized.includes("product not found");
  }

  function isEntityNotSyncedError(message) {
    return isCampaignNotSyncedError(message) || isProductNotSyncedError(message);
  }

  function isExtensionContextInvalidated(message) {
    return String(message || "").toLowerCase().includes("extension context invalidated");
  }

  function safeObjectMessage(value) {
    if (value === null || value === undefined) {
      return "";
    }
    if (typeof value === "string") {
      return value;
    }
    if (typeof value === "number" || typeof value === "boolean") {
      return String(value);
    }
    if (typeof value === "object") {
      const direct = value.message || value.error || value.reason || value.code || value.statusText || value.status;
      if (direct !== undefined && direct !== null && direct !== value) {
        return safeObjectMessage(direct);
      }
      try {
        return JSON.stringify(value);
      } catch (_error) {
        return "Не удалось прочитать детали ошибки";
      }
    }
    return String(value);
  }

  function normalizeRuntimeErrorMessage(message) {
    const safeMessage = safeObjectMessage(message);
    if (isExtensionContextInvalidated(message)) {
      return "Расширение Sellico было обновлено. Перезагрузите страницу WB, чтобы подключить новую версию.";
    }
    return safeMessage || "Неизвестная ошибка расширения Sellico";
  }

  function logHandledError(scope, reason, error) {
    const message = error?.message || safeObjectMessage(error);
    const normalizedMessage = normalizeRuntimeErrorMessage(message);
    const reasonText = safeObjectMessage(reason);
    if (
      isConfigurationError(normalizedMessage) ||
      isExtensionContextInvalidated(message) ||
      isEntityNotSyncedError(normalizedMessage)
    ) {
      console.info(`[Sellico] ${scope}: ${normalizedMessage}${reasonText ? ` (${reasonText})` : ""}`);
      return normalizedMessage;
    }
    console.error(`[Sellico] ${scope}: ${normalizedMessage}${reasonText ? ` (${reasonText})` : ""}`);
    return normalizedMessage;
  }

  function sendMessage(message) {
    return new Promise((resolve, reject) => {
      chrome.runtime.sendMessage(message, (response) => {
        const runtimeError = chrome.runtime.lastError;
        if (runtimeError) {
          reject(new Error(normalizeRuntimeErrorMessage(runtimeError.message)));
          return;
        }
        if (!response?.ok) {
          reject(new Error(normalizeRuntimeErrorMessage(response?.error || "Sellico extension request failed")));
          return;
        }
        resolve(response);
      });
    });
  }

  async function loadConfig(force = false) {
    if (!force && currentConfig) {
      return currentConfig;
    }
    const response = await sendMessage({ type: "extension:get-config" });
    currentConfig = response.config || { autoCapture: true };
    return currentConfig;
  }

  function getText(el) {
    return el ? el.textContent.trim() : "";
  }

  function firstMatch(selectors, root = document) {
    for (const selector of selectors) {
      const el = root.querySelector(selector);
      if (el) {
        return el;
      }
    }
    return null;
  }

  function parseIntParam(...keys) {
    const url = new URL(window.location.href);
    for (const key of keys) {
      const raw = url.searchParams.get(key);
      if (!raw) {
        continue;
      }
      const value = Number.parseInt(raw, 10);
      if (Number.isFinite(value) && value > 0) {
        return value;
      }
    }
    return null;
  }

  function parsePositiveInt(value) {
    const parsed = Number.parseInt(String(value || ""), 10);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
  }

  function extractWBCampaignIDFromUrl(urlValue) {
    let url;
    try {
      url = new URL(urlValue || window.location.href, window.location.origin);
    } catch (_error) {
      return null;
    }
    const isBudgetEndpoint = url.pathname.toLowerCase().includes("/adv/v1/budget");
    const keys = ["campaignId", "campaign_id", "advertId", "advertID", "advert_id"].concat(isBudgetEndpoint ? ["id"] : []);
    for (const key of keys) {
      const value = parsePositiveInt(url.searchParams.get(key));
      if (value) {
        return value;
      }
    }
    const pathMatchers = [
      /\/api\/v1\/advert\/(\d+)(?:\/|$)/i,
      /\/(?:campaigns?|adverts?|advert)\/(\d+)(?:\/|$)/i
    ];
    for (const matcher of pathMatchers) {
      const match = url.pathname.match(matcher);
      const value = parsePositiveInt(match?.[1]);
      if (value) {
        return value;
      }
    }
    return null;
  }

  function extractWBProductIDFromUrl(urlValue) {
    let url;
    try {
      url = new URL(urlValue || window.location.href, window.location.origin);
    } catch (_error) {
      return null;
    }
    const keys = ["nm", "nmId", "nmid", "nmID", "productId", "product_id"];
    for (const key of keys) {
      const value = parsePositiveInt(url.searchParams.get(key));
      if (value) {
        return value;
      }
    }
    const match = url.pathname.match(/\/(?:nm|product|products|card)\/(\d+)(?:\/|$)/i);
    return parsePositiveInt(match?.[1]);
  }

  function extractNormQueriesFromUrl(urlValue) {
    let url;
    try {
      url = new URL(urlValue || window.location.href, window.location.origin);
    } catch (_error) {
      return [];
    }
    const raw =
      url.searchParams.get("norm_queries") ||
      url.searchParams.get("normQueries") ||
      url.searchParams.get("queries") ||
      url.searchParams.get("query") ||
      "";
    return raw
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean)
      .slice(0, 50);
  }

  function extractBidCandidatesFromPresetResponse(response) {
    const bids = [];
    collectPayloadCandidates(response).forEach((candidate) => {
      const metrics = extractBidMetricsFromCandidate(candidate);
      [
        metrics.visibleBid,
        metrics.recommendedBid,
        metrics.competitiveBid,
        metrics.leadershipBid,
        metrics.cpmMin
      ].forEach((value) => {
        if (value !== null && value !== undefined) {
          bids.push(`${value} ₽`);
        }
      });
    });
    return Array.from(new Set(bids)).slice(0, 20);
  }

  function extractNumber(text) {
    if (!text) {
      return null;
    }
    const normalized = String(text)
      .replace(/\s+/g, "")
      .replace(/,/g, ".")
      .replace(/[^\d.-]/g, "");
    if (!normalized) {
      return null;
    }
    const value = Number.parseFloat(normalized);
    if (!Number.isFinite(value)) {
      return null;
    }
    return Math.round(value);
  }

  function detectPageType() {
    const href = window.location.href.toLowerCase();
    if (href.includes("auction")) {
      return "auction";
    }
    if (href.includes("advertisement") || href.includes("promotion") || href.includes("promot")) {
      return "cabinet";
    }
    if (href.includes("campaign") || href.includes("advert")) {
      return "campaign";
    }
    if (href.includes("search") || href.includes("phrase") || href.includes("query")) {
      return "query";
    }
    if (href.includes("nm") || href.includes("product") || href.includes("card")) {
      return "product";
    }
    return "cabinet";
  }

  function isPromotionPage() {
    const href = window.location.href.toLowerCase();
    return window.location.hostname === "cmp.wildberries.ru" ||
      href.includes("advertisement") ||
      href.includes("promotion") ||
      href.includes("promot");
  }

  function toBackendPageType(value) {
    const normalized = String(value || "").toLowerCase();
    return BACKEND_PAGE_TYPES.has(normalized) ? normalized : "cabinet";
  }

  function backendPageTypeFromContext(context) {
    return toBackendPageType(context?.page_type || context?.page_subtype);
  }

  function hasNonEmptyText(value) {
    return typeof value === "string" && value.trim().length > 0;
  }

  function hasBackendBidContext(item) {
    return Boolean(item?.phrase_id || item?.campaign_id || hasNonEmptyText(item?.query));
  }

  function isBackendValidationMessage(message) {
    const normalized = safeObjectMessage(message).toLowerCase();
    return (
      normalized.includes("validation_error") ||
      normalized.includes("requires phrase_id") ||
      normalized.includes("requires phrase, query or campaign") ||
      normalized.includes("unsupported network capture page_type") ||
      normalized.includes("unsupported extension page_type") ||
      normalized.includes("must be one of:")
    );
  }

  function detectPageSubtype() {
    const href = window.location.href.toLowerCase();
    const bodyText = document.body?.innerText?.slice(0, 4000).toLowerCase() || "";
    if (isPromotionPage()) {
      return "promotion";
    }
    if (href.includes("auction") || bodyText.includes("аукцион")) {
      return "auction";
    }
    if (href.includes("search") || bodyText.includes("поиск")) {
      return "search";
    }
    if (bodyText.includes("каталог")) {
      return "catalog";
    }
    return detectPageType();
  }

  function detectPrimaryQueryText(pageType) {
    const fromParams =
      new URL(window.location.href).searchParams.get("query") ||
      new URL(window.location.href).searchParams.get("phrase") ||
      new URL(window.location.href).searchParams.get("keyword") ||
      "";
    if (fromParams) {
      return fromParams;
    }
    const selectorValue = getText(firstMatch(QUERY_TEXT_SELECTORS));
    if (selectorValue) {
      return selectorValue;
    }
    const title = getText(firstMatch(TITLE_SELECTORS));
    if (pageType === "query") {
      return title;
    }
    return "";
  }

  function collectMetricBlocks() {
    return Array.from(document.querySelectorAll(METRIC_BLOCK_SELECTORS))
      .filter((node) => {
        if (!(node instanceof HTMLElement)) {
          return false;
        }
        if (node.closest(`#${PANEL_ID}`)) {
          return false;
        }
        const text = getText(node);
        return text.length > 0 && text.length < 240;
      })
      .slice(0, 250);
  }

  function findMetricBySelectors(selectors) {
    const text = getText(firstMatch(selectors));
    return extractNumber(text);
  }

  function metricRegexForKeywords(keywords) {
    const joined = keywords.map((keyword) => keyword.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|");
    return new RegExp(`(?:${joined})[^\\d-]{0,32}(-?[\\d\\s.,]+)`, "i");
  }

  function findMetricByKeywords(keywords) {
    const regex = metricRegexForKeywords(keywords);
    const blocks = collectMetricBlocks();
    for (const block of blocks) {
      const text = getText(block);
      if (!text) {
        continue;
      }
      const match = text.match(regex);
      if (!match?.[1]) {
        continue;
      }
      const value = extractNumber(match[1]);
      if (value !== null) {
        return value;
      }
    }
    return null;
  }

  function detectContext() {
    const url = new URL(window.location.href);
    const pageType = detectPageType();
    const pageSubtype = detectPageSubtype();
    const query = detectPrimaryQueryText(pageType);
    const region =
      url.searchParams.get("region") ||
      getText(firstMatch(REGION_SELECTORS)) ||
      "";
    const campaignHintFromParam = parseIntParam("campaignId", "campaign_id", "advertId", "advertID", "advert_id");
    const campaignHintFromPath = (() => {
      const segments = url.pathname.split("/").filter(Boolean);
      for (let i = 0; i < segments.length; i++) {
        if (/^(campaigns?|adverts?|advert)$/i.test(segments[i]) && segments[i + 1]) {
          const id = Number.parseInt(segments[i + 1], 10);
          if (Number.isFinite(id) && id > 0) {
            return id;
          }
        }
      }
      return null;
    })();
    const campaignHint =
      campaignHintFromParam ||
      campaignHintFromPath ||
      extractWBCampaignIDFromUrl(window.location.href) ||
      (pageSubtype === "promotion" ? null : detectVisibleWBCampaignID());
    
    debugLog("[Sellico] detectContext:", {
      url: window.location.href,
      pageType,
      campaignHintFromParam,
      campaignHintFromPath,
      campaignHint,
      pathname: url.pathname
    });
    const wbProductId = parseIntParam("nm", "nmId", "nmid", "nmID", "productId", "product_id") || extractWBProductIDFromUrl(window.location.href);
    const pageTitle = getText(firstMatch(TITLE_SELECTORS));
    const wbSellerCabinetId = parseIntParam("cabinetId", "supplierId", "supplier_id");

    return {
      url: window.location.href,
      page_type: pageType,
      wb_seller_cabinet_id: wbSellerCabinetId,
      query,
      region,
      metadata: {
        title: pageTitle,
        pathname: url.pathname,
        page_subtype: pageSubtype,
        inferred_query: query
      },
      active_filters: {
        pathname: url.pathname,
        search: url.search
      },
      wb_campaign_id: campaignHint,
      wb_product_id: wbProductId,
      page_subtype: pageSubtype
    };
  }

  function buildPageContextPayload(context) {
    const pageType = backendPageTypeFromContext(context);
    return {
      url: context.url,
      page_type: pageType,
      query: pageType === "query" ? context.query : "",
      region: context.region,
      active_filters: context.active_filters,
      metadata: {
        ...context.metadata,
        wb_campaign_id: context.wb_campaign_id,
        wb_product_id: context.wb_product_id,
        wb_seller_cabinet_id: context.wb_seller_cabinet_id,
        page_subtype: context.page_subtype
      }
    };
  }

  function buildWidgetRequest(context) {
    const allowVisibleCampaignFallback = context.page_subtype !== "promotion" || context.page_type === "campaign";
    const visibleCampaignID = context.wb_campaign_id || (allowVisibleCampaignFallback ? detectVisibleWBCampaignID() : null);
    if (visibleCampaignID && !context.wb_campaign_id && currentContext?.url === context.url) {
      currentContext = { ...currentContext, wb_campaign_id: visibleCampaignID };
      context = currentContext;
    }
    if (context.page_type === "query" && context.query) {
      debugLog("[Sellico] Widget request: query=", context.query);
      return { type: "extension:fetch-widget", query: context.query };
    }
    if (context.wb_product_id) {
      debugLog("[Sellico] Widget request: wb_product_id=", context.wb_product_id);
      return { type: "extension:fetch-widget", wbProductId: context.wb_product_id };
    }
    if (visibleCampaignID) {
      debugLog("[Sellico] Widget request: wb_campaign_id=", visibleCampaignID);
      return { type: "extension:fetch-widget", wbCampaignId: visibleCampaignID };
    }
    if (context.page_subtype === "promotion") {
      console.info("[Sellico] Promotion overview: page and network captures only until a campaign/product/query is selected");
      return null;
    }
    console.info("[Sellico] Widget request skipped: open a campaign, product or query to show a detail widget");
    return null;
  }

  function rememberUnique(values, nextValue, maxItems = 6) {
    if (!nextValue) {
      return values;
    }
    const stringValue = String(nextValue);
    return [stringValue].concat(values.filter((value) => value !== stringValue)).slice(0, maxItems);
  }

  function recordLiveCapture(payload) {
    if (!payload) {
      return;
    }
    const payloadQueries = Array.isArray(payload.payload?.norm_queries) ? payload.payload.norm_queries : [];
    const payloadBids = Array.isArray(payload.payload?.bid_candidates) ? payload.payload.bid_candidates : [];
    const responseCampaignIDs = collectUniqueNetworkCampaignIDs(payload.payload?.response);
    liveCaptureState = {
      ...liveCaptureState,
      total: liveCaptureState.total + 1,
      lastEndpoint: payload.endpoint_key || "",
      lastStatus: payload.payload?.status || "",
      lastAt: new Date(),
      campaignCandidates: rememberUnique(liveCaptureState.campaignCandidates, payload.payload?.wb_campaign_id),
      productCandidates: rememberUnique(liveCaptureState.productCandidates, payload.payload?.wb_product_id)
    };
    responseCampaignIDs.slice(0, 8).forEach((campaignID) => {
      liveCaptureState.campaignCandidates = rememberUnique(liveCaptureState.campaignCandidates, campaignID, 12);
    });
    payloadQueries.slice(0, 4).forEach((query) => {
      liveCaptureState.queryCandidates = rememberUnique(liveCaptureState.queryCandidates, query, 8);
    });
    payloadBids.slice(0, 4).forEach((bid) => {
      liveCaptureState.bidCandidates = rememberUnique(liveCaptureState.bidCandidates, bid, 8);
    });
  }

  function isAutoProbeHost() {
    return /(^|\.)cmp\.wildberries\.ru$/i.test(window.location.hostname || "");
  }

  function autoProbeRequest(path) {
    try {
      const url = new URL(path, window.location.origin);
      return { url: url.toString() };
    } catch (_error) {
      return null;
    }
  }

  function collectAutoProbeCampaignIDs(extraPayload = null) {
    const ids = new Set();
    const add = (value) => {
      const parsed = parsePositiveInt(value);
      if (parsed) {
        ids.add(parsed);
      }
    };
    add(currentContext?.wb_campaign_id);
    add(detectVisibleWBCampaignID());
    liveCaptureState.campaignCandidates.forEach(add);
    collectUniqueNetworkCampaignIDs(extraPayload?.payload?.response || extraPayload?.response).forEach(add);
    return Array.from(ids).slice(0, AUTO_PROBE_MAX_CAMPAIGNS);
  }

  function buildAutoProbeRequests(reason, extraPayload = null) {
    if (!isAutoProbeHost() || currentConfig?.autoCapture === false) {
      return [];
    }

    const now = Date.now();
    const requests = [];
    if (now - autoProbeState.lastCoreAt >= AUTO_PROBE_CORE_COOLDOWN_MS) {
      ["/adv/v1/promotion/count", "/api/advert/v2/adverts?statuses=-1,4,7,8,9,11"].forEach((path) => {
        const request = autoProbeRequest(path);
        if (request) {
          requests.push(request);
        }
      });
      autoProbeState.lastCoreAt = now;
    }

    collectAutoProbeCampaignIDs(extraPayload).forEach((campaignID) => {
      const lastBudgetAt = autoProbeState.budgetByCampaign.get(String(campaignID)) || 0;
      if (now - lastBudgetAt < AUTO_PROBE_BUDGET_COOLDOWN_MS) {
        return;
      }
      const request = autoProbeRequest(`/adv/v1/budget?id=${campaignID}`);
      if (request) {
        requests.push(request);
        autoProbeState.budgetByCampaign.set(String(campaignID), now);
      }
    });

    return requests.slice(0, AUTO_PROBE_MAX_REQUESTS);
  }

  function scheduleAutoProbe(reason = "auto", extraPayload = null, delayMs = 900) {
    if (currentConfig?.autoCapture === false || !isAutoProbeHost()) {
      return;
    }
    if (autoProbeTimer) {
      clearTimeout(autoProbeTimer);
    }
    autoProbeTimer = window.setTimeout(() => {
      autoProbeTimer = null;
      runAutoProbe(reason, extraPayload);
    }, delayMs);
  }

  function runAutoProbe(reason, extraPayload = null) {
    if (autoProbeState.inFlight) {
      return;
    }
    const requests = buildAutoProbeRequests(reason, extraPayload);
    if (!requests.length) {
      return;
    }
    autoProbeState.inFlight = true;
    window.postMessage({
      source: "sellico-content-autoprobe",
      nonce: pageBridgeNonce,
      detail: {
        reason,
        requests
      }
    }, "*");
    window.setTimeout(() => {
      autoProbeState.inFlight = false;
    }, 3000);
  }

  function liveCaptureSignalItems() {
    const items = [];
    if (!liveCaptureState.total) {
      return items;
    }
    items.push(`<strong>Сетевые ответы WB</strong><span>${escapeHtml(captureStateText())}</span>`);
    if (liveCaptureState.queryCandidates.length) {
      items.push(`<strong>Кластеры из кабинета</strong><span>${escapeHtml(liveCaptureState.queryCandidates.slice(0, 5).join(", "))}</span>`);
    }
    if (liveCaptureState.bidCandidates.length) {
      items.push(`<strong>Ставки на странице</strong><span>${escapeHtml(liveCaptureState.bidCandidates.slice(0, 5).join(", "))}</span>`);
    }
    return items;
  }

  function captureStateText() {
    if (!liveCaptureState.total) {
      return "Сетевые ответы WB пока не пойманы. Обновите страницу или откройте таблицу/карточку кампании.";
    }
    const parts = [`Поймано сетевых ответов WB: ${liveCaptureState.total}`];
    if (liveCaptureState.lastEndpoint) {
      parts.push(`последний источник: ${liveCaptureState.lastEndpoint}`);
    }
    if (liveCaptureState.lastStatus) {
      parts.push(`HTTP ${liveCaptureState.lastStatus}`);
    }
    if (liveCaptureState.campaignCandidates.length) {
      parts.push(`campaign IDs из API: ${liveCaptureState.campaignCandidates.join(", ")}`);
    }
    if (liveCaptureState.productCandidates.length) {
      parts.push(`артикулы товаров: ${liveCaptureState.productCandidates.join(", ")}`);
    }
    if (liveCaptureState.queryCandidates.length) {
      parts.push(`кластеры: ${liveCaptureState.queryCandidates.length}`);
    }
    if (liveCaptureState.bidCandidates.length) {
      parts.push(`ставки: ${liveCaptureState.bidCandidates.length}`);
    }
    if (liveCaptureState.domRowCount) {
      parts.push(`строки таблиц: ${liveCaptureState.domRowCount}`);
    }
    return parts.join(" · ");
  }

  function evidenceSummaryText(summary = workspaceEvidenceSummary) {
    if (!summary || summary.unavailable) {
      return "";
    }
    const parts = [
      `сеть ${formatNumber(summary.network_captures || 0)}`,
      `ставки ${formatNumber(summary.bid_snapshots || 0)}`,
      `позиции ${formatNumber(summary.position_snapshots || 0)}`,
      `предупреждения ${formatNumber(summary.ui_signals || 0)}`
    ];
    if (summary.latest_captured_at || summary.latestCapturedAt) {
      const date = new Date(summary.latest_captured_at || summary.latestCapturedAt);
      if (!Number.isNaN(date.getTime())) {
        parts.push(`последний сигнал ${date.toLocaleString("ru-RU")}`);
      }
    }
    return parts.join(" · ");
  }

  function evidenceSummaryItems(summary = workspaceEvidenceSummary) {
    if (!summary || summary.unavailable) {
      return [];
    }
    const items = [];
    const totalTyped = Number(summary.bid_snapshots || 0) + Number(summary.position_snapshots || 0) + Number(summary.ui_signals || 0);
    items.push(`<strong>Evidence workspace</strong><span>${escapeHtml(evidenceSummaryText(summary))}</span>`);
    if (Number(summary.network_captures || 0) > 0 && totalTyped === 0) {
      items.push(`<strong>Нужна нормализация</strong><span>Сеть WB уже поймана, но typed-сигналы пока не появились.</span>`);
    }
    (summary.issues || []).slice(0, 2).forEach((item) => {
      items.push(`<strong>${escapeHtml(item.stage || "Статус")}</strong><span>${escapeHtml(item.message || "")}</span>`);
    });
    return items;
  }

  function buildEvidenceDebugRequest(context, widget) {
    if (widget?.campaign?.id) {
      return {
        type: "extension:fetch-evidence-debug",
        scope: "campaign",
        campaignId: widget.campaign.id,
        limit: 10
      };
    }
    if (widget?.product?.id) {
      return {
        type: "extension:fetch-evidence-debug",
        scope: "product",
        productId: widget.product.id,
        limit: 10
      };
    }
    if (widget?.phrase?.id) {
      return {
        type: "extension:fetch-evidence-debug",
        scope: "query",
        phraseId: widget.phrase.id,
        query: widget.phrase.keyword || context.query || "",
        limit: 10
      };
    }
    if (context?.query) {
      return {
        type: "extension:fetch-evidence-debug",
        scope: "query",
        query: context.query,
        limit: 10
      };
    }
    return null;
  }

  function evidenceDebugText(debug = currentEvidenceDebug) {
    if (!debug || debug.unavailable) {
      return "";
    }
    const counts = debug.counts || {};
    const parts = [
      `контекст ${formatNumber(counts.page_contexts || 0)}`,
      `сеть ${formatNumber(counts.network_captures || 0)}`,
      `строки ${formatNumber(counts.dom_row_snapshots || 0)}`,
      `ставки ${formatNumber(counts.bid_snapshots || 0)}`,
      `позиции ${formatNumber(counts.position_snapshots || 0)}`,
      `сигналы ${formatNumber(counts.ui_signals || 0)}`
    ];
    if (debug.latest_captured_at) {
      const date = new Date(debug.latest_captured_at);
      if (!Number.isNaN(date.getTime())) {
        parts.push(`последний ${date.toLocaleString("ru-RU")}`);
      }
    }
    return parts.join(" · ");
  }

  function captureChecklistItems(context, widget, dataStatus, debug = currentEvidenceDebug) {
    const counts = debug?.counts || {};
    const hasPageContext = Number(counts.page_contexts || 0) > 0 || liveCaptureState.total > 0;
    const hasNetwork = Number(counts.network_captures || 0) > 0 || liveCaptureState.total > 0;
    const hasDOMRows = Number(counts.dom_row_snapshots || 0) > 0 || liveCaptureState.domRowCount > 0;
    const hasBid = Number(counts.bid_snapshots || 0) > 0 || Boolean(widget?.live_bid_snapshot) || Boolean((widget?.live_bids || [])[0]);
    const hasPosition = Number(counts.position_snapshots || 0) > 0 || (widget?.live_positions || []).length > 0;
    const hasUISignal = Number(counts.ui_signals || 0) > 0 || (widget?.ui_signals || []).length > 0;
    const hasRecommendation = (widget?.recommendations || []).length > 0;
    const isCampaign = Boolean(widget?.campaign || context?.wb_campaign_id);
    const isProduct = Boolean(widget?.product || context?.wb_product_id);
    const isQuery = Boolean(widget?.phrase || context?.query);

    const items = [
      {
        done: hasPageContext,
        title: "Открыта конкретная страница WB",
        text: hasPageContext
          ? "Контекст страницы сохранен."
          : "Откройте кампанию, товар или поисковый запрос и нажмите «Обновить контекст»."
      },
      {
        done: hasNetwork,
        title: "Пойманы allowlisted ответы WB",
        text: hasNetwork
          ? "Есть реальные сетевые ответы из кабинета."
          : "Дождитесь загрузки таблицы WB или обновите страницу; Sellico сохранит только разрешенные endpoints."
      },
      {
        done: hasDOMRows,
        title: "Сохранены видимые строки таблицы WB",
        text: hasDOMRows
          ? "DOM строки сохранены как cabinet evidence."
          : "Откройте таблицу кампаний, товаров, запросов или ставок и дождитесь автосбора."
      }
    ];

    if (isCampaign || isQuery) {
      items.push({
        done: hasBid,
        title: "Ставки видны как evidence",
        text: hasBid
          ? "Снимок ставки сохранен."
          : "Откройте таблицу ставок, аукцион или кластеры и сохраните/обновите ставку."
      });
    }
    if (isProduct || isQuery) {
      items.push({
        done: hasPosition,
        title: "Позиция товара подтверждена",
        text: hasPosition
          ? "Live-позиция сохранена."
          : "Откройте выдачу WB по запросу с регионом и сохраните позицию."
      });
    }
    items.push({
      done: hasUISignal || dataStatus?.confirmed_in_cabinet,
      title: "UI-сигналы проверены",
      text: hasUISignal
        ? "Предупреждения/статусы кабинета сохранены."
        : "Если WB показывает предупреждения или заблокированные действия, обновите контекст."
    });
    items.push({
      done: hasRecommendation,
      title: "Рекомендация Sellico готова",
      text: hasRecommendation
        ? "Есть действие на основе реальных данных."
        : "Если рекомендаций нет, причина останется в статусе данных; Sellico не будет подставлять пример."
    });

    return items;
  }

  function renderChecklist(title, items, emptyText) {
    const safeItems = items.filter(Boolean);
    if (!safeItems.length) {
      return `<div class="sellico-panel__empty">${escapeHtml(emptyText)}</div>`;
    }
    return `
      <div class="sellico-panel__subsection">
        <h4>${escapeHtml(title)}</h4>
        <ul class="sellico-panel__checklist">
          ${safeItems.map((item) => `
            <li class="${item.done ? "sellico-panel__checklist-item--done" : ""}">
              <strong>${escapeHtml(item.done ? "Готово" : "Нужно")}: ${escapeHtml(item.title)}</strong>
              <span>${escapeHtml(item.text)}</span>
            </li>
          `).join("")}
        </ul>
      </div>
    `;
  }

  function guidedCaptureSteps(context, widget, debug = currentEvidenceDebug) {
    const counts = debug?.counts || {};
    const hasCampaign = Boolean(widget?.campaign || context?.wb_campaign_id || Number(counts.network_captures || 0) > 0);
    const hasQuery = Boolean(widget?.phrase || context?.query || Number(counts.dom_row_snapshots || 0) > 0);
    const hasBid = Number(counts.bid_snapshots || 0) > 0 || Boolean(widget?.live_bid_snapshot) || (widget?.live_bids || []).length > 0;
    const hasProduct = Boolean(widget?.product || context?.wb_product_id);
    const hasPosition = Number(counts.position_snapshots || 0) > 0 || (widget?.live_positions || []).length > 0;
    const hasRecommendation = (widget?.recommendations || []).length > 0;
    const visibleBidMetrics = detectVisibleBidMetrics();
    const canSaveBid = Boolean(context?.query || liveCaptureState.queryCandidates.length === 1) && visibleBidMetrics.confidence > 0;
    const visiblePositionMetrics = detectVisiblePositionMetrics(context || {});
    const canSavePosition = Boolean(context?.wb_product_id && context?.query && context?.region && visiblePositionMetrics.visiblePosition);

    return [
      {
        done: hasCampaign,
        title: "Кампания",
        text: hasCampaign
          ? "Кампания или рекламный раздел WB распознан."
          : "Откройте раздел продвижения WB и выберите конкретную кампанию.",
        action: hasCampaign ? "refresh" : "open-wb-promotion",
        label: hasCampaign ? "Обновить" : "Открыть WB"
      },
      {
        done: hasQuery,
        title: "Запросы",
        text: hasQuery
          ? "Запрос, кластер или строки таблицы сохранены как evidence."
          : "Откройте кластеры/фразы внутри кампании и дождитесь загрузки таблицы.",
        action: "refresh",
        label: "Собрать"
      },
      {
        done: hasBid,
        title: "Ставки",
        text: hasBid
          ? "Live-ставка сохранена или подтверждена в evidence."
          : "Откройте аукцион/ставки или таблицу кластеров, затем сохраните ставку.",
        action: canSaveBid ? "save-bid" : "refresh",
        label: canSaveBid ? "Сохранить" : "Собрать"
      },
      {
        done: hasProduct && hasPosition,
        title: "Карточка",
        text: hasProduct && hasPosition
          ? "Товар и live-позиция подтверждены."
          : hasProduct
            ? "Товар распознан; для позиции откройте выдачу WB по запросу и региону."
            : "Откройте карточку товара или строку товара из кампании.",
        action: canSavePosition ? "save-position" : "refresh",
        label: canSavePosition ? "Позиция" : "Проверить"
      },
      {
        done: hasRecommendation,
        title: "Решение",
        text: hasRecommendation
          ? "Sellico готов показать действие на основе реальных данных."
          : "После sync/capture Sellico покажет рекомендацию или честную причину отсутствия.",
        action: "open-sellico-ads",
        label: "Sellico"
      }
    ];
  }

  function renderGuidedCaptureFlow(context, widget, dataStatus) {
    const steps = guidedCaptureSteps(context, widget);
    const doneCount = steps.filter((item) => item.done).length;
    const nextStep = steps.find((item) => !item.done);
    const header = `${doneCount}/${steps.length}`;
    return `
      <div class="sellico-panel__subsection sellico-panel__guide">
        <h4>Первый сбор evidence <span>${escapeHtml(header)}</span></h4>
        <ol class="sellico-panel__guide-list">
          ${steps.map((item, index) => `
            <li class="${item.done ? "sellico-panel__guide-item--done" : ""}">
              <strong>${index + 1}. ${escapeHtml(item.title)}</strong>
              <span>${escapeHtml(item.text)}</span>
            </li>
          `).join("")}
        </ol>
        ${nextStep ? `<div class="sellico-panel__guide-next"><span>Следующий шаг: ${escapeHtml(nextStep.title)}</span>${actionLink(nextStep.action, nextStep.label, true)}</div>` : ""}
        ${dataStatus?.confirmed_in_cabinet ? `<div class="sellico-panel__guide-note">Кабинет WB уже подтвердил live evidence; бизнес-метрики остаются за WB API.</div>` : ""}
      </div>
    `;
  }

  function hasWidgetContext(context) {
    return Boolean(
      (context?.page_type === "query" && context.query) ||
      context?.wb_product_id ||
      context?.wb_campaign_id
    );
  }

  function detectInlineSignals(context) {
    const signals = [];
    const warningNodes = Array.from(document.querySelectorAll("[role='alert'], .warning, .badge, .status, [class*='warning'], [class*='status']"));
    warningNodes.slice(0, 8).forEach((node) => {
      const text = getText(node);
      if (!text || text.length > 180) {
        return;
      }
      const normalized = text.toLowerCase();
      let signalType = "";
      let severity = "info";
      if (
        normalized.includes("ошиб") ||
        normalized.includes("недостат") ||
        normalized.includes("падает") ||
        normalized.includes("просад")
      ) {
        signalType = "wb_warning";
        severity = "high";
      } else if (
        normalized.includes("рекомен") ||
        normalized.includes("ставк") ||
        normalized.includes("конкур")
      ) {
        signalType = "wb_hint";
        severity = "medium";
      } else if (normalized.includes("актив") || normalized.includes("пауза")) {
        signalType = "wb_status";
      }
      if (!signalType) {
        return;
      }
      signals.push({
        query: context.query,
        region: context.region,
        signal_type: signalType,
        severity,
        title: text.slice(0, 90),
        message: text,
        confidence: severity === "high" ? 0.9 : 0.75,
        metadata: {
          page_type: backendPageTypeFromContext(context),
          wb_campaign_id: context.wb_campaign_id,
          wb_product_id: context.wb_product_id,
          wb_seller_cabinet_id: context.wb_seller_cabinet_id,
          page_subtype: context.page_subtype
        }
      });
    });
    return signals;
  }

  function normalizeNetworkCapture(detail) {
    if (!detail?.url) {
      return null;
    }
    const endpointKey = [...NETWORK_ENDPOINT_ALLOWLIST.entries()].find(([needle]) => detail.url.includes(needle))?.[1];
    if (!endpointKey) {
      return null;
    }
    let context = currentContext || detectContext();
    const networkCampaignID = extractWBCampaignIDFromUrl(detail.url);
    const networkProductID = extractWBProductIDFromUrl(detail.url);
    const networkQueries = extractNormQueriesFromUrl(detail.url);
    const bidCandidates = extractBidCandidatesFromPresetResponse(detail.response);
    if (currentContext) {
      let contextChanged = false;
      if (networkCampaignID && !currentContext.wb_campaign_id) {
        currentContext = {
          ...currentContext,
          page_type: currentContext.page_subtype === "promotion" ? "campaign" : backendPageTypeFromContext(currentContext),
          wb_campaign_id: networkCampaignID
        };
        contextChanged = true;
      }
      if (networkProductID && !currentContext.wb_product_id) {
        currentContext = { ...currentContext, wb_product_id: networkProductID };
        contextChanged = true;
      }
      if (!currentContext.wb_campaign_id && currentContext.page_subtype === "promotion") {
        const responseCampaignIDs = collectUniqueNetworkCampaignIDs(detail.response);
        if (responseCampaignIDs.length === 1) {
          console.info("[Sellico] Promotion network response contains one campaign id, keeping overview context:", responseCampaignIDs[0]);
        } else if (responseCampaignIDs.length > 1) {
          console.info("[Sellico] Promotion overview has multiple campaign ids in WB response:", responseCampaignIDs.length);
        }
      }
      if (contextChanged) {
        context = currentContext;
        scheduleWidgetRefresh("network-context-detected", 350);
      }
    }
    return {
      page_type: backendPageTypeFromContext(context),
      query: context.query,
      region: context.region,
      endpoint_key: endpointKey,
      payload: {
        url: detail.url,
        method: detail.method || "GET",
        status: detail.status || 0,
        request: detail.request || null,
        response: detail.response || null,
        metadata: detail.metadata || null,
        wb_campaign_id: context.wb_campaign_id || networkCampaignID,
        wb_product_id: context.wb_product_id || networkProductID,
        wb_seller_cabinet_id: context.wb_seller_cabinet_id,
        norm_queries: networkQueries,
        bid_candidates: bidCandidates,
        page_subtype: context.page_subtype
      }
    };
  }

  function valueByKeys(candidate, keys) {
    if (!candidate || typeof candidate !== "object") {
      return null;
    }
    for (const key of keys) {
      if (candidate[key] !== undefined && candidate[key] !== null && candidate[key] !== "") {
        return candidate[key];
      }
    }
    return null;
  }

  function toInt(value) {
    if (value === null || value === undefined || value === "") {
      return null;
    }
    if (typeof value === "number" && Number.isFinite(value)) {
      return Math.round(value);
    }
    const parsed = extractNumber(String(value));
    return parsed === null ? null : parsed;
  }

  function toStringValue(value) {
    if (value === null || value === undefined) {
      return "";
    }
    if (typeof value === "object") {
      const readable = value.name || value.title || value.text || value.label || value.value || value.message || value.statusText;
      if (readable !== undefined && readable !== null && readable !== value) {
        return toStringValue(readable);
      }
      return "";
    }
    const normalized = String(value).trim();
    return normalized;
  }

  function collectPayloadCandidates(value, acc = [], depth = 0) {
    if (depth > 4 || value === null || value === undefined) {
      return acc;
    }
    if (Array.isArray(value)) {
      value.slice(0, 100).forEach((item) => collectPayloadCandidates(item, acc, depth + 1));
      return acc;
    }
    if (typeof value !== "object") {
      return acc;
    }
    acc.push(value);
    Object.values(value).forEach((child) => collectPayloadCandidates(child, acc, depth + 1));
    return acc;
  }

  function extractCandidateQuery(candidate, fallbackContext) {
    return toStringValue(
      valueByKeys(candidate, [
        "norm_query",
        "normQuery",
        "query",
        "keyword",
        "phrase",
        "searchText",
        "text"
      ])
    ) || fallbackContext.query || "";
  }

  function extractCandidateRegion(candidate, fallbackContext) {
    return toStringValue(valueByKeys(candidate, ["region", "geo", "geo_name", "regionName"])) || fallbackContext.region || "";
  }

  function extractCandidateWBCampaignID(candidate, fallbackContext) {
    return toInt(
      valueByKeys(candidate, [
        "advert_id",
        "advertId",
        "campaign_id",
        "campaignId",
        "wb_campaign_id"
      ])
    ) || fallbackContext.wb_campaign_id || null;
  }

  function extractExplicitCandidateWBCampaignID(candidate) {
    if (!candidate || typeof candidate !== "object") {
      return null;
    }
    const directID = toInt(
      valueByKeys(candidate, [
        "advert_id",
        "advertId",
        "campaign_id",
        "campaignId",
        "wb_campaign_id"
      ])
    );
    if (directID) {
      return directID;
    }
    const looksLikeAdvert =
      candidate.settings ||
      candidate.nm_settings ||
      candidate.bid_type ||
      candidate.payment_type ||
      candidate.advert_type ||
      candidate.advertType;
    return looksLikeAdvert ? toInt(candidate.id) : null;
  }

  function collectUniqueNetworkCampaignIDs(response) {
    const ids = new Set();
    collectPayloadCandidates(response).forEach((candidate) => {
      const id = extractExplicitCandidateWBCampaignID(candidate);
      if (id) {
        ids.add(id);
      }
    });
    return Array.from(ids);
  }

  function extractCandidateWBProductID(candidate, fallbackContext) {
    return toInt(
      valueByKeys(candidate, [
        "nm_id",
        "nmId",
        "wb_product_id",
        "product_id",
        "productId",
        "nm"
      ])
    ) || fallbackContext.wb_product_id || null;
  }

  function extractBidMetricsFromCandidate(candidate) {
    return {
      visibleBid: toInt(valueByKeys(candidate, ["bid", "cpm", "current_bid", "visible_bid"])),
      recommendedBid: toInt(valueByKeys(candidate, ["recommended_bid", "recommendedBid"])),
      competitiveBid: toInt(valueByKeys(candidate, ["competitive_bid", "competitiveBid"])),
      leadershipBid: toInt(valueByKeys(candidate, ["leadership_bid", "leadershipBid"])),
      cpmMin: toInt(valueByKeys(candidate, ["cpmMin", "cpm_min", "min_bid", "minBid"]))
    };
  }

  function extractPositionMetricsFromCandidate(candidate) {
    return {
      visiblePosition: toInt(valueByKeys(candidate, ["position", "place", "visible_position"])),
      visiblePage: toInt(valueByKeys(candidate, ["page", "page_num", "pageNum"])),
      pageSubtype: toStringValue(valueByKeys(candidate, ["page_subtype", "placement", "source"]))
    };
  }

  function buildNetworkDerivedBidItems(payload, fallbackContext) {
    const response = payload?.payload?.response;
    const candidates = collectPayloadCandidates(response);
    const items = [];
    const seen = new Set();

    candidates.forEach((candidate) => {
      const metrics = extractBidMetricsFromCandidate(candidate);
      if (
        metrics.visibleBid === null &&
        metrics.recommendedBid === null &&
        metrics.competitiveBid === null &&
        metrics.leadershipBid === null &&
        metrics.cpmMin === null
      ) {
        return;
      }
      const query = extractCandidateQuery(candidate, fallbackContext);
      const wbCampaignId = extractCandidateWBCampaignID(candidate, fallbackContext);
      const wbProductId = extractCandidateWBProductID(candidate, fallbackContext);
      if (!query) {
        return;
      }
      const key = [
        query,
        fallbackContext.region || "",
        metrics.visibleBid || "",
        metrics.recommendedBid || "",
        wbCampaignId || "",
        wbProductId || ""
      ].join("|");
      if (seen.has(key)) {
        return;
      }
      seen.add(key);
      items.push({
        query: query || undefined,
        region: extractCandidateRegion(candidate, fallbackContext) || undefined,
        visible_bid: metrics.visibleBid ?? undefined,
        recommended_bid: metrics.recommendedBid ?? undefined,
        competitive_bid: metrics.competitiveBid ?? undefined,
        leadership_bid: metrics.leadershipBid ?? undefined,
        cpm_min: metrics.cpmMin ?? undefined,
        confidence: 0.82,
        metadata: {
          page_type: backendPageTypeFromContext(fallbackContext),
          wb_campaign_id: wbCampaignId,
          wb_product_id: wbProductId,
          wb_seller_cabinet_id: fallbackContext.wb_seller_cabinet_id,
          page_subtype: fallbackContext.page_subtype,
          source_endpoint: payload.endpoint_key,
          source_url: payload.payload?.url
        }
      });
    });

    return items.slice(0, 12);
  }

  function buildNetworkDerivedPositionItems(payload, fallbackContext) {
    const response = payload?.payload?.response;
    const candidates = collectPayloadCandidates(response);
    const items = [];
    const seen = new Set();

    candidates.forEach((candidate) => {
      const metrics = extractPositionMetricsFromCandidate(candidate);
      if (!metrics.visiblePosition) {
        return;
      }
      const query = extractCandidateQuery(candidate, fallbackContext);
      const region = extractCandidateRegion(candidate, fallbackContext);
      const wbProductId = extractCandidateWBProductID(candidate, fallbackContext);
      const wbCampaignId = extractCandidateWBCampaignID(candidate, fallbackContext);
      if (!query || !region || !wbProductId) {
        return;
      }
      const key = [query, region, metrics.visiblePosition, metrics.visiblePage || "", wbProductId].join("|");
      if (seen.has(key)) {
        return;
      }
      seen.add(key);
      items.push({
        query,
        region,
        visible_position: metrics.visiblePosition,
        visible_page: metrics.visiblePage ?? undefined,
        page_subtype: metrics.pageSubtype || fallbackContext.page_subtype || undefined,
        confidence: 0.8,
        metadata: {
          page_type: backendPageTypeFromContext(fallbackContext),
          wb_campaign_id: wbCampaignId,
          wb_product_id: wbProductId,
          wb_seller_cabinet_id: fallbackContext.wb_seller_cabinet_id,
          page_subtype: metrics.pageSubtype || fallbackContext.page_subtype,
          source_endpoint: payload.endpoint_key,
          source_url: payload.payload?.url
        }
      });
    });

    return items.slice(0, 12);
  }

  function buildNetworkDerivedSignals(payload, fallbackContext) {
    const response = payload?.payload?.response;
    const candidates = collectPayloadCandidates(response);
    const items = [];
    const seen = new Set();

    candidates.forEach((candidate) => {
      const warning = toStringValue(valueByKeys(candidate, ["warning", "notice", "statusText", "message", "hint"]));
      const status = toStringValue(valueByKeys(candidate, ["status", "state"]));
      const rawText = warning || status;
      if (!rawText) {
        return;
      }
      const normalized = rawText.toLowerCase();
      let signalType = "";
      let severity = "info";
      if (normalized.includes("error") || normalized.includes("ошиб") || normalized.includes("недостат")) {
        signalType = "wb_warning";
        severity = "high";
      } else if (normalized.includes("recommend") || normalized.includes("рекомен") || normalized.includes("ставк")) {
        signalType = "wb_hint";
        severity = "medium";
      } else if (status) {
        signalType = "wb_status";
      }
      if (!signalType) {
        return;
      }
      const query = extractCandidateQuery(candidate, fallbackContext);
      const region = extractCandidateRegion(candidate, fallbackContext);
      const wbCampaignId = extractCandidateWBCampaignID(candidate, fallbackContext);
      const wbProductId = extractCandidateWBProductID(candidate, fallbackContext);
      const title = rawText.slice(0, 90);
      const key = [signalType, title, query, region, wbCampaignId || "", wbProductId || ""].join("|");
      if (seen.has(key)) {
        return;
      }
      seen.add(key);
      items.push({
        query: query || undefined,
        region: region || undefined,
        signal_type: signalType,
        severity,
        title,
        message: rawText,
        confidence: 0.76,
        metadata: {
          page_type: backendPageTypeFromContext(fallbackContext),
          wb_campaign_id: wbCampaignId,
          wb_product_id: wbProductId,
          wb_seller_cabinet_id: fallbackContext.wb_seller_cabinet_id,
          page_subtype: fallbackContext.page_subtype,
          source_endpoint: payload.endpoint_key,
          source_url: payload.payload?.url
        }
      });
    });

    return items.slice(0, 10);
  }

  function queueNetworkDerivedPayloads(payload, fallbackContext) {
    const bidItems = buildNetworkDerivedBidItems(payload, fallbackContext);
    const positionItems = buildNetworkDerivedPositionItems(payload, fallbackContext);
    const signalItems = buildNetworkDerivedSignals(payload, fallbackContext);
    let queuedAny = false;

    bidItems.forEach((item) => {
      const key = [
        item.query || "",
        item.region || "",
        item.visible_bid || "",
        item.recommended_bid || "",
        item.metadata?.wb_campaign_id || "",
        item.metadata?.wb_product_id || "",
        item.metadata?.source_endpoint || ""
      ].join("|");
      queuedAny = queueItem(bidQueue, key, item) || queuedAny;
    });
    if (queuedAny && bidItems.some(hasBackendBidContext)) {
      scheduleFlush("bid");
    }

    positionItems.forEach((item) => {
      const key = [
        item.query || "",
        item.region || "",
        item.visible_position || "",
        item.visible_page || "",
        item.metadata?.wb_product_id || "",
        item.metadata?.source_endpoint || ""
      ].join("|");
      queuedAny = queueItem(positionQueue, key, item) || queuedAny;
    });
    if (positionItems.length) {
      scheduleFlush("position");
    }

    signalItems.forEach((item) => {
      const key = [
        item.signal_type,
        item.title,
        item.query || "",
        item.region || "",
        item.metadata?.wb_campaign_id || "",
        item.metadata?.wb_product_id || "",
        item.metadata?.source_endpoint || ""
      ].join("|");
      queuedAny = queueItem(uiSignalQueue, key, item) || queuedAny;
    });
    if (signalItems.length) {
      scheduleFlush("uiSignals");
    }

    return queuedAny;
  }

  function installPageBridge() {
    if (document.getElementById("sellico-page-bridge")) {
      return;
    }
    const script = document.createElement("script");
    script.id = "sellico-page-bridge";
    script.src = chrome.runtime.getURL("page-bridge.js");
    script.async = false;
    script.dataset.sellicoNonce = pageBridgeNonce;
    (document.head || document.documentElement).appendChild(script);
  }

  function createPanel() {
    let root = document.getElementById(PANEL_ID);
    if (root) {
      return root;
    }
    root = document.createElement("aside");
    root.id = PANEL_ID;
    root.setAttribute(ROOT_ATTR, "true");
    root.innerHTML = `
      <div class="sellico-panel__header">
        <div class="sellico-panel__logo">
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" aria-hidden="true">
            <path fill="currentColor" d="M12 2 4 6.4v11.2L12 22l8-4.4V6.4L12 2Zm0 2.3 5.8 3.2L12 10.7 6.2 7.5 12 4.3Zm-6 5 5 2.8v6.7l-5-2.8V9.3Zm7 9.5v-6.7l5-2.8V16l-5 2.8Z"/>
          </svg>
        </div>
        <div class="sellico-panel__header-content">
          <p class="sellico-panel__eyebrow">Sellico Live</p>
          <h3 class="sellico-panel__title">Загрузка контекста...</h3>
          <p class="sellico-panel__subtitle">Кабинет Wildberries</p>
        </div>
        <button class="sellico-panel__minimize" type="button" aria-label="Свернуть">−</button>
      </div>
      <div class="sellico-panel__body">
        <div class="sellico-panel__section" data-role="status"></div>
        <div class="sellico-panel__section" data-role="summary"></div>
        <div class="sellico-panel__section" data-role="signals"></div>
        <div class="sellico-panel__section" data-role="actions"></div>
      </div>
    `;
    document.body.appendChild(root);

    const header = root.querySelector(".sellico-panel__header");
    const minimizeBtn = root.querySelector(".sellico-panel__minimize");

    const toggleCollapse = () => {
      const isCollapsed = root.classList.toggle("sellico-panel--collapsed");
      minimizeBtn.textContent = isCollapsed ? "+" : "−";
      minimizeBtn.setAttribute("aria-label", isCollapsed ? "Развернуть" : "Свернуть");
    };

    minimizeBtn.addEventListener("click", (e) => {
      e.stopPropagation();
      toggleCollapse();
    });

    header.addEventListener("click", toggleCollapse);
    return root;
  }

  function renderList(title, items, emptyText) {
    const safeItems = items.filter(Boolean);
    if (!safeItems.length) {
      return `<div class="sellico-panel__empty">${escapeHtml(emptyText)}</div>`;
    }
    return `
      <div class="sellico-panel__subsection">
        <h4>${escapeHtml(title)}</h4>
        <ul>${safeItems.map((item) => `<li>${item}</li>`).join("")}</ul>
      </div>
    `;
  }

  function localizePageType(value) {
    const normalized = String(value || "").toLowerCase();
    const labels = {
      auction: "Аукцион",
      cabinet: "Кабинет",
      campaign: "Кампания",
      promotion: "Продвижение",
      product: "Товар",
      query: "Запрос",
      search: "Поиск"
    };
    return labels[normalized] || "Кабинет WB";
  }

  function localizeContextMode(context) {
    return localizePageType(context?.page_subtype || context?.page_type);
  }

  function localizeFreshness(value) {
    const normalized = String(value || "").toLowerCase();
    const labels = {
      fresh: "актуальные",
      ready: "готовы",
      aging: "стареют",
      stale: "устарели",
      partial: "частичные",
      empty: "нет данных",
      no_live_capture: "нет live-снимка",
      unknown: "неизвестно"
    };
    return labels[normalized] || "проверяется";
  }

  function localizeCoverage(value) {
    const normalized = String(value || "").toLowerCase();
    const labels = {
      full: "полное",
      high: "высокое",
      medium: "среднее",
      partial: "частичное",
      low: "низкое",
      none: "нет"
    };
    return labels[normalized] || "частичное";
  }

  function humanizeSource(dataStatus) {
    if (!dataStatus) {
      return "Sellico API";
    }
    if (dataStatus.confirmed_in_cabinet) {
      return "Кабинет WB";
    }
    const source = String(dataStatus.source || "").toLowerCase();
    if (source === "mixed") {
      return "API + кабинет";
    }
    if (source === "extension") {
      return "Кабинет WB";
    }
    if (source === "api") {
      return "WB API";
    }
    if (source === "derived") {
      return "Расчет Sellico";
    }
    return "Sellico API";
  }

  function summarizeRecommendation(item) {
    const action = item?.suggested_action || item?.next_action || "";
    return action || item?.title || item?.type || "Открыть Sellico";
  }

  function recommendationTone(item) {
    const normalized = `${item?.type || ""} ${item?.title || ""} ${item?.suggested_action || ""}`.toLowerCase();
    if (normalized.includes("drop") || normalized.includes("слив") || normalized.includes("waste") || normalized.includes("save")) {
      return "danger";
    }
    if (normalized.includes("scale") || normalized.includes("рост") || normalized.includes("profitable")) {
      return "success";
    }
    if (normalized.includes("research") || normalized.includes("competitor") || normalized.includes("pressure")) {
      return "warning";
    }
    return "info";
  }

  function renderActions(items, emptyText) {
    const safeItems = items.filter(Boolean);
    if (!safeItems.length) {
      return `<div class="sellico-panel__empty">${escapeHtml(emptyText)}</div>`;
    }
    return `
      <div class="sellico-panel__subsection">
        <h4>Действия</h4>
        <div class="sellico-panel__actions">${safeItems.join("")}</div>
      </div>
    `;
  }

  function actionLink(action, label, primary = false) {
    const className = primary ? "sellico-panel__action sellico-panel__action--primary" : "sellico-panel__action";
    return `<a href="#" class="${className}" data-sellico-action="${escapeHtml(action)}">${escapeHtml(label)}</a>`;
  }

  function dataStatusActionLinks(dataStatus) {
    const allowedActions = new Set(["refresh", "open-settings", "open-wb-promotion", "open-sellico-ads"]);
    return (dataStatus?.next_actions || dataStatus?.nextActions || [])
      .map((item) => {
        const action = item.action_path || item.actionPath || item.id;
        const label = item.label || "Выполнить";
        if (!allowedActions.has(action)) {
          return null;
        }
        return actionLink(action, label, item.tone === "primary");
      })
      .filter(Boolean);
  }

  function primaryInsightActionLinks(widget) {
    const allowedActions = new Set(["refresh", "open-wb-promotion", "open-sellico-ads"]);
    const primaryInsight = widget?.primary_insight || widget?.primaryInsight;
    const actionItem = primaryInsight?.next_action || primaryInsight?.nextAction;
    if (!actionItem) {
      return [];
    }
    const action = actionItem.action_path || actionItem.actionPath || actionItem.id;
    if (!allowedActions.has(action)) {
      return [];
    }
    return [actionLink(action, actionItem.label || "Выполнить", actionItem.tone === "primary")];
  }

  function dataStatusIssueItems(dataStatus) {
    return (dataStatus?.issues || [])
      .slice(0, 3)
      .map((item) => `<strong>${escapeHtml(item.stage || "Статус данных")}</strong><span>${escapeHtml(item.message || "")}</span>`);
  }

  function formatMoneyRub(value) {
    const amount = Number(value || 0);
    return `₽${Math.round(amount).toLocaleString("ru-RU")}`;
  }

  function formatNumber(value) {
    return Number(value || 0).toLocaleString("ru-RU");
  }

  function formatRate(value, digits = 1) {
    if (!Number.isFinite(value)) {
      return "—";
    }
    return `${value.toFixed(digits)}%`;
  }

  function aggregateWidgetStats(stats) {
    return (stats || []).reduce((acc, stat) => {
      acc.spend += Number(stat.spend || 0);
      acc.revenue += Number(stat.revenue || 0);
      acc.orders += Number(stat.orders || 0);
      acc.clicks += Number(stat.clicks || 0);
      acc.impressions += Number(stat.impressions || 0);
      return acc;
    }, { spend: 0, revenue: 0, orders: 0, clicks: 0, impressions: 0 });
  }

  function buildValueFirstItems(context, widget, dataStatus) {
    const items = [];
    const primaryInsight = widget?.primary_insight || widget?.primaryInsight;
    if (primaryInsight?.title || primaryInsight?.message) {
      const evidence = Array.isArray(primaryInsight.evidence) && primaryInsight.evidence.length
        ? ` · ${primaryInsight.evidence.slice(0, 2).join(", ")}`
        : "";
      items.push(`<strong>${escapeHtml(primaryInsight.title || "Что важно сейчас")}</strong><span>${escapeHtml(`${primaryInsight.message || ""}${evidence}`)}</span>`);
    }

    const stats = aggregateWidgetStats(widget?.stats);
    if (stats.spend > 0 || stats.revenue > 0 || stats.orders > 0) {
      const parts = [
        `расход ${formatMoneyRub(stats.spend)}`,
        `выручка ${formatMoneyRub(stats.revenue)}`,
        `заказы ${formatNumber(stats.orders)}`
      ];
      if (stats.revenue > 0) {
        parts.push(`ДРР ${formatRate((stats.spend / stats.revenue) * 100, 2)}`);
      }
      items.push(`<strong>Реклама за период</strong><span>${escapeHtml(parts.join(" · "))}</span>`);
    }

    const liveBid = widget?.live_bid_snapshot || (widget?.live_bids || [])[0];
    if (liveBid) {
      const bidParts = [];
      if (liveBid.visible_bid) {
        bidParts.push(`текущая ${formatMoneyRub(liveBid.visible_bid)}`);
      }
      if (liveBid.recommended_bid) {
        bidParts.push(`рекомендованная ${formatMoneyRub(liveBid.recommended_bid)}`);
      }
      if (liveBid.competitive_bid) {
        bidParts.push(`конкурентная ${formatMoneyRub(liveBid.competitive_bid)}`);
      }
      if (liveBid.cpm_min) {
        bidParts.push(`минимум ${formatMoneyRub(liveBid.cpm_min)}`);
      }
      if (bidParts.length) {
        items.push(`<strong>Ставка из кабинета WB</strong><span>${escapeHtml(bidParts.join(" · "))}</span>`);
      }
    }

    const livePosition = (widget?.live_positions || [])[0];
    if (livePosition?.visible_position) {
      const details = [
        `позиция ${formatNumber(livePosition.visible_position)}`,
        livePosition.query || context.query || "",
        livePosition.region || context.region || ""
      ].filter(Boolean);
      items.push(`<strong>Позиция в кабинете</strong><span>${escapeHtml(details.join(" · "))}</span>`);
    }

    if (dataStatus?.confirmed_in_cabinet) {
      items.push(`<strong>Данные подтверждены кабинетом</strong><span>${escapeHtml(localizeFreshness(dataStatus.freshness_state))} · ${escapeHtml(humanizeSource(dataStatus))}</span>`);
    }

    return items;
  }

  function renderPanel(context, widget) {
    const panel = createPanel();
    const title = panel.querySelector(".sellico-panel__title");
    const status = panel.querySelector('[data-role="status"]');
    const summary = panel.querySelector('[data-role="summary"]');
    const signals = panel.querySelector('[data-role="signals"]');
    const actions = panel.querySelector('[data-role="actions"]');

    title.textContent =
      widget?.campaign?.name ||
      widget?.product?.name ||
      widget?.product?.title ||
      widget?.phrase?.keyword ||
      context.query ||
      "Контекст WB";
    const subtitle = panel.querySelector(".sellico-panel__subtitle");
    if (subtitle) {
      subtitle.textContent = localizeContextMode(context);
    }

    if (!widget) {
      const statusDiv = document.createElement("div");
      statusDiv.className = "sellico-panel__status-grid";
      statusDiv.innerHTML = `<div><span>Режим</span><strong></strong></div><div><span>Данные</span><strong></strong></div>`;
      statusDiv.querySelectorAll("strong")[0].textContent = localizeContextMode(context);
      statusDiv.querySelectorAll("strong")[1].textContent = lastWidgetError
        ? "ошибка"
        : lastWidgetState === "unsupported_context"
          ? "контекст не выбран"
          : lastWidgetState === "campaign_sync_required"
            ? "кампания не синхронизирована"
          : lastWidgetState === "product_sync_required"
            ? "товар не синхронизирован"
          : "нужна синхронизация";
      status.innerHTML = "";
      status.appendChild(statusDiv);

      if (lastWidgetError) {
        const configError = isConfigurationError(lastWidgetError);
        summary.innerHTML = `
          <div class="sellico-panel__empty sellico-panel__empty--error">
            <strong>${configError ? "Sellico Live не подключен" : "Ошибка загрузки данных"}</strong><br><br>
            ${escapeHtml(lastWidgetError)}<br><br>
            ${configError
              ? "Откройте настройки расширения и нажмите «Войти и подключить». Для разработки можно указать Backend URL, Workspace ID и Access token вручную."
              : "Нажмите «Повторить» для повторной попытки."}
          </div>
        `;
      } else if (lastWidgetState === "unsupported_context") {
        const isPromotionOverview = context.page_subtype === "promotion";
        summary.innerHTML = `
          <div class="sellico-panel__empty sellico-panel__empty--notice">
            <strong>${isPromotionOverview ? "Раздел продвижения открыт" : "Страница WB без детального контекста"}</strong><br><br>
            ${isPromotionOverview
              ? "Sellico уже собирает реальные сетевые ответы и сигналы страницы. Для карточки рекомендаций выберите конкретную кампанию, товар или поисковый запрос: на общем списке WB часто показывает артикулы товаров внутри названий кампаний, и Sellico не будет выдавать их за ID кампании."
              : "Sellico уже собирает реальные сигналы страницы, но для рекомендаций нужен выбранный объект: кампания, товар или поисковый запрос."}<br><br>
            ${escapeHtml(captureStateText())}${evidenceSummaryText() ? `<br>${escapeHtml(evidenceSummaryText())}` : ""}<br><br>
            ${isPromotionOverview ? "Если нужная кампания уже открыта, нажмите «Собрать сигналы страницы» после загрузки таблицы." : "Откройте раздел продвижения, конкретную кампанию или товар, чтобы увидеть аналитику и подсказки."}
          </div>
        `;
      } else if (lastWidgetState === "campaign_sync_required") {
        summary.innerHTML = `
          <div class="sellico-panel__empty sellico-panel__empty--notice">
            <strong>Кампания найдена в WB, но ее нет в Sellico</strong><br><br>
            Расширение распознало реальную кампанию WB${context.wb_campaign_id ? ` №${escapeHtml(context.wb_campaign_id)}` : ""}, но backend не нашел ее в текущем workspace.<br><br>
            Проверьте выбранный workspace и запустите синхронизацию рекламного кабинета. До синхронизации Sellico будет только собирать сигналы страницы без рекомендаций.
          </div>
        `;
      } else if (lastWidgetState === "product_sync_required") {
        summary.innerHTML = `
          <div class="sellico-panel__empty sellico-panel__empty--notice">
            <strong>Товар найден в WB, но его нет в Sellico</strong><br><br>
            Расширение распознало реальный артикул WB${context.wb_product_id ? ` №${escapeHtml(context.wb_product_id)}` : ""}, но backend не нашел его в текущем workspace.<br><br>
            Проверьте workspace и запустите синхронизацию. До синхронизации Sellico будет только собирать сигналы страницы без рекомендаций.
          </div>
        `;
      } else {
        summary.innerHTML = `
          <div class="sellico-panel__empty sellico-panel__empty--notice">
            <strong>Нет данных от Sellico</strong><br><br>
            Для отображения рекомендаций и данных:<br>
            1. Подключи кабинет WB в Sellico<br>
            2. Запусти синхронизацию<br>
            3. Перезагрузи страницу WB
            ${evidenceSummaryText() ? `<br><br>${escapeHtml(evidenceSummaryText())}` : ""}
          </div>
        `;
      }
      const emptyChecklist = captureChecklistItems(context, widget, null);
      signals.innerHTML = renderList("Сигналы workspace", liveCaptureSignalItems().concat(evidenceSummaryItems()), "Сигналы workspace пока не собраны.") +
        renderGuidedCaptureFlow(context, widget, null) +
        renderChecklist("Чеклист захвата", emptyChecklist, "Откройте конкретный объект WB, чтобы начать сбор real evidence.");
      const links = [];
      if (isConfigurationError(lastWidgetError)) {
        links.push(actionLink("open-settings", "Открыть настройки", true));
      }
      if (lastWidgetState === "unsupported_context" && !lastWidgetError) {
        links.push(actionLink("open-wb-promotion", "Открыть продвижение", true));
        links.push(actionLink("refresh", "Собрать сигналы страницы"));
      } else if ((lastWidgetState === "campaign_sync_required" || lastWidgetState === "product_sync_required") && !lastWidgetError) {
        links.push(actionLink("open-sellico-ads", "Открыть Sellico", true));
        links.push(actionLink("refresh", "Проверить еще раз"));
      } else {
        links.push(actionLink("refresh", lastWidgetError ? "Повторить" : "Собрать данные страницы", !isConfigurationError(lastWidgetError)));
      }
      actions.innerHTML = renderActions(links, "");
      bindPanelActions(panel, context);
      renderInlineBadges(context, widget);
      return;
    }

    // Clear error on successful widget load
    lastWidgetError = null;

    const dataStatus = widget?.data_status || widget?.dataStatus;
    const statusGrid = document.createElement("div");
    statusGrid.className = "sellico-panel__status-grid";
    statusGrid.innerHTML = `
      <div><span>Режим</span><strong></strong></div>
      <div><span>Источник</span><strong></strong></div>
      <div><span>Свежесть</span><strong></strong></div>
      <div><span>Покрытие</span><strong></strong></div>
    `;
    const vals = statusGrid.querySelectorAll("strong");
    vals[0].textContent = localizeContextMode(context);
    vals[1].textContent = humanizeSource(dataStatus);
    vals[2].textContent = localizeFreshness(dataStatus?.freshness_state);
    vals[3].textContent = localizeCoverage(dataStatus?.coverage);
    status.innerHTML = "";
    status.appendChild(statusGrid);

    const valueItems = buildValueFirstItems(context, widget, dataStatus);
    const issueItems = dataStatusIssueItems(dataStatus);
    const recommendationItems = (widget?.recommendations || []).slice(0, 3).map((item) => {
      const action = escapeHtml(summarizeRecommendation(item));
      return `<strong>${escapeHtml(item.title || item.type)}</strong><span>${action}</span>`;
    });
    const signalItems = liveCaptureSignalItems()
      .concat(evidenceSummaryItems())
      .concat((widget?.ui_signals || []).slice(0, 3).map((item) => `<strong>${escapeHtml(item.title)}</strong><span>${escapeHtml(item.message)}</span>`))
      .concat((widget?.live_positions || []).slice(0, 2).map((item) => `<strong>Позиция ${escapeHtml(item.visible_position || "—")}</strong><span>${escapeHtml(item.query || context.query || "")}</span>`))
      .concat((widget?.live_bids || []).slice(0, 2).map((item) => `<strong>Ставка ${escapeHtml(item.visible_bid || "—")}</strong><span>${escapeHtml(item.query || context.query || "")}</span>`))
      .concat(widget?.live_bid_snapshot ? [`<strong>Ставка ${escapeHtml(widget.live_bid_snapshot.visible_bid || "—")}</strong><span>${escapeHtml(context.query || "")}</span>`] : []);

    summary.innerHTML = renderList(
      "Что важно сейчас",
      valueItems.concat(issueItems, recommendationItems).slice(0, 5),
      "Sellico не нашёл срочных действий на этой странице."
    );
    const scopedDebugItem = evidenceDebugText()
      ? [`<strong>Evidence объекта</strong><span>${escapeHtml(evidenceDebugText())}</span>`]
      : [];
    signals.innerHTML = renderList("Сигналы из кабинета", scopedDebugItem.concat(signalItems), "На странице пока нет подтвержденных сигналов.") +
      renderGuidedCaptureFlow(context, widget, dataStatus) +
      renderChecklist("Чеклист захвата", captureChecklistItems(context, widget, dataStatus), "Откройте объект WB, чтобы начать сбор real evidence.");

    const links = primaryInsightActionLinks(widget).concat(dataStatusActionLinks(dataStatus));
    const bidMetrics = detectVisibleBidMetrics();
    const hasSingleLiveQuery = liveCaptureState.queryCandidates.length === 1;
    if ((context.query || hasSingleLiveQuery) && bidMetrics.confidence > 0) {
      links.push(actionLink("save-bid", "Сохранить ставку"));
    }
    const positionMetrics = detectVisiblePositionMetrics(context);
    if (context.wb_product_id && context.query && context.region && positionMetrics.visiblePosition) {
      links.push(actionLink("save-position", "Сохранить позицию"));
    }
    if (!links.some((item) => item.includes('data-sellico-action="refresh"'))) {
      links.push(actionLink("refresh", "Обновить контекст", true));
    }
    actions.innerHTML = renderActions(links, "Нет быстрых действий.");
    bindPanelActions(panel, context);
    renderInlineBadges(context, widget);
  }

  function bindPanelActions(panel, context) {
    panel.querySelectorAll("[data-sellico-action]").forEach((link) => {
      link.addEventListener("click", async (event) => {
        event.preventDefault();
        const action = link.getAttribute("data-sellico-action");
        const previousText = link.textContent;
        link.textContent = "Выполняю...";
        try {
          if (action === "refresh") {
            await bootstrap("manual-refresh");
            return;
          }
          if (action === "open-settings") {
            await sendMessage({ type: "extension:open-options" });
            return;
          }
          if (action === "open-wb-promotion") {
            window.location.href = "https://cmp.wildberries.ru/";
            return;
          }
          if (action === "open-sellico-ads") {
            window.open("https://sellico.ru/ads-intelligence", "_blank", "noopener");
            return;
          }
          if (action === "save-position") {
            const saved = await persistVisiblePosition(context, true);
            if (!saved) {
              throw new Error("На странице не удалось распознать позицию товара");
            }
            scheduleWidgetRefresh("manual-position", 150);
            return;
          }
          if (action === "save-bid") {
            const saved = await persistVisibleBid(context, true);
            if (!saved) {
              throw new Error("На странице не удалось распознать ставку");
            }
            scheduleWidgetRefresh("manual-bid", 150);
          }
        } catch (error) {
          lastWidgetError = logHandledError("Panel action failed", action, error);
          renderPanel(context, null);
        } finally {
          link.textContent = previousText;
        }
      });
    });
  }

  function detectVisibleBidMetrics() {
    const visibleBid = findMetricBySelectors(BID_SELECTORS.visible);
    const recommendedBid = findMetricBySelectors(BID_SELECTORS.recommended) ?? findMetricByKeywords(["рекомен", "recommended"]);
    const competitiveBid = findMetricBySelectors(BID_SELECTORS.competitive) ?? findMetricByKeywords(["конкурент", "competition"]);
    const leadershipBid = findMetricBySelectors(BID_SELECTORS.leadership) ?? findMetricByKeywords(["лидер", "first place", "первое место"]);
    const cpmMin = findMetricBySelectors(BID_SELECTORS.cpmMin) ?? findMetricByKeywords(["минимальная ставка", "cpm min", "минимум"]);
    const hasAny =
      visibleBid !== null ||
      recommendedBid !== null ||
      competitiveBid !== null ||
      leadershipBid !== null ||
      cpmMin !== null;
    return {
      visibleBid,
      recommendedBid,
      competitiveBid,
      leadershipBid,
      cpmMin,
      confidence: visibleBid !== null ? 0.88 : hasAny ? 0.68 : 0
    };
  }

  function detectVisiblePositionMetrics(context) {
    const visiblePosition = findMetricBySelectors(POSITION_SELECTORS.visible) ?? findMetricByKeywords(["позиция", "место", "position", "place"]);
    const visiblePage = findMetricBySelectors(POSITION_SELECTORS.page) ?? findMetricByKeywords(["страница", "page"]);
    const pageSubtype = detectPageSubtype() || context.page_subtype;
    return {
      visiblePosition,
      visiblePage,
      pageSubtype,
      confidence: visiblePosition !== null ? 0.84 : 0
    };
  }

  function sameContext(expectedToken, contextURL, bootstrapTimestamp) {
    if (expectedToken !== currentBootstrapToken || currentContext?.url !== contextURL) {
      return false;
    }
    // If a timestamp was captured at bootstrap start, reject if a newer bootstrap has started
    if (bootstrapTimestamp && bootstrapTimestamp < lastBootstrapAt) {
      return false;
    }
    return true;
  }

  function queueItem(queue, key, item) {
    if (!item) {
      return false;
    }
    queue.set(key, item);
    return true;
  }

  function prepareQueuedItem(kind, item) {
    if (!item || typeof item !== "object") {
      return null;
    }
    if (kind === "network") {
      const pageType = toBackendPageType(item.page_type);
      if (!item.endpoint_key || !item.payload || !BACKEND_PAGE_TYPES.has(pageType)) {
        return null;
      }
      if (estimateJSONBytes(item.payload) > MAX_NETWORK_PAYLOAD_BYTES) {
        console.info("[Sellico] Network capture skipped: payload is too large for safe ingest", item.endpoint_key);
        return null;
      }
      return { ...item, page_type: pageType };
    }
    if (kind === "domRows") {
      const pageType = toBackendPageType(item.page_type);
      if (!BACKEND_PAGE_TYPES.has(pageType) || !hasNonEmptyText(item.table_role) || !hasNonEmptyText(item.row_key) || !hasNonEmptyText(item.visible_text)) {
        return null;
      }
      return { ...item, page_type: pageType };
    }
    if (kind === "bid") {
      if (!hasBackendBidContext(item)) {
        return null;
      }
      return item;
    }
    if (kind === "position") {
      const hasProductContext = Boolean(item.product_id || item.metadata?.wb_product_id || item.metadata?.wbProductID);
      if (!hasProductContext || !hasNonEmptyText(item.query) || !hasNonEmptyText(item.region) || !item.visible_position) {
        return null;
      }
      return item;
    }
    if (kind === "uiSignals") {
      if (!item.signal_type || !item.title) {
        return null;
      }
      return item;
    }
    return item;
  }

  function estimateJSONBytes(value) {
    try {
      return new Blob([JSON.stringify(value)]).size;
    } catch (_err) {
      return Number.MAX_SAFE_INTEGER;
    }
  }

  function chunkItems(items, size) {
    const chunks = [];
    for (let index = 0; index < items.length; index += size) {
      chunks.push(items.slice(index, index + size));
    }
    return chunks;
  }

  function scheduleFlush(kind) {
    if (flushTimers[kind]) {
      clearTimeout(flushTimers[kind]);
    }
    flushTimers[kind] = window.setTimeout(async () => {
      flushTimers[kind] = null;
      await flushQueue(kind);
    }, FLUSH_DELAYS_MS[kind]);
  }

  async function flushQueue(kind) {
    const queue =
      kind === "network"
        ? networkCaptureQueue
        : kind === "domRows"
          ? domRowQueue
          : kind === "uiSignals"
            ? uiSignalQueue
            : kind === "bid"
              ? bidQueue
              : positionQueue;

    if (!queue.size) {
      return false;
    }

    const rawItems = Array.from(queue.values());
    queue.clear();
    const items = rawItems
      .map((item) => prepareQueuedItem(kind, item))
      .filter(Boolean);

    if (!items.length) {
      console.info(`[Sellico] Extension ${kind} queue skipped: no valid items for backend contract`);
      return false;
    }

    const messageType =
      kind === "network"
        ? "extension:create-network-captures"
        : kind === "domRows"
          ? "extension:create-dom-row-snapshots"
          : kind === "uiSignals"
            ? "extension:create-ui-signals"
            : kind === "bid"
              ? "extension:create-bid-snapshots"
              : "extension:create-position-snapshots";

    try {
      const batches =
        kind === "network"
          ? chunkItems(items, MAX_NETWORK_BATCH_ITEMS)
          : kind === "domRows"
            ? chunkItems(items, MAX_DOM_ROW_BATCH_ITEMS)
            : [items];
      for (const batch of batches) {
        await sendMessage({
          type: messageType,
          items: batch
        });
      }
      scheduleWidgetRefresh(`flush-${kind}`);
      return true;
    } catch (error) {
      logHandledError("Failed to flush extension queue", kind, error);
      if (!isBackendValidationMessage(error?.message || error)) {
        items.forEach((item, index) => queue.set(`retry-${Date.now()}-${index}`, item));
      }
      return false;
    }
  }

  function scheduleWidgetRefresh(_reason, delay = FLUSH_DELAYS_MS.widget) {
    if (widgetRefreshTimer) {
      clearTimeout(widgetRefreshTimer);
    }
    // Capture current state in closure to avoid race with navigation
    const capturedContext = currentContext;
    const capturedToken = currentBootstrapToken;
    const capturedTimestamp = lastBootstrapAt;
    widgetRefreshTimer = window.setTimeout(() => {
      widgetRefreshTimer = null;
      refreshWidget(capturedContext, capturedToken, capturedTimestamp);
    }, delay);
  }

  function scheduleBadgeRefresh(delay = FLUSH_DELAYS_MS.badges) {
    if (badgeRefreshTimer) {
      clearTimeout(badgeRefreshTimer);
    }
    badgeRefreshTimer = window.setTimeout(() => {
      badgeRefreshTimer = null;
      renderInlineBadges(currentContext, currentWidget);
    }, delay);
  }

  async function refreshWidget(context, bootstrapToken, bootstrapTimestamp) {
    if (!context || !sameContext(bootstrapToken, context.url, bootstrapTimestamp)) {
      return;
    }

    const widgetRequest = buildWidgetRequest(context);
    currentWidget = null;
    currentEvidenceDebug = null;
    lastWidgetError = null;
    const evidenceSummaryPromise = sendMessage({ type: "extension:fetch-evidence-summary" })
      .then((response) => {
        workspaceEvidenceSummary = response?.summary || null;
      })
      .catch((error) => {
        workspaceEvidenceSummary = {
          unavailable: true,
          message: normalizeRuntimeErrorMessage(error?.message || error)
        };
      });
    if (widgetRequest) {
      lastWidgetState = "loading";
      try {
        debugLog("[Sellico] Sending widget request:", widgetRequest);
        const response = await sendMessage(widgetRequest);
        debugLog("[Sellico] Widget response:", response);
        if (!sameContext(bootstrapToken, context.url, bootstrapTimestamp)) {
          return;
        }
        currentWidget = response.widget || null;
        lastWidgetState = currentWidget ? "ready" : "sync_required";
        if (currentWidget) {
          const debugRequest = buildEvidenceDebugRequest(context, currentWidget);
          if (debugRequest) {
            try {
              const debugResponse = await sendMessage(debugRequest);
              currentEvidenceDebug = debugResponse?.debug || null;
            } catch (debugError) {
              currentEvidenceDebug = {
                unavailable: true,
                message: normalizeRuntimeErrorMessage(debugError?.message || debugError)
              };
            }
          }
        }
        debugLog("[Sellico] currentWidget set to:", currentWidget ? "widget object" : "null");
      } catch (error) {
        const normalizedError = normalizeRuntimeErrorMessage(error?.message || error);
        if (widgetRequest?.wbCampaignId && isCampaignNotSyncedError(normalizedError)) {
          currentWidget = null;
          lastWidgetState = "campaign_sync_required";
          lastWidgetError = null;
          console.info("[Sellico] Campaign exists in WB but is not synced in Sellico:", widgetRequest.wbCampaignId);
          await evidenceSummaryPromise;
          renderPanel(context, currentWidget);
          return;
        }
        if (widgetRequest?.wbProductId && isProductNotSyncedError(normalizedError)) {
          currentWidget = null;
          lastWidgetState = "product_sync_required";
          lastWidgetError = null;
          console.info("[Sellico] Product exists in WB but is not synced in Sellico:", widgetRequest.wbProductId);
          await evidenceSummaryPromise;
          renderPanel(context, currentWidget);
          return;
        }
        console.error("[Sellico] Widget fetch error:", error);
        currentWidget = null;
        lastWidgetState = "error";
        lastWidgetError = normalizedError;
      }
    } else {
      lastWidgetState = hasWidgetContext(context) ? "sync_required" : "unsupported_context";
    }
    await evidenceSummaryPromise;
    renderPanel(context, currentWidget);
  }

  function buildBidSnapshot(context, manual = false) {
    const metrics = detectVisibleBidMetrics();
    if (metrics.visibleBid === null && metrics.recommendedBid === null && metrics.competitiveBid === null) {
      return null;
    }
    const query = hasNonEmptyText(context.query)
      ? context.query
      : liveCaptureState.queryCandidates.length === 1
        ? liveCaptureState.queryCandidates[0]
        : "";
    if (!hasNonEmptyText(query)) {
      return null;
    }
    return {
      query: query || undefined,
      region: context.region || undefined,
      visible_bid: metrics.visibleBid ?? undefined,
      recommended_bid: metrics.recommendedBid ?? undefined,
      competitive_bid: metrics.competitiveBid ?? undefined,
      leadership_bid: metrics.leadershipBid ?? undefined,
      cpm_min: metrics.cpmMin ?? undefined,
      confidence: metrics.confidence || undefined,
      metadata: {
        page_type: backendPageTypeFromContext(context),
        wb_campaign_id: context.wb_campaign_id,
        wb_product_id: context.wb_product_id,
        wb_seller_cabinet_id: context.wb_seller_cabinet_id,
        manual,
        page_subtype: context.page_subtype
      }
    };
  }

  function buildPositionSnapshot(context, manual = false) {
    const metrics = detectVisiblePositionMetrics(context);
    if (!metrics.visiblePosition || !hasNonEmptyText(context.query) || !hasNonEmptyText(context.region) || !context.wb_product_id) {
      return null;
    }
    return {
      query: context.query,
      region: context.region,
      visible_position: metrics.visiblePosition,
      visible_page: metrics.visiblePage ?? undefined,
      page_subtype: metrics.pageSubtype || undefined,
      confidence: metrics.confidence || undefined,
      metadata: {
        page_type: backendPageTypeFromContext(context),
        wb_campaign_id: context.wb_campaign_id,
        wb_product_id: context.wb_product_id,
        wb_seller_cabinet_id: context.wb_seller_cabinet_id,
        manual,
        page_subtype: metrics.pageSubtype || context.page_subtype
      }
    };
  }

  async function persistVisibleBid(context, manual = false) {
    const item = buildBidSnapshot(context, manual);
    if (!item) {
      return false;
    }
    await sendMessage({
      type: "extension:create-bid-snapshots",
      items: [item]
    });
    return true;
  }

  async function persistVisiblePosition(context, manual = false) {
    const item = buildPositionSnapshot(context, manual);
    if (!item) {
      return false;
    }
    await sendMessage({
      type: "extension:create-position-snapshots",
      items: [item]
    });
    return true;
  }

  function queueVisibleBid(context, manual = false) {
    const item = buildBidSnapshot(context, manual);
    if (!item) {
      return false;
    }
    const key = [
      item.query || "",
      item.region || "",
      item.visible_bid || "",
      item.recommended_bid || "",
      item.competitive_bid || "",
      item.metadata?.wb_campaign_id || "",
      item.metadata?.wb_product_id || ""
    ].join("|");
    const queued = queueItem(bidQueue, key, item);
    if (queued) {
      scheduleFlush("bid");
    }
    return queued;
  }

  function queueVisiblePosition(context, manual = false) {
    const item = buildPositionSnapshot(context, manual);
    if (!item) {
      return false;
    }
    const key = [
      item.query || "",
      item.region || "",
      item.visible_position || "",
      item.visible_page || "",
      item.page_subtype || "",
      item.metadata?.wb_campaign_id || "",
      item.metadata?.wb_product_id || ""
    ].join("|");
    const queued = queueItem(positionQueue, key, item);
    if (queued) {
      scheduleFlush("position");
    }
    return queued;
  }

  function inferTableRole(context, rowText) {
    const subtype = String(context?.page_subtype || "").toLowerCase();
    const pageType = String(context?.page_type || "").toLowerCase();
    const text = String(rowText || "").toLowerCase();
    if (subtype.includes("auction") || text.includes("ставк") || text.includes("cpm") || text.includes("бид")) {
      return "bids";
    }
    if (pageType === "query" || text.includes("кластер") || text.includes("запрос")) {
      return "queries";
    }
    if (pageType === "product" || text.includes("артикул") || text.includes("nm")) {
      return "products";
    }
    if (pageType === "campaign" || subtype.includes("promotion") || text.includes("кампан")) {
      return "campaigns";
    }
    if (text.includes("расход") || text.includes("заказ") || text.includes("клик") || text.includes("показ")) {
      return "stats";
    }
    return "unknown";
  }

  function visibleRowCells(row) {
    const cellNodes = Array.from(row.querySelectorAll("td, th, [role='cell'], [role='gridcell']"));
    return cellNodes
      .map((cell, index) => ({
        index,
        text: getText(cell).slice(0, 240)
      }))
      .filter((cell) => cell.text)
      .slice(0, 16);
  }

  function buildDOMRowSnapshots(context, manual = false) {
    const pageType = backendPageTypeFromContext(context);
    if (!BACKEND_PAGE_TYPES.has(pageType)) {
      return [];
    }
    return rowCandidates().slice(0, 25).map((row, index) => {
      const visibleText = getText(row).replace(/\s+/g, " ").trim().slice(0, 1200);
      if (!visibleText || visibleText.length < 3) {
        return null;
      }
      const cells = visibleRowCells(row);
      const campaignID = extractCampaignIDFromElementMetadata(row) || context.wb_campaign_id || undefined;
      const links = Array.from(row.querySelectorAll("a[href]"))
        .map((link) => link.getAttribute("href") || link.href || "")
        .filter(Boolean)
        .slice(0, 5);
      const rowKey = [
        campaignID || "",
        context.wb_product_id || "",
        context.query || "",
        visibleText.slice(0, 180)
      ].join("|").slice(0, 300);
      return {
        page_type: pageType,
        table_role: inferTableRole(context, visibleText),
        row_key: rowKey,
        query: context.query || undefined,
        region: context.region || undefined,
        visible_text: visibleText,
        cells: cells.length ? cells : undefined,
        confidence: manual ? 0.85 : 0.65,
        metadata: {
          page_subtype: context.page_subtype,
          wb_campaign_id: campaignID,
          wb_product_id: context.wb_product_id,
          wb_seller_cabinet_id: context.wb_seller_cabinet_id,
          row_index: index,
          links,
          manual
        }
      };
    }).filter(Boolean);
  }

  function queueDOMRowSnapshots(context, manual = false) {
    const items = buildDOMRowSnapshots(context, manual);
    let queuedAny = false;
    items.forEach((item) => {
      const key = [
        item.page_type,
        item.table_role,
        item.row_key,
        item.query || "",
        item.region || ""
      ].join("|");
      queuedAny = queueItem(domRowQueue, key, item) || queuedAny;
    });
    if (queuedAny) {
      liveCaptureState = {
        ...liveCaptureState,
        domRowCount: liveCaptureState.domRowCount + items.length
      };
      scheduleFlush("domRows");
    }
    return queuedAny;
  }

  function queueUISignals(context) {
    const items = detectInlineSignals(context);
    let queuedAny = false;
    items.forEach((item) => {
      const key = [
        item.signal_type,
        item.title,
        item.message,
        item.query || "",
        item.region || "",
        item.metadata?.wb_campaign_id || "",
        item.metadata?.wb_product_id || ""
      ].join("|");
      queuedAny = queueItem(uiSignalQueue, key, item) || queuedAny;
    });
    if (queuedAny) {
      scheduleFlush("uiSignals");
    }
    return queuedAny;
  }

  async function runAutoCapturePass(bootstrapToken, context, delayMs, bootstrapTimestamp) {
    if (delayMs > 0) {
      await new Promise((resolve) => {
        const timeoutId = window.setTimeout(resolve, delayMs);
        captureTimeouts.push(timeoutId);
      });
    }

    if (!sameContext(bootstrapToken, context.url, bootstrapTimestamp)) {
      return;
    }

    let hasQueuedSignals = false;
    try {
      hasQueuedSignals = queueUISignals(context) || hasQueuedSignals;
      hasQueuedSignals = queueDOMRowSnapshots(context, false) || hasQueuedSignals;
      hasQueuedSignals = queueVisibleBid(context, false) || hasQueuedSignals;
      hasQueuedSignals = queueVisiblePosition(context, false) || hasQueuedSignals;
    } catch (_error) {
      // Best-effort live capture should not break the main panel flow.
    }

    if (hasQueuedSignals) {
      scheduleWidgetRefresh(`auto-capture-${delayMs}`);
    }
  }

  function clearCaptureTimeouts() {
    captureTimeouts.forEach((timeoutId) => clearTimeout(timeoutId));
    captureTimeouts = [];
  }

  function clearInlineBadges() {
    document.querySelectorAll(`[${INLINE_BADGE_ATTR}]`).forEach((node) => node.remove());
  }

  function rowCandidates() {
    const selectors = ROW_SELECTORS.join(",");
    return Array.from(document.querySelectorAll(selectors)).filter((node) => {
      if (!(node instanceof HTMLElement)) {
        return false;
      }
      if (node.closest(`#${PANEL_ID}`)) {
        return false;
      }
      const text = getText(node);
      return text.length > 0 && text.length < 500;
    });
  }

  function extractCampaignIDFromElementMetadata(element) {
    if (!(element instanceof HTMLElement)) {
      return null;
    }
    for (const attr of Array.from(element.attributes || [])) {
      const attrName = String(attr.name || "").toLowerCase();
      if (!/(campaign|advert)/.test(attrName) || /(nm|product|sku|subject)/.test(attrName)) {
        continue;
      }
      const value = parsePositiveInt(attr.value);
      if (value) {
        return value;
      }
    }
    return null;
  }

  function detectVisibleWBCampaignID() {
    if (!isPromotionPage()) {
      return null;
    }
    for (const row of rowCandidates()) {
      const rowID = extractCampaignIDFromElementMetadata(row);
      if (rowID) {
        return rowID;
      }
      const childrenWithAttrs = Array.from(row.querySelectorAll("[data-campaign-id], [data-advert-id], [data-advertid], [data-wb-campaign-id]"));
      for (const child of childrenWithAttrs) {
        const childID = extractCampaignIDFromElementMetadata(child);
        if (childID) {
          return childID;
        }
      }
      const links = Array.from(row.querySelectorAll("a[href]"));
      for (const link of links) {
        const hrefID = extractWBCampaignIDFromUrl(link.getAttribute("href") || link.href || "");
        if (hrefID) {
          return hrefID;
        }
      }
    }
    return null;
  }

  function findAnchorByText(matchText) {
    if (!matchText) {
      return null;
    }
    const normalized = matchText.toLowerCase();
    for (const row of rowCandidates()) {
      if (getText(row).toLowerCase().includes(normalized)) {
        return row;
      }
    }
    return null;
  }

  function primaryHeadingAnchor() {
    return firstMatch(TITLE_SELECTORS);
  }

  function buildPageLevelBadge(context, widget) {
    const firstRecommendation = (widget?.recommendations || [])[0];
    if (firstRecommendation) {
      return {
        text: summarizeRecommendation(firstRecommendation),
        tone: recommendationTone(firstRecommendation)
      };
    }
    const dataStatus = widget?.data_status || widget?.dataStatus;
    if (dataStatus?.confirmed_in_cabinet) {
      return { text: "WB live подтверждён", tone: "success" };
    }
    if (dataStatus?.freshness_state === "stale") {
      return { text: "данные stale", tone: "warning" };
    }
    if (context.page_type === "query" && context.query) {
      return { text: "контекст запроса распознан", tone: "info" };
    }
    return null;
  }

  function buildCampaignRowBadges(widget) {
    const phrases = (widget?.phrases || []).slice(0, 3);
    const recommendations = (widget?.recommendations || []).slice(0, 3);
    return phrases
      .map((phrase, index) => {
        const recommendation = recommendations[index];
        return {
          matchText: phrase.keyword,
          text: recommendation ? summarizeRecommendation(recommendation) : "Sellico: проверить фразу",
          tone: recommendation ? recommendationTone(recommendation) : "info"
        };
      })
      .filter((item) => item.matchText);
  }

  function buildQueryRowBadges(context, widget) {
    const badge = buildPageLevelBadge(context, widget);
    if (!badge) {
      return [];
    }
    return [{
      matchText: context.query || widget?.phrase?.keyword,
      text: badge.text,
      tone: badge.tone
    }];
  }

  function buildProductRowBadges(context, widget) {
    const badge = buildPageLevelBadge(context, widget);
    const productTitle = widget?.product?.title || widget?.product?.name;
    if (!badge || !productTitle) {
      return [];
    }
    return [{
      matchText: productTitle,
      text: badge.text,
      tone: badge.tone
    }];
  }

  function attachInlineBadge(target, descriptor) {
    if (!(target instanceof HTMLElement) || !descriptor?.text) {
      return;
    }
    const badge = document.createElement("span");
    badge.setAttribute(INLINE_BADGE_ATTR, "true");
    badge.className = `sellico-inline-badge sellico-inline-badge--${descriptor.tone || "info"}`;
    badge.textContent = descriptor.text;
    target.appendChild(badge);
  }

  function renderInlineBadges(context, widget) {
    clearInlineBadges();
    if (!context) {
      return;
    }

    const heading = primaryHeadingAnchor();
    const pageBadge = buildPageLevelBadge(context, widget);
    if (heading && pageBadge) {
      attachInlineBadge(heading, pageBadge);
    }

    let descriptors = [];
    if (context.page_type === "campaign" || context.page_type === "auction") {
      descriptors = buildCampaignRowBadges(widget);
    } else if (context.page_type === "query") {
      descriptors = buildQueryRowBadges(context, widget);
    } else if (context.page_type === "product") {
      descriptors = buildProductRowBadges(context, widget);
    }

    descriptors.slice(0, 3).forEach((descriptor) => {
      const anchor = findAnchorByText(descriptor.matchText);
      if (!anchor || anchor === heading) {
        return;
      }
      attachInlineBadge(anchor, descriptor);
    });
  }

  function disconnectBadgeObserver() {
    if (badgeObserver) {
      badgeObserver.disconnect();
      badgeObserver = null;
    }
  }

  function installBadgeObserver() {
    disconnectBadgeObserver();
    if (!document.body) {
      return;
    }
    let badgeDebounceTimer = null;
    badgeObserver = new MutationObserver(() => {
      if (!currentWidget) {
        return;
      }
      // Debounce: coalesce rapid DOM mutations into a single badge refresh
      if (badgeDebounceTimer) {
        clearTimeout(badgeDebounceTimer);
      }
      badgeDebounceTimer = setTimeout(() => {
        badgeDebounceTimer = null;
        scheduleBadgeRefresh();
      }, 300);
    });
    badgeObserver.observe(document.body, {
      childList: true,
      subtree: true
    });
  }

  async function bootstrap(reason = "auto") {
    const bootstrapToken = ++currentBootstrapToken;
    const bootstrapTimestamp = Date.now();
    const context = detectContext();
    currentContext = context;
    lastBootstrappedUrl = context.url;
    lastBootstrapAt = bootstrapTimestamp;

    disconnectBadgeObserver();
    renderPanel(context, currentWidget);

    await loadConfig();
    await sendMessage({ type: "extension:start-session" });
    await sendMessage({
      type: "extension:create-page-context",
      payload: buildPageContextPayload(context)
    });

    if (!sameContext(bootstrapToken, context.url, bootstrapTimestamp)) {
      return;
    }

    await refreshWidget(context, bootstrapToken, bootstrapTimestamp);

    clearCaptureTimeouts();
    if (currentConfig?.autoCapture !== false) {
      RETRY_CAPTURE_DELAYS_MS.forEach((delayMs) => {
        runAutoCapturePass(bootstrapToken, context, delayMs, bootstrapTimestamp);
      });
      scheduleAutoProbe(`bootstrap-${reason}`, null, 1200);
    }

    installBadgeObserver();
  }

  function shouldRefreshOnFocus() {
    return Date.now() - lastVisibleRefreshAt > FOCUS_REFRESH_INTERVAL_MS;
  }

  function scheduleBootstrap(reason = "auto", delay = FLUSH_DELAYS_MS.bootstrap, force = false) {
    if (!force && reason !== "focus" && window.location.href === lastBootstrappedUrl && Date.now() - lastBootstrapAt < 2000) {
      return;
    }
    if (bootstrapTimer) {
      clearTimeout(bootstrapTimer);
    }
    // Clear pending capture timeouts from previous bootstrap immediately on navigation
    clearCaptureTimeouts();
    bootstrapTimer = window.setTimeout(() => {
      bootstrapTimer = null;
      bootstrap(reason).catch((error) => {
        lastWidgetError = logHandledError("Bootstrap failed", reason, error);
        if (currentContext) {
          renderPanel(currentContext, null);
        }
      });
    }, delay);
  }

  function installNavigationHooks() {
    if (window.__sellicoNavigationHooksInstalled) {
      return;
    }
    window.__sellicoNavigationHooksInstalled = true;

    const originalPushState = history.pushState;
    const originalReplaceState = history.replaceState;

    history.pushState = function patchedPushState(...args) {
      const result = originalPushState.apply(this, args);
      scheduleBootstrap("push-state");
      return result;
    };

    history.replaceState = function patchedReplaceState(...args) {
      const result = originalReplaceState.apply(this, args);
      scheduleBootstrap("replace-state");
      return result;
    };

    window.addEventListener("popstate", () => {
      scheduleBootstrap("pop-state");
    });
    window.addEventListener("hashchange", () => {
      scheduleBootstrap("hash-change");
    });
    window.addEventListener("focus", () => {
      if (!shouldRefreshOnFocus()) {
        return;
      }
      lastVisibleRefreshAt = Date.now();
      scheduleBootstrap("focus", 0, true);
    });
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState !== "visible" || !shouldRefreshOnFocus()) {
        return;
      }
      lastVisibleRefreshAt = Date.now();
      scheduleBootstrap("visible", 0, true);
    });
  }

  window.addEventListener("message", async (event) => {
    if (event.source !== window || event.data?.source !== "sellico-page-bridge" || event.data?.nonce !== pageBridgeNonce) {
      return;
    }

    const config = currentConfig || (await loadConfig().catch(() => null));
    if (config?.autoCapture === false) {
      return;
    }

    const payload = normalizeNetworkCapture(event.data.detail);
    if (!payload) {
      return;
    }

    const safeResponseKey = (() => {
      try {
        const raw = JSON.stringify(payload.payload?.response || null);
        if (!raw) {
          return "";
        }
        return raw.length > 2000 ? `${raw.slice(0, 2000)}…` : raw;
      } catch (_err) {
        return "[unserializable]";
      }
    })();

    const key = [
      payload.endpoint_key,
      payload.page_type || "",
      payload.query || "",
      payload.region || "",
      payload.payload?.url || "",
      payload.payload?.status || "",
      safeResponseKey
    ].join("|");
    queueItem(networkCaptureQueue, key, payload);
    recordLiveCapture(payload);
    scheduleFlush("network");
    scheduleAutoProbe(`network-${payload.endpoint_key}`, payload, 650);

    const derivedQueued = queueNetworkDerivedPayloads(payload, currentContext || detectContext());
    if (derivedQueued) {
      scheduleWidgetRefresh(`network-derived-${payload.endpoint_key}`, 350);
    }
  });

  installPageBridge();
  installNavigationHooks();
  installBadgeObserver();
  bootstrap("init").catch((error) => {
    lastWidgetError = logHandledError("Bootstrap failed", "init", error);
    if (currentContext) {
      renderPanel(currentContext, null);
    }
  });
})();

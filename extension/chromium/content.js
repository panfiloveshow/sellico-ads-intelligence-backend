(function initSellicoExtension() {
  const PANEL_ID = "sellico-extension-panel";
  const ROOT_ATTR = "data-sellico-extension-root";
  const INLINE_BADGE_ATTR = "data-sellico-inline-badge";
  const NETWORK_ENDPOINT_ALLOWLIST = new Map([
    ["/adv/v1/promotion/adverts", "wb.adverts"],
    ["/adv/v0/advert", "wb.adverts"],
    ["/adv/v3/fullstats", "wb.campaign.stats"],
    ["/adv/v0/stats", "wb.campaign.stats"],
    ["/adv/v0/normquery", "wb.query.clusters"],
    ["/adv/v2/recommended-bids", "wb.bid.estimate"],
    ["/adv/v1/auction/adverts", "wb.ui.auction"],
    ["/search", "wb.ui.search"]
  ]);
  const RETRY_CAPTURE_DELAYS_MS = [0, 1500, 4000];
  const FLUSH_DELAYS_MS = {
    bootstrap: 350,
    widget: 900,
    network: 900,
    uiSignals: 700,
    bid: 500,
    position: 500,
    badges: 450
  };
  const FOCUS_REFRESH_INTERVAL_MS = 60 * 1000;

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
  let networkCaptureQueue = new Map();
  let uiSignalQueue = new Map();
  let bidQueue = new Map();
  let positionQueue = new Map();
  let flushTimers = {
    network: null,
    uiSignals: null,
    bid: null,
    position: null
  };

  function sendMessage(message) {
    return new Promise((resolve, reject) => {
      chrome.runtime.sendMessage(message, (response) => {
        const runtimeError = chrome.runtime.lastError;
        if (runtimeError) {
          reject(new Error(runtimeError.message));
          return;
        }
        if (!response?.ok) {
          reject(new Error(response?.error || "Sellico extension request failed"));
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

  function detectPageSubtype() {
    const href = window.location.href.toLowerCase();
    const bodyText = document.body?.innerText?.slice(0, 4000).toLowerCase() || "";
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
    const query = detectPrimaryQueryText(pageType);
    const region =
      url.searchParams.get("region") ||
      getText(firstMatch(REGION_SELECTORS)) ||
      "";
    const campaignHintFromParam = parseIntParam("campaignId", "campaign_id", "advertId", "advert_id");
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
    const campaignHint = campaignHintFromParam || campaignHintFromPath;
    
    // DEBUG LOG
    console.log("[Sellico] detectContext:", {
      url: window.location.href,
      pageType,
      campaignHintFromParam,
      campaignHintFromPath,
      campaignHint,
      pathname: url.pathname
    });
    const wbProductId = parseIntParam("nm", "nmId", "nmid", "productId", "product_id");
    const pageTitle = getText(firstMatch(TITLE_SELECTORS));
    const sellerCabinetId = parseIntParam("cabinetId", "supplierId", "supplier_id");
    const pageSubtype = detectPageSubtype();

    return {
      url: window.location.href,
      page_type: pageType,
      seller_cabinet_id: sellerCabinetId,
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
    return {
      url: context.url,
      page_type: context.page_type,
      seller_cabinet_id: context.seller_cabinet_id,
      query: context.page_type === "query" ? context.query : "",
      region: context.region,
      active_filters: context.active_filters,
      metadata: {
        ...context.metadata,
        wb_campaign_id: context.wb_campaign_id,
        wb_product_id: context.wb_product_id,
        page_subtype: context.page_subtype
      }
    };
  }

  function buildWidgetRequest(context) {
    if (context.page_type === "query" && context.query) {
      console.log("[Sellico] Widget request: query=", context.query);
      return { type: "extension:fetch-widget", query: context.query };
    }
    if (context.wb_product_id) {
      console.log("[Sellico] Widget request: wb_product_id=", context.wb_product_id);
      return { type: "extension:fetch-widget", wbProductId: context.wb_product_id };
    }
    if (context.wb_campaign_id) {
      console.log("[Sellico] Widget request: wb_campaign_id=", context.wb_campaign_id);
      return { type: "extension:fetch-widget", wbCampaignId: context.wb_campaign_id };
    }
    console.log("[Sellico] Widget request: null (no recognized context)");
    return null;
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
        seller_cabinet_id: context.seller_cabinet_id,
        query: context.query,
        region: context.region,
        signal_type: signalType,
        severity,
        title: text.slice(0, 90),
        message: text,
        confidence: severity === "high" ? 0.9 : 0.75,
        metadata: {
          page_type: context.page_type,
          wb_campaign_id: context.wb_campaign_id,
          wb_product_id: context.wb_product_id,
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
    const context = currentContext || detectContext();
    return {
      seller_cabinet_id: context.seller_cabinet_id,
      page_type: context.page_type,
      query: context.query,
      region: context.region,
      endpoint_key: endpointKey,
      payload: {
        url: detail.url,
        method: detail.method || "GET",
        status: detail.status || 0,
        request: detail.request || null,
        response: detail.response || null,
        wb_campaign_id: context.wb_campaign_id,
        wb_product_id: context.wb_product_id,
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
      if (!query && !wbCampaignId) {
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
        seller_cabinet_id: fallbackContext.seller_cabinet_id,
        query: query || undefined,
        region: extractCandidateRegion(candidate, fallbackContext) || undefined,
        visible_bid: metrics.visibleBid ?? undefined,
        recommended_bid: metrics.recommendedBid ?? undefined,
        competitive_bid: metrics.competitiveBid ?? undefined,
        leadership_bid: metrics.leadershipBid ?? undefined,
        cpm_min: metrics.cpmMin ?? undefined,
        confidence: 0.82,
        metadata: {
          page_type: fallbackContext.page_type,
          wb_campaign_id: wbCampaignId,
          wb_product_id: wbProductId,
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
        seller_cabinet_id: fallbackContext.seller_cabinet_id,
        query,
        region,
        visible_position: metrics.visiblePosition,
        visible_page: metrics.visiblePage ?? undefined,
        page_subtype: metrics.pageSubtype || fallbackContext.page_subtype || undefined,
        confidence: 0.8,
        metadata: {
          page_type: fallbackContext.page_type,
          wb_campaign_id: wbCampaignId,
          wb_product_id: wbProductId,
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
        seller_cabinet_id: fallbackContext.seller_cabinet_id,
        query: query || undefined,
        region: region || undefined,
        signal_type: signalType,
        severity,
        title,
        message: rawText,
        confidence: 0.76,
        metadata: {
          page_type: fallbackContext.page_type,
          wb_campaign_id: wbCampaignId,
          wb_product_id: wbProductId,
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
    if (bidItems.length) {
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
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="28" height="28">
            <defs>
              <linearGradient id="sellicoGrad" x1="0%" y1="0%" x2="100%" y2="100%">
                <stop offset="0%" style="stop-color:#f59e0b;stop-opacity:1" />
                <stop offset="100%" style="stop-color:#d97706;stop-opacity:1" />
              </linearGradient>
            </defs>
            <circle cx="50" cy="50" r="48" fill="url(#sellicoGrad)"/>
            <text x="50" y="68" font-family="Arial, sans-serif" font-size="50" font-weight="bold" fill="white" text-anchor="middle">S</text>
          </svg>
        </div>
        <div class="sellico-panel__header-content">
          <p class="sellico-panel__eyebrow">Sellico Live</p>
          <h3 class="sellico-panel__title">Загрузка контекста...</h3>
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
      return `<div class="sellico-panel__empty">${emptyText}</div>`;
    }
    return `
      <div class="sellico-panel__subsection">
        <h4>${title}</h4>
        <ul>${safeItems.map((item) => `<li>${item}</li>`).join("")}</ul>
      </div>
    `;
  }

  function humanizeSource(dataStatus) {
    if (!dataStatus) {
      return "backend estimate";
    }
    if (dataStatus.confirmed_in_cabinet) {
      return "подтверждено в кабинете";
    }
    return dataStatus.source || "backend estimate";
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

    if (!widget) {
      const statusDiv = document.createElement("div");
      statusDiv.className = "sellico-panel__status-grid";
      statusDiv.innerHTML = `<div><span>Режим</span><strong></strong></div><div><span>Данные</span><strong></strong></div>`;
      statusDiv.querySelectorAll("strong")[0].textContent = context.page_type;
      statusDiv.querySelectorAll("strong")[1].textContent = lastWidgetError ? "ошибка" : "не подключены";
      status.innerHTML = "";
      status.appendChild(statusDiv);

      if (lastWidgetError) {
        summary.innerHTML = `
          <div class="sellico-panel__empty sellico-panel__empty--notice">
            <strong>Ошибка загрузки данных</strong><br><br>
            <span style="color:#94a3b8;font-size:12px;">${escapeHtml(lastWidgetError)}</span><br><br>
            Нажми «Повторить» для повторной попытки.
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
          </div>
        `;
      }
      signals.innerHTML = "";
      const links = [];
      links.push(`<a href="#" data-sellico-action="refresh">${lastWidgetError ? "Повторить" : "Обновить контекст"}</a>`);
      actions.innerHTML = renderList("Действия", links, "");
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
      <div><span>Coverage</span><strong></strong></div>
    `;
    const vals = statusGrid.querySelectorAll("strong");
    vals[0].textContent = context.page_type;
    vals[1].textContent = humanizeSource(dataStatus);
    vals[2].textContent = dataStatus?.freshness_state || "unknown";
    vals[3].textContent = dataStatus?.coverage || "partial";
    status.innerHTML = "";
    status.appendChild(statusGrid);

    const recommendationItems = (widget?.recommendations || []).slice(0, 3).map((item) => {
      const action = summarizeRecommendation(item);
      return `<strong>${item.title || item.type}</strong><span>${action}</span>`;
    });
    const signalItems = []
      .concat((widget?.ui_signals || []).slice(0, 3).map((item) => `<strong>${item.title}</strong><span>${item.message}</span>`))
      .concat((widget?.live_positions || []).slice(0, 2).map((item) => `<strong>Позиция ${item.visible_position || "—"}</strong><span>${item.query || context.query || ""}</span>`))
      .concat((widget?.live_bids || []).slice(0, 2).map((item) => `<strong>Bid ${item.visible_bid || "—"}</strong><span>${item.query || context.query || ""}</span>`))
      .concat(widget?.live_bid_snapshot ? [`<strong>Bid ${widget.live_bid_snapshot.visible_bid || "—"}</strong><span>${context.query || ""}</span>`] : []);

    summary.innerHTML = renderList("Что важно сейчас", recommendationItems, "Sellico не нашёл срочных действий на этой странице.");
    signals.innerHTML = renderList("Live сигналы", signalItems, "На странице пока нет подтверждённых live сигналов.");

    const links = [];
    if (context.wb_product_id) {
      links.push(`<a href="#" data-sellico-action="save-position">Сохранить snapshot позиции</a>`);
    }
    if (context.query) {
      links.push(`<a href="#" data-sellico-action="save-bid">Сохранить snapshot ставки</a>`);
    }
    links.push(`<a href="#" data-sellico-action="refresh">Обновить контекст</a>`);
    actions.innerHTML = renderList("Действия", links, "Нет быстрых действий.");
    bindPanelActions(panel, context);
    renderInlineBadges(context, widget);
  }

  function bindPanelActions(panel, context) {
    panel.querySelectorAll("[data-sellico-action]").forEach((link) => {
      link.addEventListener("click", async (event) => {
        event.preventDefault();
        const action = link.getAttribute("data-sellico-action");
        if (action === "refresh") {
          scheduleBootstrap("manual-refresh", 0, true);
          return;
        }
        if (action === "save-position") {
          await persistVisiblePosition(context, true);
          scheduleWidgetRefresh("manual-position", 150);
          return;
        }
        if (action === "save-bid") {
          await persistVisibleBid(context, true);
          scheduleWidgetRefresh("manual-bid", 150);
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
        : kind === "uiSignals"
          ? uiSignalQueue
          : kind === "bid"
            ? bidQueue
            : positionQueue;

    if (!queue.size) {
      return false;
    }

    const items = Array.from(queue.values());
    queue.clear();

    const messageType =
      kind === "network"
        ? "extension:create-network-captures"
        : kind === "uiSignals"
          ? "extension:create-ui-signals"
          : kind === "bid"
            ? "extension:create-bid-snapshots"
            : "extension:create-position-snapshots";

    try {
      await sendMessage({
        type: messageType,
        items
      });
      scheduleWidgetRefresh(`flush-${kind}`);
      return true;
    } catch (_error) {
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
    if (widgetRequest) {
      try {
        console.log("[Sellico] Sending widget request:", widgetRequest);
        const response = await sendMessage(widgetRequest);
        console.log("[Sellico] Widget response:", response);
        if (!sameContext(bootstrapToken, context.url, bootstrapTimestamp)) {
          return;
        }
        currentWidget = response.widget || null;
        console.log("[Sellico] currentWidget set to:", currentWidget ? "widget object" : "null");
      } catch (error) {
        console.error("[Sellico] Widget fetch error:", error);
        currentWidget = null;
        lastWidgetError = error?.message || String(error);
      }
    }
    renderPanel(context, currentWidget);
  }

  function buildBidSnapshot(context, manual = false) {
    const metrics = detectVisibleBidMetrics();
    if (metrics.visibleBid === null && metrics.recommendedBid === null && metrics.competitiveBid === null) {
      return null;
    }
    if (!context.query && !context.wb_campaign_id) {
      return null;
    }
    return {
      seller_cabinet_id: context.seller_cabinet_id,
      query: context.query || undefined,
      region: context.region || undefined,
      visible_bid: metrics.visibleBid ?? undefined,
      recommended_bid: metrics.recommendedBid ?? undefined,
      competitive_bid: metrics.competitiveBid ?? undefined,
      leadership_bid: metrics.leadershipBid ?? undefined,
      cpm_min: metrics.cpmMin ?? undefined,
      confidence: metrics.confidence || undefined,
      metadata: {
        page_type: context.page_type,
        wb_campaign_id: context.wb_campaign_id,
        wb_product_id: context.wb_product_id,
        manual,
        page_subtype: context.page_subtype
      }
    };
  }

  function buildPositionSnapshot(context, manual = false) {
    const metrics = detectVisiblePositionMetrics(context);
    if (!metrics.visiblePosition || !context.query) {
      return null;
    }
    return {
      seller_cabinet_id: context.seller_cabinet_id,
      query: context.query,
      region: context.region,
      visible_position: metrics.visiblePosition,
      visible_page: metrics.visiblePage ?? undefined,
      page_subtype: metrics.pageSubtype || undefined,
      confidence: metrics.confidence || undefined,
      metadata: {
        page_type: context.page_type,
        wb_campaign_id: context.wb_campaign_id,
        wb_product_id: context.wb_product_id,
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
      bootstrap(reason).catch(() => {
        // Keep WB UI unaffected even if Sellico capture fails.
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
    if (event.source !== window || event.data?.source !== "sellico-page-bridge") {
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
    scheduleFlush("network");

    const derivedQueued = queueNetworkDerivedPayloads(payload, currentContext || detectContext());
    if (derivedQueued) {
      scheduleWidgetRefresh(`network-derived-${payload.endpoint_key}`, 350);
    }
  });

  installPageBridge();
  installNavigationHooks();
  installBadgeObserver();
  bootstrap("init").catch(() => {
    // Never block the underlying WB cabinet.
  });
})();

import { useQuery } from "@tanstack/react-query";

// All workspace-scoped queries need the X-Workspace-ID header. We pull it
// from a workspace context (TODO: wire WorkspaceProvider in a follow-up;
// for the MVP the page reads from URL param or a fixed value).
//
// Date range params are required by the backend — pass ISO yyyy-mm-dd.

export interface AdsOverviewParams {
  workspaceId: string;
  dateFrom: string;
  dateTo: string;
  sellerCabinetId?: string;
  enabled?: boolean;
}

/**
 * Fetches /api/v1/ads/overview — workspace KPI dashboard payload.
 * The result shape is the Go `domain.AdsOverview`; we type it loosely as
 * `unknown` for now and narrow at usage sites because the OpenAPI schema
 * for /ads/* is intentionally `type: object` (Phase F.2 looseness — the
 * Go domain types are the source of truth).
 */
export function useAdsOverview(params: AdsOverviewParams) {
  return useQuery({
    queryKey: ["ads-overview", params.workspaceId, params.dateFrom, params.dateTo, params.sellerCabinetId ?? null],
    enabled: params.enabled ?? Boolean(params.workspaceId),
    queryFn: async () => {
      // Hand-rolled fetch instead of api.GET because the typed openapi-fetch
      // call requires the OpenAPI to declare exact response schemas — which
      // we deliberately left loose (type: object). Once the schemas tighten
      // (Sprint 7 polish), switch to api.GET("/api/v1/ads/overview", ...).
      const search = new URLSearchParams({
        date_from: params.dateFrom,
        date_to: params.dateTo,
      });
      if (params.sellerCabinetId) search.set("seller_cabinet_id", params.sellerCabinetId);

      const res = await fetch(`/api/v1/ads/overview?${search.toString()}`, {
        headers: {
          "X-Workspace-ID": params.workspaceId,
        },
      });
      if (!res.ok) {
        throw new Error(`ads/overview ${res.status}`);
      }
      const json = (await res.json()) as { data: AdsOverviewResponse };
      return json.data;
    },
  });
}

// Hand-typed reflection of internal/domain.AdsOverview. Keep in sync with
// the Go struct until openapi-typescript starts producing rich schemas.
export interface AdsMetricsSummary {
  impressions: number;
  clicks: number;
  spend: number; // kopecks
  orders: number;
  revenue: number; // kopecks
  ctr: number; // ratio 0..1
  cpc: number; // kopecks
  cpo: number; // kopecks
  roas: number;
  drr: number;
  conversion_rate: number;
  data_mode?: string;
}

export interface AdsPeriodCompare {
  current: AdsMetricsSummary;
  previous: AdsMetricsSummary;
  trend: string;
}

export interface AttentionItem {
  type: string;
  title: string;
  description: string;
  severity: string;
  action_label?: string;
  action_path?: string;
  source_type?: string;
  source_id?: string;
}

export interface AdsOverviewTotals {
  cabinets: number;
  products: number;
  campaigns: number;
  queries: number;
  active_campaigns: number;
  attention_items: number;
}

export interface AdsEntityRef {
  id: string;
  label: string;
  wb_id?: number;
  count?: number;
  source?: string;
}

export interface ProductAdsSummary {
  id: string;
  workspace_id: string;
  seller_cabinet_id: string;
  integration_name: string;
  cabinet_name: string;
  wb_product_id: number;
  title: string;
  brand?: string;
  category?: string;
  image_url?: string;
  price?: number;
  campaigns_count: number;
  queries_count: number;
  health_status: string;
  health_reason?: string;
  primary_action?: string;
  performance: AdsMetricsSummary;
  period_compare?: AdsPeriodCompare;
  related_campaigns?: AdsEntityRef[];
  top_queries?: AdsEntityRef[];
  waste_queries?: AdsEntityRef[];
  winning_queries?: AdsEntityRef[];
}

export interface CampaignPerformanceSummary {
  id: string;
  workspace_id: string;
  seller_cabinet_id: string;
  integration_name: string;
  cabinet_name: string;
  wb_campaign_id: number;
  name: string;
  status: string;
  bid_type: string;
  payment_type: string;
  daily_budget?: number;
  health_status: string;
  health_reason?: string;
  primary_action?: string;
  performance: AdsMetricsSummary;
  period_compare?: AdsPeriodCompare;
  related_products?: AdsEntityRef[];
  top_queries?: AdsEntityRef[];
  waste_queries?: AdsEntityRef[];
  winning_queries?: AdsEntityRef[];
}

export interface QueryPerformanceSummary {
  id: string;
  workspace_id: string;
  campaign_id: string;
  seller_cabinet_id: string;
  integration_name: string;
  cabinet_name: string;
  campaign_name: string;
  wb_cluster_id: number;
  keyword: string;
  current_bid?: number;
  cluster_size?: number;
  source: string;
  signal_category: string;
  health_status: string;
  health_reason?: string;
  primary_action?: string;
  performance: AdsMetricsSummary;
  period_compare?: AdsPeriodCompare;
  related_products?: AdsEntityRef[];
}

export interface AdsOverviewResponse {
  performance_compare?: AdsPeriodCompare;
  cabinets: unknown[];
  attention: AttentionItem[];
  top_products: ProductAdsSummary[];
  top_campaigns: CampaignPerformanceSummary[];
  top_queries: QueryPerformanceSummary[];
  totals: AdsOverviewTotals;
}

// --- Single-entity detail queries ---

interface DetailParams {
  workspaceId: string;
  id: string;
  dateFrom: string;
  dateTo: string;
  enabled?: boolean;
}

const detailFetcher = async <T>(
  endpoint: string,
  workspaceId: string,
  dateFrom: string,
  dateTo: string,
): Promise<T> => {
  const search = new URLSearchParams({ date_from: dateFrom, date_to: dateTo });
  const res = await fetch(`${endpoint}?${search.toString()}`, {
    headers: { "X-Workspace-ID": workspaceId },
  });
  if (!res.ok) throw new Error(`${endpoint} ${res.status}`);
  const json = (await res.json()) as { data: T };
  return json.data;
};

export function useAdsProduct(p: DetailParams) {
  return useQuery({
    queryKey: ["ads-product", p.workspaceId, p.id, p.dateFrom, p.dateTo],
    enabled: p.enabled ?? Boolean(p.workspaceId && p.id),
    queryFn: () =>
      detailFetcher<ProductAdsSummary>(`/api/v1/ads/products/${p.id}`, p.workspaceId, p.dateFrom, p.dateTo),
  });
}

export function useAdsCampaign(p: DetailParams) {
  return useQuery({
    queryKey: ["ads-campaign", p.workspaceId, p.id, p.dateFrom, p.dateTo],
    enabled: p.enabled ?? Boolean(p.workspaceId && p.id),
    queryFn: () =>
      detailFetcher<CampaignPerformanceSummary>(`/api/v1/ads/campaigns/${p.id}`, p.workspaceId, p.dateFrom, p.dateTo),
  });
}

export function useAdsQuery(p: DetailParams) {
  return useQuery({
    queryKey: ["ads-query", p.workspaceId, p.id, p.dateFrom, p.dateTo],
    enabled: p.enabled ?? Boolean(p.workspaceId && p.id),
    queryFn: () =>
      detailFetcher<QueryPerformanceSummary>(`/api/v1/ads/queries/${p.id}`, p.workspaceId, p.dateFrom, p.dateTo),
  });
}

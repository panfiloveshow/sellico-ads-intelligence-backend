import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";

export interface Recommendation {
  id: string;
  workspace_id: string;
  campaign_id?: string;
  phrase_id?: string;
  product_id?: string;
  seller_cabinet_id?: string;
  title: string;
  description: string;
  type: string;
  severity: "low" | "medium" | "high" | "critical" | string;
  confidence: number;
  next_action?: string;
  status: "active" | "completed" | "dismissed" | string;
  created_at: string;
  updated_at: string;
}

interface RecommendationsListResponse {
  data: Recommendation[];
  meta?: { page: number; per_page: number; total: number };
}

interface RecommendationsParams {
  workspaceId: string;
  page?: number;
  perPage?: number;
  type?: string;
  severity?: string;
  status?: string;
  enabled?: boolean;
}

export function useRecommendations(p: RecommendationsParams) {
  return useQuery({
    queryKey: ["recommendations", p.workspaceId, p.page, p.perPage, p.type, p.severity, p.status],
    enabled: p.enabled ?? Boolean(p.workspaceId),
    queryFn: async () => {
      const search = new URLSearchParams();
      if (p.page) search.set("page", String(p.page));
      if (p.perPage) search.set("per_page", String(p.perPage));
      if (p.type) search.set("type", p.type);
      if (p.severity) search.set("severity", p.severity);
      if (p.status) search.set("status", p.status);
      const qs = search.toString();
      const res = await fetch(`/api/v1/recommendations${qs ? `?${qs}` : ""}`, {
        headers: { "X-Workspace-ID": p.workspaceId },
      });
      if (!res.ok) throw new Error(`recommendations ${res.status}`);
      return (await res.json()) as RecommendationsListResponse;
    },
  });
}

interface ActionParams {
  workspaceId: string;
  id: string;
}

/** Generic factory — recommendations have three actions with the same shape. */
function useRecAction(action: "apply" | "resolve" | "dismiss") {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (p: ActionParams) => {
      const res = await fetch(`/api/v1/recommendations/${p.id}/${action}`, {
        method: "POST",
        headers: { "X-Workspace-ID": p.workspaceId },
      });
      if (!res.ok) throw new Error(`${action} ${res.status}`);
      const body = (await res.json()) as { data: Recommendation };
      return body.data;
    },
    onSuccess: (_, vars) => {
      // Invalidate every recommendations query for this workspace; granular
      // patching by id is overkill for the list-page use-case.
      qc.invalidateQueries({ queryKey: ["recommendations", vars.workspaceId] });
      // Dashboard also surfaces attention items derived from active recs.
      qc.invalidateQueries({ queryKey: ["ads-overview", vars.workspaceId] });
    },
  });
}

/** POST /api/v1/recommendations/{id}/apply — executes the suggested change in WB. */
export const useApplyRecommendation = () => useRecAction("apply");
/** POST /api/v1/recommendations/{id}/resolve — mark as completed manually. */
export const useResolveRecommendation = () => useRecAction("resolve");
/** POST /api/v1/recommendations/{id}/dismiss — hide from list, never auto-resurface. */
export const useDismissRecommendation = () => useRecAction("dismiss");

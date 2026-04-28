import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";

// Workspace settings (thresholds + telegram + WB autopilot prefs).
// Backend exposes raw key/value JSON; we don't strongly type the inner shape
// here because Settings page renders it as a generic form-builder driven by
// the schema endpoint. For Sprint 7 polish we'll narrow the types.
export interface WorkspaceSettings {
  [key: string]: unknown;
}

interface SettingsParams {
  workspaceId: string;
}

export function useWorkspaceSettings({ workspaceId }: SettingsParams) {
  return useQuery({
    queryKey: ["workspace-settings", workspaceId],
    enabled: Boolean(workspaceId),
    queryFn: async () => {
      const res = await fetch("/api/v1/settings", {
        headers: { "X-Workspace-ID": workspaceId },
      });
      if (!res.ok) throw new Error(`settings ${res.status}`);
      const body = (await res.json()) as { data: WorkspaceSettings };
      return body.data;
    },
  });
}

export function useUpdateWorkspaceSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ workspaceId, patch }: { workspaceId: string; patch: WorkspaceSettings }) => {
      const res = await fetch("/api/v1/settings", {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          "X-Workspace-ID": workspaceId,
        },
        body: JSON.stringify(patch),
      });
      if (!res.ok) throw new Error(`settings PUT ${res.status}`);
      return (await res.json()) as { data: WorkspaceSettings };
    },
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["workspace-settings", vars.workspaceId] });
    },
  });
}

// --- Seller cabinets (Settings → Cabinets list) ---

export interface SellerCabinet {
  id: string;
  workspace_id: string;
  name: string;
  status: string;
  source: string; // "manual" | "sellico"
  last_synced_at?: string;
  created_at: string;
  updated_at: string;
}

export function useSellerCabinets({ workspaceId }: SettingsParams) {
  return useQuery({
    queryKey: ["seller-cabinets", workspaceId],
    enabled: Boolean(workspaceId),
    queryFn: async () => {
      const res = await fetch("/api/v1/seller-cabinets", {
        headers: { "X-Workspace-ID": workspaceId },
      });
      if (!res.ok) throw new Error(`seller-cabinets ${res.status}`);
      const body = (await res.json()) as { data: SellerCabinet[] };
      return body.data;
    },
  });
}

export function useTriggerSync() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ workspaceId, cabinetId }: { workspaceId: string; cabinetId: string }) => {
      const res = await fetch(`/api/v1/seller-cabinets/${cabinetId}/sync`, {
        method: "POST",
        headers: { "X-Workspace-ID": workspaceId },
      });
      if (!res.ok) throw new Error(`sync trigger ${res.status}`);
      return await res.json();
    },
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["seller-cabinets", vars.workspaceId] });
    },
  });
}

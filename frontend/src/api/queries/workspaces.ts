import { useQuery } from "@tanstack/react-query";

export interface Workspace {
  id: string;
  name: string;
  slug: string;
}

interface WorkspacesPaginated {
  data: Workspace[];
  meta?: { page: number; per_page: number; total: number };
}

/**
 * Fetches /api/v1/workspaces — the workspaces the current user is a member of.
 * Used by AppLayout to populate the workspace switcher and by pages that
 * need to default to "the user's only / first workspace" before a switcher
 * is wired up (Sprint 6 MVP).
 */
export function useWorkspaces() {
  return useQuery({
    queryKey: ["workspaces"],
    queryFn: async (): Promise<Workspace[]> => {
      const res = await fetch("/api/v1/workspaces");
      if (!res.ok) throw new Error(`workspaces ${res.status}`);
      const body = (await res.json()) as WorkspacesPaginated;
      return body.data;
    },
    staleTime: 5 * 60_000, // workspaces rarely change; aggressive cache
  });
}

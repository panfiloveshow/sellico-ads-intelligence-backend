import { Card, CardContent, Stack, Typography, Chip, Button, Box, Skeleton } from "@mui/material";
import { useNavigate } from "react-router-dom";

interface AttentionItem {
  type: string;
  title: string;
  description: string;
  severity: string; // "low" | "medium" | "high" | "critical"
  action_label?: string;
  action_path?: string;
}

interface AttentionListProps {
  items: AttentionItem[] | undefined;
  loading?: boolean;
  /** Hide trailing items beyond this — Command Center uses 6 by default. */
  limit?: number;
}

const severityColor: Record<string, "default" | "info" | "warning" | "error"> = {
  low: "default",
  medium: "info",
  high: "warning",
  critical: "error",
};

/**
 * Vertically-stacked actionable attention items. Each row renders title +
 * one-line description + a CTA button that routes via React Router (action_path
 * is a SPA path produced by the backend, e.g. "/ads-intelligence/jobs").
 *
 * Empty list intentionally renders nothing — calling page decides whether
 * to show a "You're all caught up" empty state, since that copy varies by
 * surface (dashboard vs. recommendations page).
 */
export function AttentionList({ items, loading, limit = 6 }: AttentionListProps) {
  const navigate = useNavigate();

  if (loading) {
    return (
      <Stack spacing={1}>
        {Array.from({ length: 3 }).map((_, i) => (
          <Skeleton key={i} variant="rectangular" height={80} />
        ))}
      </Stack>
    );
  }

  if (!items || items.length === 0) return null;

  const visible = items.slice(0, limit);

  return (
    <Stack spacing={1.5}>
      {visible.map((item, idx) => (
        <Card key={`${item.type}-${idx}`} variant="outlined">
          <CardContent sx={{ display: "flex", alignItems: "flex-start", gap: 2 }}>
            <Box sx={{ flex: 1 }}>
              <Stack direction="row" spacing={1} alignItems="center" sx={{ mb: 0.5 }}>
                <Chip size="small" label={item.severity} color={severityColor[item.severity] ?? "default"} />
                <Typography variant="h3" sx={{ fontSize: "1rem", fontWeight: 600 }}>
                  {item.title}
                </Typography>
              </Stack>
              <Typography variant="body2" color="text.secondary">
                {item.description}
              </Typography>
            </Box>
            {item.action_label && item.action_path && (
              <Button
                variant="outlined"
                size="small"
                onClick={() => navigate(item.action_path!)}
              >
                {item.action_label}
              </Button>
            )}
          </CardContent>
        </Card>
      ))}
    </Stack>
  );
}

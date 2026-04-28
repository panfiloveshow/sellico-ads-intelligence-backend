import { useState } from "react";
import {
  Alert,
  Box,
  Button,
  Card,
  CardContent,
  Chip,
  CircularProgress,
  FormControl,
  InputLabel,
  MenuItem,
  Select,
  Stack,
  Typography,
  Pagination,
} from "@mui/material";

import {
  useRecommendations,
  useApplyRecommendation,
  useDismissRecommendation,
  type Recommendation,
} from "@/api/queries/recommendations";
import { useWorkspaces } from "@/api/queries/workspaces";

const severityColors: Record<string, "default" | "info" | "warning" | "error"> = {
  low: "default",
  medium: "info",
  high: "warning",
  critical: "error",
};

const PER_PAGE = 20;

/**
 * Sprint 7 — Recommendations list.
 *
 * Three filters (severity / type / status) — defaults to status=active so
 * the page is actionable on first load (showing dismissed recs would just
 * be a noisy archive).
 *
 * Each row exposes Apply + Dismiss buttons; mutations invalidate the list
 * AND the dashboard (`ads-overview`) so the attention-items count on the
 * Command Center decrements in real time.
 *
 * Type filter intentionally has hard-coded options (mirroring
 * domain.RecommendationType*) — the backend has no /types endpoint yet,
 * and asking the user to type a slug is a worse UX than a static dropdown.
 */
export function RecommendationsPage() {
  const { data: workspaces } = useWorkspaces();
  const workspaceId = workspaces?.[0]?.id ?? "";

  const [severity, setSeverity] = useState<string>("");
  const [type, setType] = useState<string>("");
  const [status, setStatus] = useState<string>("active");
  const [page, setPage] = useState(1);

  const { data, isLoading, error } = useRecommendations({
    workspaceId,
    page,
    perPage: PER_PAGE,
    severity: severity || undefined,
    type: type || undefined,
    status: status || undefined,
  });

  if (!workspaceId) {
    return <Alert severity="info">Сначала создайте workspace.</Alert>;
  }

  return (
    <Stack spacing={3}>
      <Typography variant="h1" sx={{ fontSize: "1.75rem" }}>Рекомендации</Typography>

      <Stack direction={{ xs: "column", sm: "row" }} spacing={2}>
        <FormControl size="small" sx={{ minWidth: 160 }}>
          <InputLabel>Статус</InputLabel>
          <Select label="Статус" value={status} onChange={(e) => { setStatus(e.target.value); setPage(1); }}>
            <MenuItem value="">Все</MenuItem>
            <MenuItem value="active">Активные</MenuItem>
            <MenuItem value="completed">Применённые</MenuItem>
            <MenuItem value="dismissed">Отклонённые</MenuItem>
          </Select>
        </FormControl>

        <FormControl size="small" sx={{ minWidth: 160 }}>
          <InputLabel>Срочность</InputLabel>
          <Select label="Срочность" value={severity} onChange={(e) => { setSeverity(e.target.value); setPage(1); }}>
            <MenuItem value="">Любая</MenuItem>
            <MenuItem value="critical">Критично</MenuItem>
            <MenuItem value="high">Высокая</MenuItem>
            <MenuItem value="medium">Средняя</MenuItem>
            <MenuItem value="low">Низкая</MenuItem>
          </Select>
        </FormControl>

        <FormControl size="small" sx={{ minWidth: 220 }}>
          <InputLabel>Тип</InputLabel>
          <Select label="Тип" value={type} onChange={(e) => { setType(e.target.value); setPage(1); }}>
            <MenuItem value="">Любой</MenuItem>
            <MenuItem value="raise_bid">Поднять ставку</MenuItem>
            <MenuItem value="lower_bid">Понизить ставку</MenuItem>
            <MenuItem value="bid_adjustment">Корректировка ставки</MenuItem>
            <MenuItem value="position_drop">Падение позиций</MenuItem>
            <MenuItem value="low_ctr">Низкий CTR</MenuItem>
            <MenuItem value="high_spend_low_orders">Большой расход без заказов</MenuItem>
            <MenuItem value="add_minus_phrase">Добавить минус-фразу</MenuItem>
            <MenuItem value="disable_phrase">Отключить фразу</MenuItem>
            <MenuItem value="optimize_seo">Оптимизация SEO</MenuItem>
          </Select>
        </FormControl>
      </Stack>

      {error && <Alert severity="error">Не удалось загрузить: {(error as Error).message}</Alert>}

      {isLoading ? (
        <Box sx={{ display: "flex", justifyContent: "center", py: 6 }}>
          <CircularProgress />
        </Box>
      ) : data?.data.length === 0 ? (
        <Alert severity="success">Активных рекомендаций нет — система не нашла улучшений.</Alert>
      ) : (
        <Stack spacing={1.5}>
          {data?.data.map((rec) => (
            <RecommendationRow key={rec.id} rec={rec} workspaceId={workspaceId} />
          ))}
        </Stack>
      )}

      {data?.meta && data.meta.total > PER_PAGE && (
        <Box sx={{ display: "flex", justifyContent: "center" }}>
          <Pagination
            count={Math.ceil(data.meta.total / PER_PAGE)}
            page={page}
            onChange={(_, p) => setPage(p)}
            color="primary"
          />
        </Box>
      )}
    </Stack>
  );
}

interface RowProps {
  rec: Recommendation;
  workspaceId: string;
}

function RecommendationRow({ rec, workspaceId }: RowProps) {
  const apply = useApplyRecommendation();
  const dismiss = useDismissRecommendation();
  const inFlight = apply.isPending || dismiss.isPending;

  const isActive = rec.status === "active";

  return (
    <Card variant="outlined">
      <CardContent>
        <Stack direction={{ xs: "column", md: "row" }} spacing={2} alignItems={{ md: "flex-start" }}>
          <Box sx={{ flex: 1 }}>
            <Stack direction="row" spacing={1} alignItems="center" sx={{ mb: 0.5 }} flexWrap="wrap">
              <Chip size="small" label={rec.severity} color={severityColors[rec.severity] ?? "default"} />
              <Chip size="small" label={rec.type} variant="outlined" />
              {!isActive && <Chip size="small" label={rec.status} variant="outlined" />}
              <Typography variant="caption" color="text.secondary">
                уверенность: {Math.round(rec.confidence * 100)}%
              </Typography>
            </Stack>
            <Typography variant="h3" sx={{ fontSize: "1rem", fontWeight: 600 }}>
              {rec.title}
            </Typography>
            <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
              {rec.description}
            </Typography>
            {rec.next_action && (
              <Typography variant="body2" sx={{ mt: 1, fontStyle: "italic" }}>
                → {rec.next_action}
              </Typography>
            )}
          </Box>

          {isActive && (
            <Stack direction="row" spacing={1}>
              <Button
                variant="contained"
                size="small"
                disabled={inFlight}
                onClick={() => apply.mutate({ workspaceId, id: rec.id })}
              >
                Применить
              </Button>
              <Button
                variant="outlined"
                size="small"
                disabled={inFlight}
                onClick={() => dismiss.mutate({ workspaceId, id: rec.id })}
              >
                Отклонить
              </Button>
            </Stack>
          )}
        </Stack>
        {(apply.error || dismiss.error) && (
          <Alert severity="error" sx={{ mt: 1 }}>
            {(apply.error ?? dismiss.error)?.toString()}
          </Alert>
        )}
      </CardContent>
    </Card>
  );
}

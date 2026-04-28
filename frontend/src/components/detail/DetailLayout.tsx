import { Box, Button, Chip, Stack, Typography, Skeleton, Alert } from "@mui/material";
import ArrowBackIcon from "@mui/icons-material/ArrowBack";
import { useNavigate } from "react-router-dom";
import type { ReactNode } from "react";

interface DetailLayoutProps {
  /** Page title shown after the back button. */
  title: string;
  /** Optional subtitle — accepts string or rich content (e.g. inline link to parent entity). */
  subtitle?: ReactNode;
  /** Right-side metadata chips (status, health, source, etc.). */
  chips?: { label: string; color?: "default" | "primary" | "success" | "warning" | "error" | "info" }[];
  /** Where the back button navigates — defaults to history.back(). */
  backTo?: string;
  /** Page body. */
  children: ReactNode;
  /** Skeleton in place of body while loading. */
  loading?: boolean;
  /** Error to render in place of body when fetch fails. */
  error?: Error | null;
}

/**
 * Shared scaffolding for `/products/:id`, `/campaigns/:id`, `/queries/:id`.
 *
 * Every detail page has the same anatomy:
 *   ┌─ ← back │ title (subtitle) │ [chips] ──┐
 *   │                                         │
 *   │ ← children (metrics, tables, charts) → │
 *   │                                         │
 *   └─────────────────────────────────────────┘
 *
 * Loading and error states are rendered inside the layout so the header
 * stays put — gives the user a stable visual anchor (and a back button)
 * even when the data hasn't arrived yet.
 */
export function DetailLayout({ title, subtitle, chips, backTo, children, loading, error }: DetailLayoutProps) {
  const navigate = useNavigate();
  const onBack = () => (backTo ? navigate(backTo) : navigate(-1));

  return (
    <Stack spacing={3}>
      <Stack direction="row" alignItems="flex-start" spacing={2} flexWrap="wrap">
        <Button startIcon={<ArrowBackIcon />} onClick={onBack} size="small" sx={{ mt: 0.5 }}>
          Назад
        </Button>
        <Box sx={{ flex: 1, minWidth: 200 }}>
          {loading ? (
            <Skeleton variant="text" width="40%" height={36} />
          ) : (
            <Typography variant="h1" sx={{ fontSize: "1.75rem" }}>
              {title}
            </Typography>
          )}
          {subtitle && !loading && (
            <Typography variant="body2" color="text.secondary">
              {subtitle}
            </Typography>
          )}
        </Box>
        {chips && chips.length > 0 && (
          <Stack direction="row" spacing={1}>
            {chips.map((c, i) => (
              <Chip key={i} size="small" label={c.label} color={c.color ?? "default"} />
            ))}
          </Stack>
        )}
      </Stack>

      {error ? (
        <Alert severity="error">Не удалось загрузить детали: {error.message}</Alert>
      ) : (
        children
      )}
    </Stack>
  );
}

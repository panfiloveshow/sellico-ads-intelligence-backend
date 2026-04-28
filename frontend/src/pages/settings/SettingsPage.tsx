import { useState } from "react";
import {
  Alert,
  Box,
  Button,
  Card,
  CardContent,
  Chip,
  CircularProgress,
  Stack,
  Tab,
  Tabs,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  Typography,
} from "@mui/material";

import { useWorkspaces } from "@/api/queries/workspaces";
import {
  useSellerCabinets,
  useTriggerSync,
  useWorkspaceSettings,
  type SellerCabinet,
} from "@/api/queries/settings";

type TabValue = "general" | "cabinets" | "thresholds";

/**
 * Sprint 7 — Settings page (4 tabs planned: General / Cabinets / Thresholds /
 * Members; Members deferred to Track C SSO migration since it overlaps
 * with Sellico-side workspace member management).
 *
 * Tab switching is local state — URL-pinned tabs (via routes /settings/*)
 * goes into the polish pass once we know which tab gets the most traffic.
 */
export function SettingsPage() {
  const { data: workspaces } = useWorkspaces();
  const workspaceId = workspaces?.[0]?.id ?? "";
  const [tab, setTab] = useState<TabValue>("general");

  if (!workspaceId) {
    return <Alert severity="info">Сначала создайте workspace.</Alert>;
  }

  return (
    <Stack spacing={3}>
      <Typography variant="h1" sx={{ fontSize: "1.75rem" }}>Настройки</Typography>

      <Tabs value={tab} onChange={(_, v) => setTab(v)}>
        <Tab value="general" label="Общие" />
        <Tab value="cabinets" label="WB-кабинеты" />
        <Tab value="thresholds" label="Пороги движка" />
      </Tabs>

      {tab === "general" && <GeneralTab workspace={workspaces?.[0]} />}
      {tab === "cabinets" && <CabinetsTab workspaceId={workspaceId} />}
      {tab === "thresholds" && <ThresholdsTab workspaceId={workspaceId} />}
    </Stack>
  );
}

function GeneralTab({ workspace }: { workspace?: { id: string; name: string; slug: string } }) {
  if (!workspace) return null;
  return (
    <Card variant="outlined">
      <CardContent>
        <Stack spacing={1}>
          <Box>
            <Typography variant="caption" color="text.secondary">Название</Typography>
            <Typography>{workspace.name}</Typography>
          </Box>
          <Box>
            <Typography variant="caption" color="text.secondary">Slug</Typography>
            <Typography sx={{ fontFamily: "monospace" }}>{workspace.slug}</Typography>
          </Box>
          <Box>
            <Typography variant="caption" color="text.secondary">ID</Typography>
            <Typography sx={{ fontFamily: "monospace", fontSize: "0.8rem" }}>{workspace.id}</Typography>
          </Box>
          <Typography variant="body2" color="text.disabled" sx={{ mt: 2 }}>
            Редактирование общих настроек в Sprint 7 polish.
          </Typography>
        </Stack>
      </CardContent>
    </Card>
  );
}

function CabinetsTab({ workspaceId }: { workspaceId: string }) {
  const { data, isLoading, error } = useSellerCabinets({ workspaceId });
  const triggerSync = useTriggerSync();

  if (isLoading) return <Box sx={{ display: "flex", justifyContent: "center", py: 4 }}><CircularProgress /></Box>;
  if (error) return <Alert severity="error">Не удалось загрузить: {(error as Error).message}</Alert>;
  if (!data || data.length === 0) {
    return (
      <Alert severity="info">
        WB-кабинеты ещё не подключены. Подключение происходит автоматически после
        того, как ваш workspace связан с Sellico (см. поле external_workspace_id).
      </Alert>
    );
  }

  return (
    <Card variant="outlined">
      <CardContent>
        <Table>
          <TableHead>
            <TableRow>
              <TableCell>Кабинет</TableCell>
              <TableCell>Статус</TableCell>
              <TableCell>Источник</TableCell>
              <TableCell>Последний sync</TableCell>
              <TableCell align="right">Действие</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {data.map((c) => <CabinetRow key={c.id} cabinet={c} workspaceId={workspaceId} onTrigger={triggerSync.mutate} pending={triggerSync.isPending} />)}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

function CabinetRow({
  cabinet,
  workspaceId,
  onTrigger,
  pending,
}: {
  cabinet: SellerCabinet;
  workspaceId: string;
  onTrigger: (params: { workspaceId: string; cabinetId: string }) => void;
  pending: boolean;
}) {
  return (
    <TableRow>
      <TableCell>{cabinet.name}</TableCell>
      <TableCell>
        <Chip
          size="small"
          label={cabinet.status}
          color={cabinet.status === "active" ? "success" : cabinet.status === "error" ? "error" : "default"}
        />
      </TableCell>
      <TableCell>
        <Chip size="small" label={cabinet.source} variant="outlined" />
      </TableCell>
      <TableCell>
        {cabinet.last_synced_at ? new Date(cabinet.last_synced_at).toLocaleString("ru-RU") : "—"}
      </TableCell>
      <TableCell align="right">
        <Button
          size="small"
          variant="outlined"
          disabled={pending}
          onClick={() => onTrigger({ workspaceId, cabinetId: cabinet.id })}
        >
          Sync
        </Button>
      </TableCell>
    </TableRow>
  );
}

function ThresholdsTab({ workspaceId }: { workspaceId: string }) {
  const { data, isLoading, error } = useWorkspaceSettings({ workspaceId });

  if (isLoading) return <Box sx={{ display: "flex", justifyContent: "center", py: 4 }}><CircularProgress /></Box>;
  if (error) return <Alert severity="error">Не удалось загрузить: {(error as Error).message}</Alert>;

  return (
    <Card variant="outlined">
      <CardContent>
        <Typography variant="body2" color="text.secondary" gutterBottom>
          Текущие настройки workspace (recommendation engine + telegram + autopilot):
        </Typography>
        <Box component="pre" sx={{
          fontFamily: "monospace",
          fontSize: "0.8rem",
          backgroundColor: "background.default",
          p: 1.5,
          borderRadius: 1,
          overflow: "auto",
          maxHeight: 400,
        }}>
{JSON.stringify(data ?? {}, null, 2)}
        </Box>
        <Typography variant="body2" color="text.disabled" sx={{ mt: 2 }}>
          Form-builder для редактирования — Sprint 7 polish.
        </Typography>
      </CardContent>
    </Card>
  );
}

import { Card, CardContent, Stack, Typography } from "@mui/material";

// Stub: Sprint 6 fills this in with live overview data, attention items,
// and top campaigns/products/queries from /api/v1/ads/overview.
export function CommandCenterPage() {
  return (
    <Stack spacing={3}>
      <Typography variant="h1">Командный центр</Typography>
      <Card>
        <CardContent>
          <Typography variant="h3" gutterBottom>
            Каркас собран
          </Typography>
          <Typography variant="body2">
            Каркас фронтенда (Sprint 5 v1.0 roadmap) готов. Следующий этап —
            подключение реальных данных из <code>/api/v1/ads/overview</code>,
            таблицы кампаний и графики (Sprint 6).
          </Typography>
        </CardContent>
      </Card>
    </Stack>
  );
}

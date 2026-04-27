import { useState } from "react";
import { useNavigate, useLocation, Navigate } from "react-router-dom";
import { Alert, Box, Button, Paper, Stack, TextField, Typography } from "@mui/material";

import { useAuth } from "@/lib/auth";

export function LoginPage() {
  const { user, login } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  if (user) {
    const from = (location.state as { from?: string } | null)?.from ?? "/";
    return <Navigate to={from} replace />;
  }

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    const result = await login(email, password);
    setSubmitting(false);
    if (!result.ok) {
      setError(result.error);
      return;
    }
    navigate("/");
  };

  return (
    <Box sx={{ minHeight: "100vh", display: "flex", alignItems: "center", justifyContent: "center", bgcolor: "background.default" }}>
      <Paper sx={{ p: 4, width: 360 }}>
        <Stack component="form" spacing={2} onSubmit={onSubmit}>
          <Typography variant="h2">Войти</Typography>
          {error && <Alert severity="error">{error}</Alert>}
          <TextField
            autoFocus
            label="Email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            autoComplete="email"
          />
          <TextField
            label="Пароль"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            autoComplete="current-password"
          />
          <Button type="submit" variant="contained" disabled={submitting}>
            {submitting ? "Вход…" : "Войти"}
          </Button>
        </Stack>
      </Paper>
    </Box>
  );
}

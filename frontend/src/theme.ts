import { createTheme } from "@mui/material";

// Design tokens come from frontend-spec/ARCHITECTURE.md. The palette skews
// neutral with a single accent so dense data tables (the bulk of the UI)
// stay legible. Light theme is the default; dark mode toggle lives in
// settings (Sprint 7).
export const theme = createTheme({
  palette: {
    mode: "light",
    primary: { main: "#3046C5" },
    secondary: { main: "#7B1FA2" },
    error: { main: "#C62828" },
    warning: { main: "#EF6C00" },
    success: { main: "#2E7D32" },
    background: { default: "#F5F6FA", paper: "#FFFFFF" },
  },
  typography: {
    fontFamily:
      '"Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
    h1: { fontSize: "1.75rem", fontWeight: 600 },
    h2: { fontSize: "1.5rem", fontWeight: 600 },
    h3: { fontSize: "1.25rem", fontWeight: 600 },
    body2: { color: "#555" },
  },
  shape: { borderRadius: 8 },
  components: {
    MuiButton: { defaultProps: { disableElevation: true } },
    MuiPaper: { defaultProps: { elevation: 0 }, styleOverrides: { root: { border: "1px solid #E5E7EB" } } },
  },
});

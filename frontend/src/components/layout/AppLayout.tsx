import { AppBar, Box, Drawer, IconButton, List, ListItemButton, ListItemIcon, ListItemText, Toolbar, Typography } from "@mui/material";
import LogoutIcon from "@mui/icons-material/Logout";
import DashboardIcon from "@mui/icons-material/Dashboard";
import LightbulbIcon from "@mui/icons-material/Lightbulb";
import SettingsIcon from "@mui/icons-material/Settings";
import { Outlet, useLocation, useNavigate } from "react-router-dom";

import { useAuth } from "@/lib/auth";

const SIDEBAR_WIDTH = 220;

// Sprint 6 nav: dashboard + recommendations + settings only.
// Campaigns/products list pages get their own sidebar entries when the
// dedicated list pages land (Sprint 7); for now they're reachable via
// drill-down from the dashboard.
const NAV = [
  { to: "/", label: "Командный центр", icon: <DashboardIcon /> },
  { to: "/recommendations", label: "Рекомендации", icon: <LightbulbIcon /> },
  { to: "/settings", label: "Настройки", icon: <SettingsIcon /> },
];

export function AppLayout() {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();

  return (
    <Box sx={{ display: "flex", height: "100vh" }}>
      <AppBar
        position="fixed"
        color="default"
        elevation={0}
        sx={{ width: `calc(100% - ${SIDEBAR_WIDTH}px)`, ml: `${SIDEBAR_WIDTH}px`, borderBottom: "1px solid #E5E7EB" }}
      >
        <Toolbar sx={{ justifyContent: "space-between" }}>
          <Typography variant="h3">Sellico Ads Intelligence</Typography>
          <Box sx={{ display: "flex", alignItems: "center", gap: 2 }}>
            <Typography variant="body2">{user?.email}</Typography>
            <IconButton
              size="small"
              aria-label="Выйти"
              onClick={async () => {
                await logout();
                navigate("/login");
              }}
            >
              <LogoutIcon fontSize="small" />
            </IconButton>
          </Box>
        </Toolbar>
      </AppBar>

      <Drawer
        variant="permanent"
        sx={{
          width: SIDEBAR_WIDTH,
          flexShrink: 0,
          [`& .MuiDrawer-paper`]: { width: SIDEBAR_WIDTH, boxSizing: "border-box" },
        }}
      >
        <Toolbar sx={{ px: 2 }}>
          <Typography variant="h3" color="primary">Sellico</Typography>
        </Toolbar>
        <List>
          {NAV.map((item) => (
            <ListItemButton
              key={item.to}
              selected={location.pathname === item.to}
              onClick={() => navigate(item.to)}
            >
              <ListItemIcon sx={{ minWidth: 40 }}>{item.icon}</ListItemIcon>
              <ListItemText primary={item.label} />
            </ListItemButton>
          ))}
        </List>
      </Drawer>

      <Box component="main" sx={{ flexGrow: 1, mt: 8, p: 3, overflow: "auto", backgroundColor: "background.default" }}>
        <Outlet />
      </Box>
    </Box>
  );
}

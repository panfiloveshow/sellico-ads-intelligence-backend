import { Navigate, Outlet, Route, Routes } from "react-router-dom";
import { CircularProgress, Stack } from "@mui/material";

import { AuthProvider, useAuth } from "@/lib/auth";
import { AppLayout } from "@/components/layout/AppLayout";
import { LoginPage } from "@/pages/auth/LoginPage";
import { CommandCenterPage } from "@/pages/dashboard/CommandCenterPage";
import { ProductDetailPage } from "@/pages/products/ProductDetailPage";
import { CampaignDetailPage } from "@/pages/campaigns/CampaignDetailPage";
import { QueryDetailPage } from "@/pages/queries/QueryDetailPage";

export function App() {
  return (
    <AuthProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route element={<RequireAuth />}>
          <Route element={<AppLayout />}>
            <Route path="/" element={<CommandCenterPage />} />
            <Route path="/products/:id" element={<ProductDetailPage />} />
            <Route path="/campaigns/:id" element={<CampaignDetailPage />} />
            <Route path="/queries/:id" element={<QueryDetailPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Route>
      </Routes>
    </AuthProvider>
  );
}

function RequireAuth() {
  const { user, loading } = useAuth();
  if (loading) {
    return (
      <Stack alignItems="center" justifyContent="center" sx={{ height: "100vh" }}>
        <CircularProgress />
      </Stack>
    );
  }
  if (!user) return <Navigate to="/login" replace />;
  return <Outlet />;
}

import { Component, type ErrorInfo, type ReactNode } from "react";
import { Alert, AlertTitle, Box, Button, Stack } from "@mui/material";

interface Props {
  children: ReactNode;
  /** Optional fallback override; defaults to a generic alert with retry. */
  fallback?: (error: Error, reset: () => void) => ReactNode;
}

interface State {
  error: Error | null;
}

/**
 * App-level ErrorBoundary. Wraps the protected routes so a render error in
 * any single page renders a friendly fallback instead of a blank screen.
 *
 * What it intentionally does NOT do:
 *  - Report to Sentry / a remote tracker — Sprint 7 polish task. Logs to
 *    console only for now.
 *  - Reset on route change — TanStack Query handles its own retry/reset
 *    for data fetches, and we don't want a navigation to silently mask
 *    a render bug. The user has to explicitly click "Try again".
 *
 * Class component is required because React still has no hook equivalent
 * (`use(error)` boundary RFCs are not stable yet).
 */
export class ErrorBoundary extends Component<Props, State> {
  override state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  override componentDidCatch(error: Error, info: ErrorInfo) {
    // eslint-disable-next-line no-console
    console.error("ErrorBoundary caught", error, info.componentStack);
  }

  reset = () => this.setState({ error: null });

  override render() {
    const { error } = this.state;
    const { children, fallback } = this.props;
    if (!error) return children;
    if (fallback) return fallback(error, this.reset);

    return (
      <Box sx={{ p: 4 }}>
        <Alert
          severity="error"
          action={
            <Button color="inherit" size="small" onClick={this.reset}>
              Попробовать снова
            </Button>
          }
        >
          <AlertTitle>Что-то пошло не так</AlertTitle>
          <Stack spacing={1}>
            <span>{error.message}</span>
            <span style={{ fontSize: "0.75rem", opacity: 0.7 }}>
              Если это повторяется — обновите страницу или сообщите команде.
            </span>
          </Stack>
        </Alert>
      </Box>
    );
  }
}

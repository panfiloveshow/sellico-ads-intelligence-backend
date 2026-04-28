/// <reference types="vite/client" />

// Strongly-type the env vars we actually use, so misspellings surface at
// compile time. Add new VITE_* keys here as they appear in vite.config.ts
// or .env.local files.
interface ImportMetaEnv {
  readonly VITE_API_BASE_URL?: string;
  readonly VITE_API_TARGET?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

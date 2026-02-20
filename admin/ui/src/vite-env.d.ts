/// <reference types="vite/client" />

import type { WalletInterface } from "@bsv/sdk";

declare global {
  interface Window {
    CWI?: WalletInterface;
  }
}

// An in-app confirmation dialog, replacing window.confirm: themed, keyboard
// friendly, and able to say what the confirm button will do ("Remove list")
// instead of a bare OK. ConfirmHost (mounted once in App) renders it.
import { writable } from 'svelte/store';

export interface ConfirmRequest {
  message: string;
  confirm: string; // the button label: what happens
  cancel?: string;
  danger?: boolean;
  resolve: (ok: boolean) => void;
}

export const pendingConfirm = writable<ConfirmRequest | null>(null);

export function confirmDialog(opts: Omit<ConfirmRequest, 'resolve'>): Promise<boolean> {
  return new Promise((resolve) => {
    pendingConfirm.set({
      ...opts,
      resolve: (ok) => {
        pendingConfirm.set(null);
        resolve(ok);
      },
    });
  });
}

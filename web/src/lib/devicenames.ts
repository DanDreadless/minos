// Device names by address, so pages that list queries can say "Kitchen
// iPad" instead of 192.168.1.21. Loaded on first use and refreshed once a
// minute while something uses it; a name is the user's label, else the
// discovered hostname.
import { writable, type Readable } from 'svelte/store';
import { api } from './api';

const store = writable<Record<string, string>>({});
let users = 0;
let timer: ReturnType<typeof setInterval> | null = null;

async function load(): Promise<void> {
  try {
    const out: Record<string, string> = {};
    for (const d of await api.clients()) {
      const name = (d.name || d.hostname || '').trim();
      if (!name) continue;
      for (const ip of d.ips?.length ? d.ips : [d.ip]) out[ip] = name;
    }
    store.set(out);
  } catch {
    // Names are a nicety; addresses still show.
  }
}

// DeviceNames is the address → name map. Subscribing starts the refresh.
export const deviceNames: Readable<Record<string, string>> = {
  subscribe(run) {
    if (users++ === 0) {
      void load();
      timer = setInterval(load, 60_000);
    }
    const unsub = store.subscribe(run);
    return () => {
      unsub();
      if (--users === 0 && timer) {
        clearInterval(timer);
        timer = null;
      }
    };
  },
};

import { writable } from 'svelte/store';
import { Login, Logout, IsAuthenticated, GetServerURL } from '../../bindings/immich-desktop-sync/app';

export interface User {
  id: string;
  email: string;
  name: string;
}

export interface AuthState {
  authenticated: boolean;
  user: User | null;
  serverUrl: string;
  loading: boolean;
  error: string | null;
}

function createAuthStore() {
  const { subscribe, set, update } = writable<AuthState>({
    authenticated: false,
    user: null,
    serverUrl: '',
    loading: false,
    error: null,
  });

  return {
    subscribe,

    async init() {
      try {
        const [authenticated, serverUrl] = await Promise.all([
          IsAuthenticated(),
          GetServerURL(),
        ]);
        update(s => ({ ...s, authenticated, serverUrl }));
      } catch (e) {
        console.error('auth init:', e);
      }
    },

    async login(serverUrl: string, email: string, password: string) {
      update(s => ({ ...s, loading: true, error: null }));
      try {
        const user = await Login(serverUrl, email, password);
        set({ authenticated: true, user: user as User, serverUrl, loading: false, error: null });
      } catch (e: any) {
        update(s => ({ ...s, loading: false, error: String(e) }));
        throw e;
      }
    },

    async logout() {
      await Logout();
      set({ authenticated: false, user: null, serverUrl: '', loading: false, error: null });
    },
  };
}

export const auth = createAuthStore();

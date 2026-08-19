import { writable } from 'svelte/store';
import { GetUploadQueue, GetFolders, AddFolder, RemoveFolder, SelectFolder } from '../../bindings/immich-desktop-sync/app';

export interface UploadQueueItem {
  id: number;
  filePath: string;
  status: 'pending' | 'uploading' | 'done' | 'failed';
  retryCount: number;
  lastAttempt: string;
  error: string;
}

export interface UploadsState {
  queue: UploadQueueItem[];
  folders: string[];
  loading: boolean;
}

function createUploadsStore() {
  const { subscribe, set, update } = writable<UploadsState>({
    queue: [],
    folders: [],
    loading: false,
  });

  let pollTimer: ReturnType<typeof setInterval> | null = null;

  return {
    subscribe,

    async refresh() {
      try {
        const [queue, folders] = await Promise.all([
          GetUploadQueue(),
          GetFolders(),
        ]);
        update(s => ({
          ...s,
          queue: (queue as UploadQueueItem[]) ?? [],
          folders: (folders as string[]) ?? [],
        }));
      } catch (e) {
        console.error('uploads refresh:', e);
      }
    },

    startPolling(intervalMs = 5000) {
      if (pollTimer) return;
      pollTimer = setInterval(() => this.refresh(), intervalMs);
      this.refresh();
    },

    stopPolling() {
      if (pollTimer) {
        clearInterval(pollTimer);
        pollTimer = null;
      }
    },

    async addFolder() {
      const path = await SelectFolder();
      if (!path) return;
      await AddFolder(path);
      await this.refresh();
    },

    async addFolderByPath(path: string) {
      await AddFolder(path);
      await this.refresh();
    },

    async removeFolder(path: string) {
      await RemoveFolder(path);
      await this.refresh();
    },
  };
}

export const uploads = createUploadsStore();

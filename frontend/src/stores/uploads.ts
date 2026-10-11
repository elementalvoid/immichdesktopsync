import { writable } from 'svelte/store';
import { Events } from '@wailsio/runtime';
import { GetUploadQueue, GetFolders, AddFolder, RemoveFolder, SelectFolder } from '../../bindings/immich-desktop-sync/app';

export interface UploadQueueItem {
  id: number;
  filePath: string;
  status: 'pending' | 'uploading' | 'done' | 'failed' | 'exhausted';
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
  const { subscribe, update } = writable<UploadsState>({
    queue: [],
    folders: [],
    loading: false,
  });

  let queueRefreshing = false;
  let queueAgain = false;

  // A folder copy emits one upload:changed event per file, so coalesce: keep
  // one queue fetch in flight and run one more if events arrived meanwhile.
  async function refreshQueue() {
    if (queueRefreshing) {
      queueAgain = true;
      return;
    }
    queueRefreshing = true;
    try {
      do {
        queueAgain = false;
        const queue = await GetUploadQueue();
        update(s => ({ ...s, queue: (queue as UploadQueueItem[]) ?? [] }));
      } while (queueAgain);
    } catch (e) {
      console.error('uploads refresh:', e);
    } finally {
      queueRefreshing = false;
    }
  }

  async function refreshFolders() {
    try {
      const folders = await GetFolders();
      update(s => ({ ...s, folders: (folders as string[]) ?? [] }));
    } catch (e) {
      console.error('uploads refresh:', e);
    }
  }

  const refresh = () => Promise.all([refreshQueue(), refreshFolders()]);

  let bound = false;

  return {
    subscribe,
    refresh,

    // The backend emits upload:changed on every enqueue, start, done, and
    // fail, so the store does not poll. Only the queue can change on those
    // events; folders change through addFolder/removeFolder.
    bindEvents() {
      if (bound) return;
      bound = true;
      Events.On('upload:changed', refreshQueue);
      refresh();
    },

    unbindEvents() {
      if (!bound) return;
      bound = false;
      Events.Off('upload:changed');
    },

    async addFolder() {
      const path = await SelectFolder();
      if (!path) return;
      await AddFolder(path);
      await refreshFolders();
    },

    async addFolderByPath(path: string) {
      await AddFolder(path);
      await refreshFolders();
    },

    async removeFolder(path: string) {
      await RemoveFolder(path);
      await refreshFolders();
    },
  };
}

export const uploads = createUploadsStore();

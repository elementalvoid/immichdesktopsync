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

  // Named so the event handler is the same function on bind and unbind.
  async function refresh() {
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
  }

  let bound = false;

  return {
    subscribe,
    refresh,

    // The backend emits upload:changed on every enqueue, start, done, and
    // fail, so the store does not poll.
    bindEvents() {
      if (bound) return;
      bound = true;
      Events.On('upload:changed', refresh);
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

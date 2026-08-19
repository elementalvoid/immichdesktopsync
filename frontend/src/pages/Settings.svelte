<script lang="ts">
  import { onMount } from 'svelte';
  import { ClearCache, GetDownloadsFolder, SetDownloadsFolder, SelectFolder } from '../../bindings/immich-desktop-sync/app';
  import { auth } from '../stores/auth';
  import { uploads } from '../stores/uploads';
  import FolderList from '../components/FolderList.svelte';

  let cacheClearing = false;
  let cacheMsg = '';
  let folderError = '';
  let folderAdding = false;
  let manualPath = '';

  let downloadsFolder = '';
  let downloadsFolderSaving = false;
  let downloadsFolderMsg = '';
  let manualDownloadsPath = '';

  onMount(async () => {
    uploads.refresh();
    downloadsFolder = await GetDownloadsFolder();
  });

  async function handleLogout() {
    await auth.logout();
  }

  async function handleClearCache() {
    cacheClearing = true;
    cacheMsg = '';
    try {
      await ClearCache();
      cacheMsg = 'Cache cleared.';
    } catch (e: any) {
      cacheMsg = 'Error: ' + String(e);
    } finally {
      cacheClearing = false;
    }
  }

  async function handleBrowse() {
    folderError = '';
    folderAdding = true;
    try {
      await uploads.addFolder();
    } catch (e: any) {
      folderError = String(e).replace(/^Error: /, '');
    } finally {
      folderAdding = false;
    }
  }

  async function handleManualAdd() {
    const path = manualPath.trim();
    if (!path) return;
    folderError = '';
    folderAdding = true;
    try {
      await uploads.addFolderByPath(path);
      manualPath = '';
    } catch (e: any) {
      folderError = String(e).replace(/^Error: /, '');
    } finally {
      folderAdding = false;
    }
  }

  async function handleBrowseDownloads() {
    downloadsFolderMsg = '';
    downloadsFolderSaving = true;
    try {
      const path = await SelectFolder();
      if (!path) return;
      await SetDownloadsFolder(path);
      downloadsFolder = path;
      downloadsFolderMsg = 'Saved.';
    } catch (e: any) {
      downloadsFolderMsg = 'Error: ' + String(e).replace(/^Error: /, '');
    } finally {
      downloadsFolderSaving = false;
    }
  }

  async function handleSetDownloadsPath() {
    const path = manualDownloadsPath.trim();
    if (!path) return;
    downloadsFolderMsg = '';
    downloadsFolderSaving = true;
    try {
      await SetDownloadsFolder(path);
      downloadsFolder = path;
      manualDownloadsPath = '';
      downloadsFolderMsg = 'Saved.';
    } catch (e: any) {
      downloadsFolderMsg = 'Error: ' + String(e).replace(/^Error: /, '');
    } finally {
      downloadsFolderSaving = false;
    }
  }
</script>

<div class="flex h-full flex-col overflow-y-auto p-6">
  <h2 class="mb-6 text-xl font-semibold text-[#cdd6f4]">Settings</h2>

  <section class="mb-6 rounded-xl bg-[#313244] p-5">
    <h3 class="mb-3 text-sm font-semibold uppercase tracking-wider text-[#a6adc8]">Server</h3>
    <p class="text-sm text-[#cdd6f4]">
      <span class="text-[#a6adc8]">URL: </span>{$auth.serverUrl || '—'}
    </p>
  </section>

  <section class="mb-6 rounded-xl bg-[#313244] p-5">
    <div class="mb-3 flex items-center justify-between">
      <h3 class="text-sm font-semibold uppercase tracking-wider text-[#a6adc8]">Watched Folders</h3>
      <button
        on:click={handleBrowse}
        disabled={folderAdding}
        class="flex items-center gap-1 rounded-lg bg-[#4250af] px-3 py-1.5 text-sm font-medium text-white transition hover:bg-[#5c6bc0] disabled:opacity-50"
      >
        <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
        </svg>
        {folderAdding ? 'Adding…' : 'Browse…'}
      </button>
    </div>

    <div class="mb-3 flex gap-2">
      <input
        bind:value={manualPath}
        on:keydown={(e) => e.key === 'Enter' && handleManualAdd()}
        placeholder="Or paste a folder path here…"
        class="flex-1 rounded-lg bg-[#1e1e2e] px-3 py-2 text-sm text-[#cdd6f4] placeholder-[#585b70] outline-none ring-1 ring-[#45475a] focus:ring-[#4250af]"
      />
      <button
        on:click={handleManualAdd}
        disabled={!manualPath.trim() || folderAdding}
        class="rounded-lg bg-[#313244] px-3 py-2 text-sm text-[#a6adc8] transition hover:bg-[#45475a] disabled:opacity-40 ring-1 ring-[#45475a]"
      >Add</button>
    </div>

    {#if folderError}
      <p class="mb-3 rounded-lg bg-red-900/30 border border-red-500/40 px-3 py-2 text-sm text-red-300">{folderError}</p>
    {/if}

    <FolderList />
  </section>

  <section class="mb-6 rounded-xl bg-[#313244] p-5">
    <div class="mb-3 flex items-center justify-between">
      <h3 class="text-sm font-semibold uppercase tracking-wider text-[#a6adc8]">Downloads Folder</h3>
      <button
        on:click={handleBrowseDownloads}
        disabled={downloadsFolderSaving}
        class="flex items-center gap-1 rounded-lg bg-[#4250af] px-3 py-1.5 text-sm font-medium text-white transition hover:bg-[#5c6bc0] disabled:opacity-50"
      >
        <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z"/>
        </svg>
        {downloadsFolderSaving ? 'Saving…' : 'Browse…'}
      </button>
    </div>

    {#if downloadsFolder}
      <p class="mb-3 truncate rounded-lg bg-[#1e1e2e] px-3 py-2 text-sm text-[#cdd6f4]" title={downloadsFolder}>{downloadsFolder}</p>
    {:else}
      <p class="mb-3 text-sm text-[#585b70]">No downloads folder set. Files will be saved here when you download from the gallery.</p>
    {/if}

    <div class="flex gap-2">
      <input
        bind:value={manualDownloadsPath}
        on:keydown={(e) => e.key === 'Enter' && handleSetDownloadsPath()}
        placeholder="Or paste a folder path here…"
        class="flex-1 rounded-lg bg-[#1e1e2e] px-3 py-2 text-sm text-[#cdd6f4] placeholder-[#585b70] outline-none ring-1 ring-[#45475a] focus:ring-[#4250af]"
      />
      <button
        on:click={handleSetDownloadsPath}
        disabled={!manualDownloadsPath.trim() || downloadsFolderSaving}
        class="rounded-lg bg-[#313244] px-3 py-2 text-sm text-[#a6adc8] transition hover:bg-[#45475a] disabled:opacity-40 ring-1 ring-[#45475a]"
      >Set</button>
    </div>
    {#if downloadsFolderMsg}
      <p class="mt-2 text-sm {downloadsFolderMsg.startsWith('Error') ? 'text-red-400' : 'text-green-400'}">{downloadsFolderMsg}</p>
    {/if}
  </section>

  <section class="mb-6 rounded-xl bg-[#313244] p-5">
    <h3 class="mb-3 text-sm font-semibold uppercase tracking-wider text-[#a6adc8]">Cache</h3>
    <p class="mb-3 text-sm text-[#a6adc8]">Clear the local thumbnail cache to free up disk space.</p>
    <button
      on:click={handleClearCache}
      disabled={cacheClearing}
      class="rounded-lg border border-[#45475a] px-4 py-2 text-sm text-[#cdd6f4] transition hover:bg-[#45475a] disabled:opacity-50"
    >
      {cacheClearing ? 'Clearing…' : 'Clear Cache'}
    </button>
    {#if cacheMsg}
      <p class="mt-2 text-sm text-[#a6adc8]">{cacheMsg}</p>
    {/if}
  </section>

  <section class="rounded-xl bg-[#313244] p-5">
    <h3 class="mb-3 text-sm font-semibold uppercase tracking-wider text-[#a6adc8]">Account</h3>
    <button
      on:click={handleLogout}
      class="rounded-lg border border-red-500/50 bg-red-900/20 px-4 py-2 text-sm font-medium text-red-300 transition hover:bg-red-900/40"
    >
      Sign out
    </button>
  </section>
</div>

<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { auth } from './stores/auth';
  import { uploads } from './stores/uploads';
  import Login from './pages/Login.svelte';
  import Gallery from './pages/Gallery.svelte';
  import Settings from './pages/Settings.svelte';
  import UploadStatus from './components/UploadStatus.svelte';
  import { Events } from '@wailsio/runtime';

  type Page = 'gallery' | 'settings';
  let page: Page = 'gallery';

  let isDragging = false;
  let dragCounter = 0;

  onMount(async () => {
    await auth.init();
    if ($auth.authenticated) {
      uploads.startPolling();
    }

    Events.On('files:dropped', () => {
      dragCounter = 0;
      isDragging = false;
      uploads.refresh();
    });

    Events.On('upload:started', () => uploads.refresh());
    Events.On('upload:done', () => uploads.refresh());

    window.addEventListener('dragenter', onDragEnter);
    window.addEventListener('dragleave', onDragLeave);
  });

  onDestroy(() => {
    uploads.stopPolling();
    Events.Off('files:dropped', 'upload:started', 'upload:done');
    window.removeEventListener('dragenter', onDragEnter);
    window.removeEventListener('dragleave', onDragLeave);
  });

  function onDragEnter(e: DragEvent) {
    if (e.dataTransfer?.types.includes('Files')) {
      dragCounter++;
      isDragging = true;
    }
  }
  function onDragLeave() {
    dragCounter--;
    if (dragCounter <= 0) { dragCounter = 0; isDragging = false; }
  }

  $: if ($auth.authenticated) {
    uploads.startPolling();
  } else {
    uploads.stopPolling();
  }

  $: queueSize = $uploads.queue.filter(i => i.status !== 'done').length;
  $: uploadingCount = $uploads.queue.filter(i => i.status === 'uploading').length;

  let showUploadPanel = false;
</script>

{#if !$auth.authenticated}
  <Login />
{:else}
  <div class="flex h-full bg-[#1e1e2e]">
    <aside class="flex w-56 flex-col border-r border-[#45475a] bg-[#181825]">
      <div class="flex items-center gap-3 px-4 py-5">
        <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-[#4250af]">
          <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5 text-white" viewBox="0 0 24 24" fill="currentColor">
            <path d="M21 19V5a2 2 0 00-2-2H5a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2zM8.5 13.5l2.5 3.01L14.5 12l4.5 6H5l3.5-4.5z"/>
          </svg>
        </div>
        <span class="text-sm font-bold text-[#cdd6f4]">Immich Sync</span>
      </div>

      <nav class="flex-1 space-y-1 px-2">
        <button
          on:click={() => page = 'gallery'}
          class="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm transition {page === 'gallery'
            ? 'bg-[#4250af]/20 text-[#4250af] font-medium'
            : 'text-[#a6adc8] hover:bg-[#313244]'}"
        >
          <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/>
            <rect x="14" y="14" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/>
          </svg>
          Gallery
        </button>

        <button
          on:click={() => page = 'settings'}
          class="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm transition {page === 'settings'
            ? 'bg-[#4250af]/20 text-[#4250af] font-medium'
            : 'text-[#a6adc8] hover:bg-[#313244]'}"
        >
          <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="3"/>
            <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/>
          </svg>
          Settings
        </button>
      </nav>

      <div class="border-t border-[#45475a] p-2">
        <button
          on:click={() => (showUploadPanel = !showUploadPanel)}
          class="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm text-[#a6adc8] transition hover:bg-[#313244]"
        >
          <span class="relative">
            <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/>
              <polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/>
            </svg>
            {#if queueSize > 0}
              <span class="absolute -right-1 -top-1 flex h-3 w-3 items-center justify-center rounded-full bg-[#4250af] text-[8px] text-white font-bold">
                {queueSize > 9 ? '9+' : queueSize}
              </span>
            {/if}
          </span>
          Uploads
          {#if uploadingCount > 0}
            <span class="ml-auto flex h-2 w-2 rounded-full bg-blue-400 animate-pulse" />
          {/if}
        </button>
      </div>
    </aside>

    <div class="flex flex-1 flex-col overflow-hidden">
      {#if page === 'gallery'}
        <Gallery />
      {:else}
        <Settings />
      {/if}
    </div>

    {#if showUploadPanel}
      <div class="flex w-72 flex-col border-l border-[#45475a] bg-[#181825]">
        <div class="flex items-center justify-between border-b border-[#45475a] px-4 py-3">
          <span class="text-sm font-semibold text-[#cdd6f4]">Upload Queue</span>
          <button
            on:click={() => (showUploadPanel = false)}
            class="rounded p-1 text-[#585b70] transition hover:text-[#cdd6f4]"
          >
            <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          </button>
        </div>
        <UploadStatus />
      </div>
    {/if}
  </div>

  {#if isDragging}
    <div class="pointer-events-none fixed inset-0 z-50 flex items-center justify-center bg-[#1e1e2e]/80 backdrop-blur-sm">
      <div class="flex flex-col items-center gap-4 rounded-2xl border-2 border-dashed border-[#4250af] bg-[#181825]/90 px-16 py-12 shadow-2xl">
        <svg xmlns="http://www.w3.org/2000/svg" class="h-16 w-16 text-[#4250af]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5m-13.5-9L12 3m0 0l4.5 4.5M12 3v13.5"/>
        </svg>
        <p class="text-lg font-semibold text-[#cdd6f4]">Drop photos to upload</p>
        <p class="text-sm text-[#585b70]">JPG, PNG, HEIC, MP4 and more</p>
      </div>
    </div>
  {/if}

{/if}

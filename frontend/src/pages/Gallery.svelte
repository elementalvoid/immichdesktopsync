<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import { GetAssets, GetAlbums, GetAlbumAssets } from '../../bindings/immich-desktop-sync/app';
  import { Events } from '@wailsio/runtime';
  import PhotoGrid from '../components/PhotoGrid.svelte';
  import Lightbox from '../components/Lightbox.svelte';
  import TimelineRail from '../components/TimelineRail.svelte';
  import { groupByDate, buildTimeline, type Group } from '../lib/gallery';
  import { getThumbUrl } from '../lib/thumbCache';

  // Timeline grouping (photos view only)
  $: groups = view === 'photos' ? groupByDate(allAssets) : ([] as Group<Asset>[]);
  $: displayAssets = groups.flatMap((g) => g.assets);
  $: timeline = buildTimeline(groups);
  let activeDateKey: string | null = null;

  $: activeYear = activeDateKey
    ? (groups.find((g) => g.key === activeDateKey)?.date?.getFullYear() ?? null)
    : null;
  $: activeMonth = activeDateKey
    ? (groups.find((g) => g.key === activeDateKey)?.date?.getMonth() ?? null)
    : null;

  // Scroll container + offset tracking
  let scrollEl: HTMLDivElement;
  const offsets = new Map<string, number>();
  let measured = false;
  const scrollWatcher = (): void => {
    if (!scrollEl) return;
    const top = scrollEl.scrollTop + 20;
    let current: string | null = null;
    for (const g of groups) {
      const off = offsets.get(g.key);
      if (off !== undefined && off <= top) current = g.key;
    }
    if (current !== activeDateKey) activeDateKey = current;
  };

  $: if (groups.length && scrollEl && !measured) {
    tick().then(() => {
      scrollEl.querySelectorAll<HTMLElement>('[data-datekey]').forEach((el) => {
        offsets.set(el.dataset.datekey!, el.offsetTop);
      });
      measured = true;
      scrollWatcher();
    });
  }

  function scrollToGroup(key: string): void {
    const off = offsets.get(key);
    if (off !== undefined && scrollEl) scrollEl.scrollTo({ top: off, behavior: 'smooth' });
  }

  function selectTimeline(year: number, month: number): void {
    const g = groups.find((x) => x.date?.getFullYear() === year && x.date?.getMonth() === month);
    if (g) scrollToGroup(g.key);
  }

  let pendingUploads = 0;
  let gridKey = 0;

  interface Asset {
    id: string;
    type: string;
    originalPath: string;
    originalFileName?: string;
    localDateTime?: string;
    fileCreatedAt?: string;
    fileModifiedAt?: string;
    duration?: string;
    isFavorite?: boolean;
    exifInfo?: {
      exifImageWidth?: number;
      exifImageHeight?: number;
      fileSizeInByte?: number;
      make?: string;
      model?: string;
      lensModel?: string;
      fNumber?: number;
      focalLength?: number;
      iso?: number;
      exposureTime?: string;
      latitude?: number | null;
      longitude?: number | null;
      city?: string;
      state?: string;
      country?: string;
      description?: string;
    };
  }

  interface Album {
    id: string;
    albumName: string;
    description: string;
    assetCount: number;
    albumThumbnailAssetId: string;
  }

  let allAssets: Asset[] = [];
  let albums: Album[] = [];
  let view: 'photos' | 'albums' = 'photos';
  let loading = false;
  let error = '';

  let openAlbum: Album | null = null;
  let albumAssets: Asset[] = [];
  let albumLoading = false;
  let albumError = '';

  let lightboxIndex = -1;
  $: lightboxOpen = lightboxIndex >= 0;
  $: lightboxAssets = openAlbum ? albumAssets : displayAssets;

  onMount(() => {
    loadData();
    Events.On('upload:done', () => { pendingUploads++; });
  });

  onDestroy(() => {
    Events.Off('upload:done');
  });

  async function loadData() {
    loading = true;
    error = '';
    pendingUploads = 0;
    measured = false;
    offsets.clear();
    activeDateKey = null;
    try {
      const [a, al] = await Promise.all([GetAssets(), GetAlbums()]);
      allAssets = (a as Asset[]) ?? [];
      albums = (al as Album[]) ?? [];
    } catch (e: any) {
      error = String(e).replace(/^Error: /, '');
    } finally {
      loading = false;
      gridKey++;
    }
  }

  async function openAlbumView(album: Album) {
    openAlbum = album;
    albumAssets = [];
    albumError = '';
    albumLoading = true;
    try {
      albumAssets = (await GetAlbumAssets(album.id) as Asset[]) ?? [];
    } catch (e: any) {
      albumError = String(e).replace(/^Error: /, '');
    } finally {
      albumLoading = false;
    }
  }

  function closeAlbumView() {
    openAlbum = null;
    albumAssets = [];
    lightboxIndex = -1;
  }

  let coverUrls: Record<string, string> = {};
  async function loadCover(album: Album) {
    if (!album.albumThumbnailAssetId || coverUrls[album.id]) return;
    const url = await getThumbUrl(album.albumThumbnailAssetId).catch(() => '');
    if (url) coverUrls = { ...coverUrls, [album.id]: url };
  }

  function albumCoverAction(node: HTMLElement, album: Album) {
    const io = new IntersectionObserver((entries) => {
      if (entries[0].isIntersecting) { io.disconnect(); loadCover(album); }
    }, { rootMargin: '100px' });
    io.observe(node);
    return { destroy() { io.disconnect(); } };
  }
</script>

<div class="flex h-full flex-col">
  <div class="flex items-center gap-3 border-b border-[#45475a] px-6 py-3">
    {#if openAlbum}
      <button
        on:click={closeAlbumView}
        class="flex items-center gap-1 rounded-lg px-2 py-1.5 text-sm text-[#a6adc8] transition hover:bg-[#45475a]"
      >
        <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="15 18 9 12 15 6"/>
        </svg>
        Albums
      </button>
      <span class="text-[#585b70]">/</span>
      <span class="text-sm font-medium text-[#cdd6f4]">{openAlbum.albumName}</span>
      <span class="ml-1 text-xs text-[#585b70]">({albumAssets.length})</span>
    {:else}
      <div class="flex gap-2">
        <button
          on:click={() => (view = 'photos')}
          class="rounded-lg px-4 py-1.5 text-sm font-medium transition {view === 'photos'
            ? 'bg-[#4250af] text-white'
            : 'text-[#a6adc8] hover:bg-[#45475a]'}"
        >Photos</button>
        <button
          on:click={() => (view = 'albums')}
          class="rounded-lg px-4 py-1.5 text-sm font-medium transition {view === 'albums'
            ? 'bg-[#4250af] text-white'
            : 'text-[#a6adc8] hover:bg-[#45475a]'}"
        >Albums</button>
      </div>
    {/if}

    <div class="ml-auto flex items-center gap-2">
      {#if pendingUploads > 0}
        <span class="text-xs text-[#4250af] font-medium">{pendingUploads} new</span>
      {/if}
      <button
        on:click={() => openAlbum ? openAlbumView(openAlbum) : loadData()}
        disabled={loading || albumLoading}
        class="relative rounded-lg p-2 text-[#a6adc8] transition hover:bg-[#45475a] disabled:opacity-50"
        title="Refresh"
      >
        {#if pendingUploads > 0}
          <span class="absolute right-1 top-1 h-2 w-2 rounded-full bg-[#4250af]" />
        {/if}
        <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4 {loading || albumLoading ? 'animate-spin' : ''}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h5M20 20v-5h-5M4 9a8 8 0 0113.66-4M20 15a8 8 0 01-13.66 4"/>
        </svg>
      </button>
    </div>
  </div>

  <div class="flex flex-1 overflow-hidden">
    <div
      bind:this={scrollEl}
      on:scroll={scrollWatcher}
      class="relative flex-1 overflow-y-auto p-4 pr-2"
    >
    {#if openAlbum}
      {#if albumError}
        <div class="rounded-lg bg-red-900/40 border border-red-500/50 px-4 py-3 text-sm text-red-300">{albumError}</div>
      {:else if albumLoading}
        <div class="flex h-40 items-center justify-center text-[#585b70]">
          <svg class="h-8 w-8 animate-spin" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z"/>
          </svg>
        </div>
      {:else if albumAssets.length === 0}
        <div class="flex h-40 flex-col items-center justify-center text-[#585b70]">
          <p>This album is empty</p>
        </div>
      {:else}
        <PhotoGrid assets={albumAssets} on:select={(e) => (lightboxIndex = e.detail)} />
      {/if}

    {:else if view === 'photos'}
      {#if error}
        <div class="rounded-lg bg-red-900/40 border border-red-500/50 px-4 py-3 text-sm text-red-300">{error}</div>
      {:else if allAssets.length === 0 && !loading}
        <div class="flex h-full flex-col items-center justify-center text-[#585b70]">
          <svg xmlns="http://www.w3.org/2000/svg" class="mb-3 h-16 w-16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1">
            <rect x="3" y="3" width="18" height="18" rx="2" ry="2"/>
            <circle cx="8.5" cy="8.5" r="1.5"/>
            <polyline points="21 15 16 10 5 21"/>
          </svg>
          <p>No photos found on your Immich server</p>
        </div>
      {:else}
        {#key gridKey}
          {#each groups as group (group.key)}
            <div class="mb-6">
              <div class="mb-2 text-sm font-semibold text-[#cdd6f4]" data-datekey={group.key}>{group.label}</div>
              <PhotoGrid assets={group.assets} offset={group.start} on:select={(e) => (lightboxIndex = e.detail)} />
            </div>
          {/each}
        {/key}
      {/if}

    {:else}
      {#if error}
        <div class="rounded-lg bg-red-900/40 border border-red-500/50 px-4 py-3 text-sm text-red-300">{error}</div>
      {:else if albums.length === 0 && !loading}
        <div class="flex h-full flex-col items-center justify-center text-[#585b70]">
          <p>No albums found</p>
        </div>
      {:else}
        <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
          {#each albums as album (album.id)}
            <!-- svelte-ignore a11y-click-events-have-key-events -->
            <div
              class="cursor-pointer rounded-xl bg-[#313244] p-0 overflow-hidden transition hover:ring-2 hover:ring-[#4250af]"
              use:albumCoverAction={album}
              on:click={() => openAlbumView(album)}
            >
              <div class="relative aspect-square w-full overflow-hidden bg-[#1e1e2e]">
                {#if coverUrls[album.id]}
                  <img
                    src={coverUrls[album.id]}
                    alt={album.albumName}
                    class="h-full w-full object-cover transition duration-200 hover:scale-105"
                  />
                {:else}
                  <div class="flex h-full w-full items-center justify-center">
                    <svg xmlns="http://www.w3.org/2000/svg" class="h-12 w-12 text-[#45475a]" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M22 16V4a2 2 0 00-2-2H8a2 2 0 00-2 2v12a2 2 0 002 2h12a2 2 0 002-2zM11 12l2.03 2.71L16 11l4 5H8l3-4zM2 6v14a2 2 0 002 2h14v-2H4V6H2z"/>
                    </svg>
                  </div>
                {/if}
                <div class="absolute bottom-1.5 right-1.5 rounded-full bg-black/60 px-2 py-0.5 text-xs text-white">
                  {album.assetCount}
                </div>
              </div>
              <div class="px-3 py-2">
                <p class="truncate text-sm font-medium text-[#cdd6f4]">{album.albumName}</p>
                {#if album.description}
                  <p class="truncate text-xs text-[#585b70]">{album.description}</p>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    {/if}
    </div>

    {#if view === 'photos' && !openAlbum && groups.length > 0}
      <div class="w-12 flex-shrink-0 overflow-y-auto py-4">
        <TimelineRail {timeline} {activeYear} {activeMonth} onSelect={selectTimeline} />
      </div>
    {/if}
  </div>
</div>

{#if lightboxOpen}
  <Lightbox
    assets={lightboxAssets}
    bind:index={lightboxIndex}
    onClose={() => (lightboxIndex = -1)}
  />
{/if}

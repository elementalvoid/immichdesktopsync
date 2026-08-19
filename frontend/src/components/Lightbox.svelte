<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { getThumbUrl } from '../lib/thumbCache';
  import { DownloadAsset, GetStreamPort, GetAssetInfo } from '../../bindings/immich-desktop-sync/app';

  interface ExifInfo {
    fileSizeInByte?: number;
    exifImageWidth?: number;
    exifImageHeight?: number;
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
  }

  interface LightboxAsset {
    id: string;
    type: string;
    originalPath: string;
    originalFileName?: string;
    localDateTime?: string;
    fileCreatedAt?: string;
    fileModifiedAt?: string;
    duration?: string;
    isFavorite?: boolean;
    exifInfo?: ExifInfo;
  }

  export let assets: LightboxAsset[] = [];
  export let index: number = 0;
  export let onClose: () => void = () => {};

  $: asset = assets[index];
  $: isVideo = asset?.type === 'VIDEO';
  $: filename = asset?.originalFileName
    || asset?.originalPath?.replace(/\\/g, '/').split('/').pop()
    || '';
  $: dateStr = formatDate(asset?.localDateTime ?? asset?.fileCreatedAt ?? '');

  // ── Images ──────────────────────────────────────────────────────────────────
  let imgPromise: Promise<string> = Promise.resolve('');
  $: if (asset && !isVideo) imgPromise = getThumbUrl(asset.id, 'preview');

  $: {
    if (index > 0) getThumbUrl(assets[index - 1].id, 'preview');
    if (index < assets.length - 1) getThumbUrl(assets[index + 1].id, 'preview');
  }

  // ── Video streaming ──────────────────────────────────────────────────────────
  let streamPort = 0;
  $: streamUrl = (isVideo && streamPort && asset)
    ? `http://127.0.0.1:${streamPort}/video/${asset.id}`
    : '';

  // ── Zoom ─────────────────────────────────────────────────────────────────────
  let scale = 1;
  let panX = 0;
  let panY = 0;
  let isDragging = false;
  let didDrag = false;
  let dragStartX = 0;
  let dragStartY = 0;
  let dragStartPanX = 0;
  let dragStartPanY = 0;

  $: zoomPct = Math.round(scale * 100);

  function resetZoom() { scale = 1; panX = 0; panY = 0; }

  function zoomBy(factor: number, cx = 0, cy = 0) {
    const newScale = Math.max(0.5, Math.min(10, scale * factor));
    panX = cx - (cx - panX) * (newScale / scale);
    panY = cy - (cy - panY) * (newScale / scale);
    scale = newScale;
  }

  function onWheel(e: WheelEvent) {
    e.preventDefault();
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const cx = e.clientX - rect.left - rect.width / 2;
    const cy = e.clientY - rect.top - rect.height / 2;
    const factor = e.deltaY < 0 ? 1.15 : 1 / 1.15;
    zoomBy(factor, cx, cy);
  }

  function onImgMouseDown(e: MouseEvent) {
    if (e.button !== 0) return;
    isDragging = true;
    didDrag = false;
    dragStartX = e.clientX;
    dragStartY = e.clientY;
    dragStartPanX = panX;
    dragStartPanY = panY;
    e.preventDefault();
  }

  function onImgMouseMove(e: MouseEvent) {
    if (!isDragging) return;
    const dx = e.clientX - dragStartX;
    const dy = e.clientY - dragStartY;
    if (Math.abs(dx) > 2 || Math.abs(dy) > 2) didDrag = true;
    panX = dragStartPanX + dx;
    panY = dragStartPanY + dy;
  }

  function onImgMouseUp() { isDragging = false; }

  function onImgDblClick() {
    if (scale !== 1) resetZoom(); else zoomBy(2);
  }

  // ── Navigation ───────────────────────────────────────────────────────────────
  function prev() { if (index > 0) { resetZoom(); index--; } }
  function next() { if (index < assets.length - 1) { resetZoom(); index++; } }

  // ── Info panel ───────────────────────────────────────────────────────────────
  let showInfo = false;
  let richAsset: LightboxAsset | null = null;
  let infoLoading = false;

  // Reset when navigating to a different asset
  $: { asset; showInfo = false; richAsset = null; }

  // Fetch full EXIF via the dedicated endpoint when panel opens
  $: if (showInfo && asset && !richAsset && !infoLoading) fetchAssetInfo();

  async function fetchAssetInfo() {
    if (!asset) return;
    infoLoading = true;
    try {
      richAsset = await GetAssetInfo(asset.id) as LightboxAsset;
    } catch { /* show what we have */ } finally {
      infoLoading = false;
    }
  }

  // Use richAsset's exifInfo when available, fall back to what the list gave us
  $: infoAsset = richAsset ?? asset;

  function formatDate(iso: string): string {
    if (!iso) return '';
    try {
      return new Date(iso).toLocaleDateString(undefined, {
        year: 'numeric', month: 'long', day: 'numeric',
      });
    } catch { return ''; }
  }

  function formatDateTime(iso: string): string {
    if (!iso) return '';
    try {
      return new Date(iso).toLocaleString(undefined, {
        year: 'numeric', month: 'short', day: 'numeric',
        hour: '2-digit', minute: '2-digit',
      });
    } catch { return ''; }
  }

  function formatSize(bytes?: number): string {
    if (!bytes) return '';
    if (bytes >= 1_073_741_824) return (bytes / 1_073_741_824).toFixed(1) + ' GB';
    if (bytes >= 1_048_576)     return (bytes / 1_048_576).toFixed(1) + ' MB';
    if (bytes >= 1_024)         return (bytes / 1_024).toFixed(0) + ' KB';
    return bytes + ' B';
  }

  function formatDuration(dur?: string): string {
    if (!dur) return '';
    // Immich format: "0:00:15.123456" → strip microseconds, trim leading zeros
    const parts = dur.replace(/\.\d+$/, '').split(':');
    if (parts.length === 3) {
      const [h, m, s] = parts;
      return h === '0' ? `${m}:${s}` : `${h}:${m}:${s}`;
    }
    return dur;
  }

  function formatExposure(exp?: string): string {
    if (!exp) return '';
    // Immich stores exposureTime as a fraction string e.g. "1/17" or "1/1000".
    // For long exposures it may be a decimal like "2" or "0.5".
    if (exp.includes('/')) return `${exp} s`;
    const n = parseFloat(exp);
    if (isNaN(n)) return exp;
    if (n >= 1) return `${n} s`;
    const denom = Math.round(1 / n);
    return `1/${denom} s`;
  }

  function formatGPS(lat?: number | null, lon?: number | null): string {
    if (lat == null || lon == null) return '';
    const latStr = Math.abs(lat).toFixed(5) + (lat >= 0 ? '°N' : '°S');
    const lonStr = Math.abs(lon).toFixed(5) + (lon >= 0 ? '°E' : '°W');
    return `${latStr}, ${lonStr}`;
  }

  // Derived info fields — use richAsset (full EXIF) if loaded, else fall back
  $: exif = infoAsset?.exifInfo;
  $: camera = [exif?.make, exif?.model].filter(Boolean).join(' ');
  $: resolution = (exif?.exifImageWidth && exif?.exifImageHeight)
    ? `${exif.exifImageWidth} × ${exif.exifImageHeight}`
    : '';
  $: location = [exif?.city, exif?.state, exif?.country].filter(Boolean).join(', ');
  $: gps = formatGPS(exif?.latitude, exif?.longitude);

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') { if (showInfo) showInfo = false; else onClose(); }
    else if (e.key === 'ArrowLeft') prev();
    else if (e.key === 'ArrowRight') next();
    else if (e.key === '+' || e.key === '=') zoomBy(1.25);
    else if (e.key === '-') zoomBy(1 / 1.25);
    else if (e.key === '0') resetZoom();
    else if (e.key === 'i' || e.key === 'I') showInfo = !showInfo;
  }

  onMount(async () => {
    window.addEventListener('keydown', onKey);
    streamPort = await GetStreamPort();
  });
  onDestroy(() => window.removeEventListener('keydown', onKey));

  // ── Download ─────────────────────────────────────────────────────────────────
  let downloading = false;
  let downloadMsg = '';
  $: { asset; downloadMsg = ''; }

  async function handleDownload() {
    if (!asset) return;
    downloading = true;
    downloadMsg = '';
    try {
      await DownloadAsset(asset.id, filename || `${asset.id}.bin`);
      downloadMsg = 'Saved';
      setTimeout(() => { downloadMsg = ''; }, 3000);
    } catch (e: any) {
      downloadMsg = String(e).replace(/^Error: /, '');
      setTimeout(() => { downloadMsg = ''; }, 5000);
    } finally {
      downloading = false;
    }
  }
</script>

<!-- svelte-ignore a11y-click-events-have-key-events -->
<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/90"
  on:click|self={onClose}
>
  <!-- Top-right toolbar -->
  <div class="absolute right-4 top-4 z-10 flex items-center gap-2">
    {#if downloadMsg}
      <span class="text-xs {downloadMsg === 'Saved' ? 'text-green-400' : 'text-red-400'}">{downloadMsg}</span>
    {/if}

    <!-- Info toggle -->
    <button
      class="rounded-full p-2 text-white transition {showInfo ? 'bg-white/25' : 'bg-black/50 hover:bg-white/20'}"
      on:click={() => showInfo = !showInfo} title="Info (I)"
    >
      <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <circle cx="12" cy="12" r="10"/>
        <path d="M12 16v-4"/>
        <path d="M12 8h.01"/>
      </svg>
    </button>

    <!-- Download -->
    <button
      class="rounded-full bg-black/50 p-2 text-white transition hover:bg-white/20 disabled:opacity-40"
      on:click={handleDownload} disabled={downloading} title="Download original"
    >
      {#if downloading}
        <svg class="h-5 w-5 animate-spin" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z"/>
        </svg>
      {:else}
        <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3"/>
        </svg>
      {/if}
    </button>

    <!-- Close -->
    <button
      class="rounded-full bg-black/50 p-2 text-white transition hover:bg-white/20"
      on:click={onClose} title="Close (Esc)"
    >
      <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
      </svg>
    </button>
  </div>

  <!-- Prev -->
  {#if index > 0}
    <button
      class="absolute left-3 top-1/2 z-10 -translate-y-1/2 rounded-full bg-black/50 p-3 text-white transition hover:bg-white/20"
      on:click={prev} title="Previous (←)"
    >
      <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
        <polyline points="15 18 9 12 15 6"/>
      </svg>
    </button>
  {/if}

  <!-- Main content + Info panel side-by-side -->
  <div class="flex h-full w-full overflow-hidden pt-12 pb-14">

    <!-- Media area -->
    <div class="flex flex-1 items-center justify-center overflow-hidden px-14">
      {#if isVideo}
        <!-- svelte-ignore a11y-media-has-caption -->
        <video
          src={streamUrl}
          controls
          autoplay
          class="max-h-full max-w-full rounded shadow-2xl"
          on:click|stopPropagation
        ></video>

      {:else}
        {#await imgPromise}
          <div class="flex flex-col items-center gap-3 text-white/40">
            <svg class="h-10 w-10 animate-spin" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z"/>
            </svg>
            <span class="text-sm">Loading…</span>
          </div>
        {:then url}
          {#if url}
            <!-- svelte-ignore a11y-click-events-have-key-events -->
            <div
              class="h-full w-full flex items-center justify-center overflow-hidden"
              style="cursor: {isDragging ? 'grabbing' : scale > 1 ? 'grab' : 'default'}"
              on:wheel|preventDefault={onWheel}
              on:mousedown={onImgMouseDown}
              on:mousemove={onImgMouseMove}
              on:mouseup={onImgMouseUp}
              on:mouseleave={onImgMouseUp}
              on:dblclick={onImgDblClick}
              on:click|stopPropagation={() => {}}
            >
              <img
                src={url}
                alt={filename}
                class="max-h-full max-w-full rounded object-contain shadow-2xl select-none"
                style="transform: translate({panX}px, {panY}px) scale({scale}); transition: {isDragging ? 'none' : 'transform 0.15s ease-out'};"
                draggable="false"
              />
            </div>
          {:else}
            <div class="flex flex-col items-center gap-2 text-white/40">
              <svg xmlns="http://www.w3.org/2000/svg" class="h-16 w-16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1">
                <rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>
              </svg>
              <span class="text-sm">Could not load image</span>
            </div>
          {/if}
        {/await}
      {/if}
    </div>

    <!-- Info panel -->
    {#if showInfo}
      <!-- svelte-ignore a11y-click-events-have-key-events -->
      <div
        class="w-72 flex-shrink-0 overflow-y-auto border-l border-white/10 bg-black/70 backdrop-blur-md"
        on:click|stopPropagation={() => {}}
      >
        <div class="space-y-5 px-5 py-5 text-sm text-white/80">

          {#if infoLoading}
            <div class="flex justify-center py-8">
              <svg class="h-6 w-6 animate-spin text-white/30" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
                <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
                <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z"/>
              </svg>
            </div>
          {:else}

          <!-- File -->
          <section>
            <h3 class="mb-2 text-[10px] font-semibold uppercase tracking-widest text-white/35">File</h3>
            <p class="break-all font-medium text-white leading-snug">{filename}</p>
            {#if exif?.fileSizeInByte}
              <p class="mt-1 text-white/50">{formatSize(exif.fileSizeInByte)}</p>
            {/if}
            {#if isVideo && infoAsset?.duration}
              <p class="mt-0.5 text-white/50">{formatDuration(infoAsset.duration)}</p>
            {/if}
          </section>

          <!-- Date -->
          {#if infoAsset?.localDateTime || infoAsset?.fileCreatedAt}
            <section>
              <h3 class="mb-2 text-[10px] font-semibold uppercase tracking-widest text-white/35">Date</h3>
              <p>{formatDateTime(infoAsset.localDateTime ?? infoAsset.fileCreatedAt ?? '')}</p>
            </section>
          {/if}

          <!-- Dimensions -->
          {#if resolution}
            <section>
              <h3 class="mb-2 text-[10px] font-semibold uppercase tracking-widest text-white/35">Dimensions</h3>
              <p>{resolution}</p>
            </section>
          {/if}

          <!-- Camera -->
          {#if camera || exif?.lensModel || exif?.fNumber || exif?.focalLength || exif?.iso || exif?.exposureTime}
            <section>
              <h3 class="mb-2 text-[10px] font-semibold uppercase tracking-widest text-white/35">Camera</h3>
              <div class="space-y-1">
                {#if camera}<p class="font-medium text-white">{camera}</p>{/if}
                {#if exif?.lensModel}<p class="text-white/50 text-xs">{exif.lensModel}</p>{/if}
                {#if exif?.exposureTime}<p class="text-white/70">{formatExposure(exif.exposureTime)}</p>{/if}
                {#if exif?.iso}<p class="text-white/70">ISO {exif.iso}</p>{/if}
                {#if exif?.fNumber}<p class="text-white/70">ƒ/{exif.fNumber}</p>{/if}
                {#if exif?.focalLength}<p class="text-white/70">{exif.focalLength} mm</p>{/if}
              </div>
            </section>
          {/if}

          <!-- Location -->
          {#if location || gps}
            <section>
              <h3 class="mb-2 text-[10px] font-semibold uppercase tracking-widest text-white/35">Location</h3>
              {#if location}<p>{location}</p>{/if}
              {#if gps}<p class="mt-0.5 text-xs text-white/40">{gps}</p>{/if}
            </section>
          {/if}

          <!-- Description -->
          {#if exif?.description}
            <section>
              <h3 class="mb-2 text-[10px] font-semibold uppercase tracking-widest text-white/35">Description</h3>
              <p class="italic text-white/60">{exif.description}</p>
            </section>
          {/if}

          {/if}
        </div>
      </div>
    {/if}
  </div>

  <!-- Next -->
  {#if index < assets.length - 1}
    <button
      class="absolute right-3 top-1/2 z-10 -translate-y-1/2 rounded-full bg-black/50 p-3 text-white transition hover:bg-white/20"
      on:click={next} title="Next (→)"
    >
      <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
        <polyline points="9 18 15 12 9 6"/>
      </svg>
    </button>
  {/if}

  <!-- Footer -->
  <div class="absolute bottom-0 left-0 right-0 flex items-center justify-between bg-gradient-to-t from-black/70 to-transparent px-6 py-4">
    <div class="min-w-0">
      <p class="truncate text-sm font-medium text-white">{filename}</p>
      {#if dateStr}<p class="text-xs text-white/60">{dateStr}</p>{/if}
    </div>
    <div class="ml-4 flex flex-shrink-0 items-center gap-3">
      {#if !isVideo}
        <div class="flex items-center gap-1 rounded bg-black/50 px-1 py-0.5">
          <button
            class="rounded p-1 text-white/60 transition hover:text-white hover:bg-white/10"
            on:click={() => zoomBy(1/1.25)} title="Zoom out (−)"
          >
            <svg xmlns="http://www.w3.org/2000/svg" class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
              <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/><line x1="8" y1="11" x2="14" y2="11"/>
            </svg>
          </button>
          <button
            class="min-w-[2.5rem] text-center text-xs text-white/50 hover:text-white transition"
            on:click={resetZoom} title="Reset zoom (0)"
          >{zoomPct}%</button>
          <button
            class="rounded p-1 text-white/60 transition hover:text-white hover:bg-white/10"
            on:click={() => zoomBy(1.25)} title="Zoom in (+)"
          >
            <svg xmlns="http://www.w3.org/2000/svg" class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
              <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/><line x1="11" y1="8" x2="11" y2="14"/><line x1="8" y1="11" x2="14" y2="11"/>
            </svg>
          </button>
        </div>
      {/if}
      {#if isVideo}
        <span class="flex items-center gap-1 rounded bg-white/10 px-2 py-0.5 text-xs text-white/70">
          <svg xmlns="http://www.w3.org/2000/svg" class="h-3 w-3" viewBox="0 0 24 24" fill="currentColor"><polygon points="5 3 19 12 5 21 5 3"/></svg>
          VIDEO
        </span>
      {/if}
      <span class="text-xs text-white/40">{index + 1} / {assets.length}</span>
    </div>
  </div>
</div>

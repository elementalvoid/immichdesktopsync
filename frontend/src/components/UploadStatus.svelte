<script lang="ts">
  import { uploads } from '../stores/uploads';
  import type { UploadQueueItem } from '../stores/uploads';
  import { RetryFailed } from '../../bindings/immich-desktop-sync/app';

  $: queue = $uploads.queue;
  $: pending = queue.filter(i => i.status === 'pending').length;
  $: uploading = queue.filter(i => i.status === 'uploading').length;
  $: failed = queue.filter(i => i.status === 'failed').length;
  $: total = queue.length;

  let retrying = false;

  async function handleRetryFailed() {
    retrying = true;
    try {
      await RetryFailed();
      await uploads.refresh();
    } finally {
      retrying = false;
    }
  }

  function statusColor(status: UploadQueueItem['status']) {
    switch (status) {
      case 'uploading': return 'text-blue-400';
      case 'done':      return 'text-green-400';
      case 'failed':    return 'text-red-400';
      default:          return 'text-[#a6adc8]';
    }
  }

  function basename(path: string) {
    return path.replace(/\\/g, '/').split('/').pop() ?? path;
  }
</script>

<div class="flex h-full flex-col">
  <!-- Summary bar -->
  <div class="flex flex-wrap items-center gap-3 border-b border-[#45475a] px-4 py-3 text-sm">
    <span class="text-[#a6adc8]">Total: <strong class="text-[#cdd6f4]">{total}</strong></span>
    {#if uploading > 0}
      <span class="text-blue-400">Uploading: <strong>{uploading}</strong></span>
    {/if}
    {#if pending > 0}
      <span class="text-[#a6adc8]">Pending: <strong class="text-[#cdd6f4]">{pending}</strong></span>
    {/if}
    {#if failed > 0}
      <span class="text-red-400">Failed: <strong>{failed}</strong></span>
    {/if}
    {#if failed > 0}
      <button
        on:click={handleRetryFailed}
        disabled={retrying}
        class="ml-auto rounded px-2 py-0.5 text-xs bg-[#313244] text-[#a6adc8] hover:bg-[#45475a] transition disabled:opacity-50"
      >
        {retrying ? 'Retrying…' : 'Retry All'}
      </button>
    {/if}
  </div>

  <!-- Queue list -->
  <div class="flex-1 overflow-y-auto">
    {#if queue.length === 0}
      <div class="flex h-full items-center justify-center text-sm text-[#585b70]">
        Upload queue is empty
      </div>
    {:else}
      <ul class="divide-y divide-[#45475a]">
        {#each queue as item (item.id)}
          <li class="px-4 py-2">
            <div class="flex items-center gap-3">
              <span class="w-2 h-2 rounded-full flex-shrink-0 {item.status === 'uploading' ? 'bg-blue-400 animate-pulse' : item.status === 'failed' ? 'bg-red-400' : 'bg-[#585b70]'}" />
              <span class="flex-1 truncate text-sm text-[#cdd6f4]" title={item.filePath}>
                {basename(item.filePath)}
              </span>
              <span class="text-xs {statusColor(item.status)} flex-shrink-0 capitalize">
                {item.status}{#if item.retryCount > 0}<span class="text-[#585b70]"> ×{item.retryCount}</span>{/if}
              </span>
            </div>
            {#if item.status === 'failed' && item.error}
              <p class="mt-1 ml-5 truncate text-xs text-red-400/80" title={item.error}>{item.error}</p>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>

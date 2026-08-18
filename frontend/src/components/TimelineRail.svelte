<script lang="ts">
  import type { TimelineYear } from '../lib/gallery';

  export let timeline: TimelineYear[] = [];
  export let activeYear: number | null = null;
  export let activeMonth: number | null = null;

  export function onSelect(year: number, month: number): void {}

  function monthName(m: number): string {
    return new Date(2000, m, 1).toLocaleString(undefined, { month: 'long' });
  }
</script>

<div class="sticky top-4 flex min-h-0 flex-col items-end gap-1 pr-1 text-right">
  {#each timeline as yr (yr.year)}
    <!-- svelte-ignore a11y-click-events-have-key-events -->
    <div
      class="flex cursor-pointer flex-col items-end gap-1 rounded px-1 py-0.5 transition hover:bg-[#313244]"
      on:click={() => onSelect(yr.year, yr.months[0])}
    >
      <span
        class="text-xs font-semibold tabular-nums leading-none {yr.year === activeYear ? 'text-[#cdd6f4]' : 'text-[#6c7086]'}"
      >{yr.year}</span>
      <div class="flex flex-wrap items-center justify-end gap-1">
        {#each yr.months as m (m)}
          <!-- svelte-ignore a11y-click-events-have-key-events -->
          <span
            class="h-1.5 w-1.5 cursor-pointer rounded-full transition {yr.year === activeYear && m === activeMonth ? 'bg-[#89b4fa] scale-125' : 'bg-[#585b70] hover:bg-[#a6adc8]'}"
            title={monthName(m)}
            on:click|stopPropagation={() => onSelect(yr.year, m)}
          ></span>
        {/each}
      </div>
    </div>
  {/each}
</div>

// Pure logic for the timeline gallery: date grouping, timeline markers, and
// justified (aspect-ratio-aware) row layout. No Svelte/dependency imports so it
// can be run with `node` for a self-check (see tests/gallery.selfcheck.mjs).

export interface RawExif {
  exifImageWidth?: number;
  exifImageHeight?: number;
}

export interface RawAsset {
  id: string;
  type?: string;
  localDateTime?: string;
  fileCreatedAt?: string;
  exifInfo?: RawExif;
}

export interface Group<T extends RawAsset = RawAsset> {
  key: string; // yyyy-m-d or "__nodate"
  label: string;
  date: Date | null;
  assets: T[];
  start: number; // index of first asset within the flattened display order
}

export interface TimelineYear {
  year: number;
  months: number[]; // 0-11, descending (newest month first)
}

// ── Dates ────────────────────────────────────────────────────────────────────
export function parseDate(a: RawAsset): Date | null {
  const iso = a.localDateTime || a.fileCreatedAt;
  if (!iso) return null;
  const d = new Date(iso);
  return isNaN(d.getTime()) ? null : d;
}

function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

export function dayLabel(d: Date): string {
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const day = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  const diff = Math.round((today.getTime() - day.getTime()) / 86_400_000);
  if (diff === 0) return 'Today';
  if (diff === 1) return 'Yesterday';
  const opts: Intl.DateTimeFormatOptions =
    d.getFullYear() === now.getFullYear()
      ? { weekday: 'short', month: 'short', day: 'numeric' }
      : { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric' };
  return d.toLocaleDateString(undefined, opts);
}

export function groupByDate<T extends RawAsset>(assets: T[]): Group<T>[] {
  const dated = assets
    .map((a) => ({ a, d: parseDate(a) }))
    .sort((x, y) => {
      if (x.d && y.d) return y.d.getTime() - x.d.getTime();
      if (x.d) return -1; // dated items first (descending); no-date sinks to end
      if (y.d) return 1;
      return 0;
    });

  const groups: Group<T>[] = [];
  let current: Group<T> | null = null;
  let globalIdx = 0;
  for (const { a, d } of dated) {
    if (d) {
      const key = `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
      if (!current || current.key !== key) {
        current = { key, label: dayLabel(d), date: d, assets: [], start: globalIdx };
        groups.push(current);
      }
    } else {
      if (!current || current.key !== '__nodate') {
        current = { key: '__nodate', label: 'No Date', date: null, assets: [], start: globalIdx };
        groups.push(current);
      }
    }
    current.assets.push(a);
    globalIdx++;
  }
  return groups;
}

export function buildTimeline(groups: Group[]): TimelineYear[] {
  const byYear = new Map<number, Set<number>>();
  for (const g of groups) {
    if (!g.date) continue;
    const y = g.date.getFullYear();
    if (!byYear.has(y)) byYear.set(y, new Set());
    byYear.get(y)!.add(g.date.getMonth());
  }
  return [...byYear.entries()]
    .sort((a, b) => b[0] - a[0])
    .map(([year, months]) => ({ year, months: [...months].sort((a, b) => b - a) }));
}

// ── Fixed-height layout ──────────────────────────────────────────────────────
// The photo grid renders each thumbnail at a fixed height; width follows the
// image's innate aspect ratio (see PhotoGrid.svelte), so no precomputed widths
// are needed.
export const ROW_HEIGHT = 200;

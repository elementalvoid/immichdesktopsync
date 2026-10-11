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

// Append one page of assets, skipping ids already loaded. A library that
// changes during paging can repeat an asset across pages, so dedupe by id.
export function appendAssets<T extends RawAsset>(current: T[], incoming: T[]): T[] {
  if (incoming.length === 0) return current;
  const seen = new Set(current.map((a) => a.id));
  const fresh = incoming.filter((a) => !seen.has(a.id));
  return fresh.length === 0 ? current : [...current, ...fresh];
}

// Order date groups newest first, matching groupByDate. No-date groups sink
// to the end.
function compareGroupDates(a: Group, b: Group): number {
  if (a.date && b.date) return b.date.getTime() - a.date.getTime();
  if (a.date) return -1;
  if (b.date) return 1;
  return 0;
}

// Merge one page of assets into already-grouped assets. Only the new page is
// sorted; the existing groups are reused (as shallow copies) so the whole
// library is not re-sorted on every page. Pages arrive newest first, so assets
// within a day keep append order.
export function mergeGroups<T extends RawAsset>(groups: Group<T>[], incoming: T[]): Group<T>[] {
  if (incoming.length === 0) return groups;
  const byKey = new Map<string, Group<T>>();
  for (const g of groups) {
    byKey.set(g.key, { ...g }); // copy, so a later start recompute is not shared
  }
  for (const g of groupByDate(incoming)) {
    const existing = byKey.get(g.key);
    if (existing) {
      existing.assets = [...existing.assets, ...g.assets];
    } else {
      byKey.set(g.key, g);
    }
  }
  const merged = [...byKey.values()].sort(compareGroupDates);
  let start = 0;
  for (const g of merged) {
    g.start = start;
    start += g.assets.length;
  }
  return merged;
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

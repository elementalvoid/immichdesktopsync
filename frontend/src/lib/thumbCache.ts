import { GetThumbnail } from '../../wailsjs/go/main/App';

export type ThumbSize = 'thumbnail' | 'preview';

// cache is keyed by size so the small grid thumbs and the larger preview used
// when opening a photo don't evict each other.
const cache = new Map<string, string>();
const inflight = new Map<string, Promise<string>>();

export async function getThumbUrl(assetId: string, size: ThumbSize = 'thumbnail'): Promise<string> {
  const cacheKey = `${size}:${assetId}`;
  if (cache.has(cacheKey)) return cache.get(cacheKey)!;
  if (inflight.has(cacheKey)) return inflight.get(cacheKey)!;

  const promise = (async () => {
    const b64 = await GetThumbnail(assetId, size) as unknown as string;
    if (!b64) return '';
    const binary = atob(b64);
    const buf = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) buf[i] = binary.charCodeAt(i);
    const url = URL.createObjectURL(new Blob([buf]));
    cache.set(cacheKey, url);
    inflight.delete(cacheKey);
    return url;
  })();

  inflight.set(cacheKey, promise);
  return promise;
}
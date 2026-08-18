# Feature Request: Timeline Gallery with Dynamic Date Navigation and Organic Photo Grid

## Summary

Enhance the Gallery view to resemble the Immich web gallery:

1. Group photos by their capture date.
2. Add a right-side timeline navigator based entirely on the user’s actual Immich gallery.
3. Replace the fixed square thumbnail grid with a more organic, aspect-ratio-aware layout.

Reference screenshot: `Screenshot 2026-08-17 230458.png`

## Requirements

### 1. Date-based grouping

In the Photos view:

- Group assets by calendar date.
- Use `localDateTime` as the primary date source.
- Fall back to `fileCreatedAt` if `localDateTime` is missing or invalid.
- Sort groups newest first.
- Sort assets within each group newest first.
- Display a date heading for each group, for example:
  - `Today`
  - `Yesterday`
  - `Sat, Aug 8`
  - `Fri, Jul 24`
  - `Sun, Jul 19`
- Avoid displaying a year in the heading when the asset is from the current year, matching the Immich-style presentation.
- Clearly separate each date group vertically.

Date parsing must be defensive. Assets with missing or invalid dates should still render in a fallback group rather than breaking the Gallery.

### 2. Dynamic right-side timeline navigator

Add a fixed or sticky vertical date navigator on the right side of the Photos Gallery.

The navigator must be generated dynamically from the loaded gallery data:

- Do not hardcode years, months, or dates from the reference screenshot.
- Show only years/months that actually exist in the user’s Immich gallery.
- Include visual markers for available timeline sections.
- Use larger labels for years.
- Use smaller dots or markers for available months/date ranges.
- Highlight the currently visible timeline position while scrolling.
- Allow clicking a year/month marker to scroll to the corresponding gallery section.
- Support dragging or scrubbing the timeline when practical.
- The navigator should remain usable when the user has:
  - only one year of photos;
  - sparse dates;
  - many years of photos;
  - a large gallery;
  - no photos.

The timeline should be based on the same grouped data used to render the gallery, so navigation and visible sections cannot drift apart.

Suggested behavior:

- Calculate each date group’s scroll position after rendering.
- Associate timeline markers with the nearest rendered date group.
- Clicking a marker scrolls that group into view.
- Use `IntersectionObserver` or scroll position tracking to update the active marker.
- Preserve the existing scroll container rather than introducing a second page-level scrollbar.

### 3. Organic aspect-ratio-aware photo grid

Replace the current fixed square tile layout in `PhotoGrid.svelte`.

The new layout should:

- Preserve each source image’s aspect ratio where possible.
- Avoid cropping images into square tiles.
- Arrange neighboring images into visually balanced rows.
- Use a target row height rather than forcing all tiles to the same width or height.
- Allow the final row to use a simpler layout when it cannot be fully justified.
- Keep consistent small gaps between images.
- Ensure very wide or very tall images do not dominate the layout.
- Maintain responsive behavior across window sizes.
- Keep lazy thumbnail loading and the existing lightbox click behavior.

A justified-gallery-style layout is preferred:

- Calculate each asset’s aspect ratio from available metadata.
- Use `exifInfo.exifImageWidth / exifInfo.exifImageHeight` when available.
- Fall back to a `1:1` ratio when dimensions are unavailable.
- Build rows whose total aspect-ratio width fits the available container width.
- Scale images within each row to a target height.
- Apply reasonable minimum and maximum row heights.
- Use a fallback aspect ratio for videos or assets without EXIF dimensions.

Do not add a layout dependency unless the existing code cannot reasonably support this with CSS/TypeScript.

### 4. Existing functionality must remain intact

Preserve:

- Thumbnail lazy loading and concurrency limits.
- Video indicators.
- Album view.
- Album asset view.
- Lightbox navigation.
- Refresh behavior.
- Upload notifications.
- Error and empty states.

The new date grouping and timeline navigator should apply to the main Photos view first. Album views may continue using the existing grid unless extending the feature there is straightforward and does not complicate the implementation.

## Likely files to modify

- `frontend/src/pages/Gallery.svelte`
  - Date grouping.
  - Timeline data generation.
  - Scroll tracking and navigation.
- `frontend/src/components/PhotoGrid.svelte`
  - Aspect-ratio-aware layout.
  - Asset dimension support.
- Possibly `frontend/src/components/TimelineNavigator.svelte`
  - Only if a separate component makes the implementation clearer.
- Possibly `frontend/wailsjs/go/models.ts`
  - Only if additional asset dimension fields are required.

The backend likely already exposes enough information through `Asset.ExifInfo`, especially:

- `exifImageWidth`
- `exifImageHeight`
- `localDateTime`
- `fileCreatedAt`

Avoid backend changes unless the Immich response does not contain usable dimensions.

## Acceptance criteria

- Photos are visibly grouped by capture date.
- Groups are ordered newest to oldest.
- The date labels are derived from actual asset data.
- The right-side navigator contains no hardcoded example dates or years.
- Navigator markers reflect only timeline ranges present in the user’s gallery.
- Clicking a marker scrolls to the correct gallery section.
- The active timeline position updates while scrolling.
- Thumbnails preserve their source aspect ratio.
- The grid produces balanced, organic rows rather than fixed square tiles.
- Missing dates or dimensions do not crash the Gallery.
- Existing album, lightbox, upload, refresh, and lazy-loading behavior still works.
- Add focused tests or a small runnable check for:
  - date fallback/grouping;
  - timeline marker generation;
  - aspect-ratio fallback and row layout calculations.

---

## Implementation Plan & Task List

**Approach:** pure logic (grouping, timeline, justified layout) lives in a testable TS module under `frontend/src/lib/`; Svelte components only render and wire it up. No new dependencies, no backend changes (exif dimensions already exposed). Self-check run with `node` (v26 strips TS types).

### Key facts from the code
- `Gallery.svelte` keeps `allAssets` (photos) and `albumAssets`; `PhotoGrid` gets a flat list and dispatches a **global index** (`e.detail`) into it for the lightbox.
- Thumbnails come from `GetThumbnail` (base64 → blob URL) via `getThumbUrl()`, lazily with concurrency limits in `PhotoGrid`.
- Aspect ratios are already available: `exifInfo.exifImageWidth/Height`.
- Dates: `localDateTime` primary, `fileCreatedAt` fallback (both ISO strings).
- Lightbox indexes into the full asset array, so grouping must keep **global indices** valid.

### Task list
- [x] 1. `frontend/src/lib/gallery.ts` — pure helpers: `parseDate`, `aspectRatio`, `groupByDate` (defensive, dated desc, fallback "No Date" group), `dayLabel` (Today/Yesterday/current-year handling), `buildTimeline` (years + months from real data), `layoutRows` (justified gallery).
- [x] 2. Self-check `frontend/tests/gallery.selfcheck.mjs` — asserts grouping order, date fallback, timeline years/months, and justified layout widths/heights; run via `node`.
- [x] 3. `PhotoGrid.svelte` — replace square grid with justified rows: measure container width (ResizeObserver action), use `layoutRows`, size tiles by computed width/height, keep lazy-thumb concurrency + video indicator, dispatch `offset + localIndex` (global index preserved).
- [x] 4. `TimelineRail.svelte` — new sticky right rail: renders years (desc) with month dots from `buildTimeline`, highlights active year/month, click → callback.
- [x] 5. `Gallery.svelte` — sort/group `allAssets` via `groupByDate`; render per-group headers + `PhotoGrid` per group with correct `offset`; flatten display order for the lightbox; measure group scroll offsets and track active group on scroll; wire `TimelineRail` clicks to scroll into view. Gate to Photos view; leave Albums view on existing grid.
- [x] 6. Verify: `npm run check`, `npm run build`, `node tests/gallery.selfcheck.mjs`.

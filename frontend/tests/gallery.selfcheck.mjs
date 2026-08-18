// Self-check for the pure gallery/layout logic. Run: node tests/gallery.selfcheck.mjs
// (the .mjs imports .ts, which Node 26 strips types from natively.)
import {
  groupByDate,
  buildTimeline,
  parseDate,
} from '../src/lib/gallery.ts';

let failed = 0;
function assert(cond, msg) {
  if (!cond) {
    failed++;
    console.error('FAIL: ' + msg);
  } else {
    console.log('ok: ' + msg);
  }
}

// grouping: desc order, same-day merged, no-date fallback at end
const assets = [
  { id: 'a1', localDateTime: '2024-08-08T10:00:00Z' },
  { id: 'a2', localDateTime: '2024-08-08T09:00:00Z' },
  { id: 'a3', localDateTime: '2026-05-01T00:00:00Z' },
  { id: 'a4', fileCreatedAt: '2001-01-01T00:00:00Z', localDateTime: '' },
  { id: 'a5' }, // no date at all
  { id: 'a6', localDateTime: '2026-05-02T00:00:00Z' },
];
const groups = groupByDate(assets);
// structure (desc): a6(May2), a3(May1), a1+a2(Aug 8, merged), a4(2001), No Date
assert(groups.length === 5, 'group count = 5 (got ' + groups.length + ')');
assert(groups[0].assets[0].id === 'a6', 'newest group first contains a6');
assert(groups[1].assets[0].id === 'a3', 'second group contains a3');
assert(groups[2].assets.map((x) => x.id).join(',') === 'a1,a2', 'same-day merged to one group');
assert(groups[groups.length - 1].label === 'No Date', 'no-date group is last');
assert(groups[3].date !== null && groups[3].key !== '__nodate', 'valid fileCreatedAt is its own dated group: ' + groups[3].key);
assert(groups[3].date.getFullYear() >= 1990, 'old group year is sane: ' + groups[3].date.getFullYear());
assert(groups[4].assets.length === 1, 'only the truly dateless asset is in No Date');

// flatten matches display order; start offsets are correct
const flat = groups.flatMap((g) => g.assets);
assert(groups[2].start === 2, 'group start offset correct: ' + groups[2].start);
assert(flat[0].id === 'a6' && flat[1].id === 'a3', 'flattened display order');

// timeline: only real years/months, descending
const tl = buildTimeline(groups);
assert(tl[0].year === 2026 && tl[0].months.length >= 1, 'newest year 2026 present');
assert(tl.some((y) => y.year === 2024), '2024 present');
assert(tl.some((y) => y.year === groups[3].date.getFullYear()), 'old dated year present in timeline');
assert(!tl.some((y) => isNaN(y.year)), 'no NaN year from No Date group');

// parseDate fallback
assert(parseDate({ id: 'x', localDateTime: 'junk' }) === null, 'invalid date -> null');
assert(parseDate({ id: 'x', localDateTime: '', fileCreatedAt: '2020-01-01T00:00:00Z' }) !== null, 'fallback to fileCreatedAt');

console.log(failed === 0 ? '\nALL PASS' : `\n${failed} FAILURE(S)`);
process.exit(failed === 0 ? 0 : 1);

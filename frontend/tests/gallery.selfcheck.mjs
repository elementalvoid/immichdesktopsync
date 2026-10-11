// Self-check for the pure gallery/layout logic. Run: node tests/gallery.selfcheck.mjs
// (the .mjs imports .ts, which Node 26 strips types from natively.)
import {
  groupByDate,
  buildTimeline,
  parseDate,
  appendAssets,
  mergeGroups,
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

// paging: append keeps order, drops repeated ids, and skips the copy when empty
const page1 = [{ id: 'p1' }, { id: 'p2' }];
const page2 = [{ id: 'p3' }, { id: 'p2' }];
const merged = appendAssets(page1, page2);
assert(merged.map((a) => a.id).join(',') === 'p1,p2,p3', 'append keeps order and drops the repeated id: ' + merged.map((a) => a.id));
assert(page1.map((a) => a.id).join(',') === 'p1,p2', 'append does not mutate the first page');
assert(appendAssets(page1, []) === page1, 'append of an empty page returns the same array');
assert(appendAssets(page1, [{ id: 'p1' }]) === page1, 'append of only-seen ids returns the same array');

// mergeGroups: extend existing groups without re-grouping the whole library
const baseGroups = groupByDate([{ id: 'm1', localDateTime: '2024-08-08T10:00:00Z' }]);
const grown = mergeGroups(baseGroups, [
  { id: 'm2', localDateTime: '2024-08-08T09:00:00Z' }, // same day
  { id: 'm3', localDateTime: '2024-08-07T10:00:00Z' }, // older day
  { id: 'm4' },                                       // no date
]);
assert(grown.length === 3, 'merge adds one day group and the no-date group: ' + grown.length);
assert(grown[0].assets.map((a) => a.id).join(',') === 'm1,m2', 'same day merged in append order: ' + grown[0].assets.map((a) => a.id));
assert(grown[0].start === 0 && grown[1].start === 2, 'merge recomputes start offsets');
assert(grown[1].assets[0].id === 'm3', 'older day follows the newer day');
assert(grown[2].label === 'No Date', 'no-date group is last after merge');
assert(baseGroups[0].assets.length === 1, 'merge does not mutate the input groups');
assert(mergeGroups(baseGroups, []) === baseGroups, 'merge of an empty page returns the same array');

console.log(failed === 0 ? '\nALL PASS' : `\n${failed} FAILURE(S)`);
process.exit(failed === 0 ? 0 : 1);

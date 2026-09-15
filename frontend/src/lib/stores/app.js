import { writable } from 'svelte/store';

export const searchResults = writable(null);
export const searchQuery   = writable(null);
export const isSearching   = writable(false);
export const stats         = writable(null);
export const status        = writable(null);
export const toasts        = writable([]);

export const DEFAULT_PARAMS = {
  min_similarity:   0.85,
  max_results:      200,
  weights: {
    phash: 0.45,
    ahash: 0.10,
    dhash: 0.25,
    color: 0.20,
  },
  file_types:       [],
  extensions:       [],
  min_width:        0,  max_width:      0,
  min_height:       0,  max_height:     0,
  min_size:         0,  max_size:       0,
  min_date:         0,  max_date:       0,
  date_field:       'modified',
  path_pattern:     '',
  filename_pattern: '',
  codecs:           [],
  min_fps:          0,  max_fps:        0,
  min_bitrate:      0,  max_bitrate:    0,
  min_duration_ms:  0,  max_duration_ms: 0,
};

export const searchParams = writable(JSON.parse(JSON.stringify(DEFAULT_PARAMS)));

// ── Toast helper ──────────────────────────────────────────────────────────────

let _tid = 0;
export function addToast(msg, type = 'info', duration = 3500) {
  const id = ++_tid;
  toasts.update(t => [...t, { id, msg, type }]);
  setTimeout(() => toasts.update(t => t.filter(x => x.id !== id)), duration);
}

<script>
  import { onDestroy } from 'svelte';
  import { searchResults, searchQuery, isSearching, searchParams, addToast } from '$lib/stores/app.js';
  import { search } from '$lib/api.js';
  import UploadZone  from '$lib/components/UploadZone.svelte';
  import FileCard    from '$lib/components/FileCard.svelte';
  import FilterPanel from '$lib/components/FilterPanel.svelte';

  const VIDEO_EXTS = new Set(['mp4','mkv','mov','avi','webm','flv','wmv','m4v','mpg','mpeg','ts','mts']);

  let queryFile     = null;
  let previewUrl    = null;  // object URL – only set for images
  let isVideo       = false;
  let filterOpen    = false;
  let debounce      = null;

  // ── File intake ──────────────────────────────────────────────────────────
  async function handleFile(e) {
    const file = e.detail;
    queryFile = file;
    isVideo   = VIDEO_EXTS.has(file.name.split('.').pop().toLowerCase());

    if (previewUrl) URL.revokeObjectURL(previewUrl);
    previewUrl = isVideo ? null : URL.createObjectURL(file);

    await runSearch();
  }

  // ── Search ───────────────────────────────────────────────────────────────
  async function runSearch() {
    if (!queryFile) return;
    isSearching.set(true);
    try {
      const resp = await search(queryFile, $searchParams);
      searchResults.set(resp);
      searchQuery.set(resp.query);
    } catch (err) {
      addToast(err.message || 'Search failed', 'error');
    } finally {
      isSearching.set(false);
    }
  }

  function debouncedSearch() {
    clearTimeout(debounce);
    debounce = setTimeout(runSearch, 320);
  }

  function onThreshold(e) {
    searchParams.update(p => ({ ...p, min_similarity: +e.target.value / 100 }));
  }

  function onParamChange(e) {
    searchParams.set(e.detail);
    debouncedSearch();
  }

  // ── Reset ────────────────────────────────────────────────────────────────
  function reset() {
    queryFile = null;
    if (previewUrl) { URL.revokeObjectURL(previewUrl); previewUrl = null; }
    searchResults.set(null);
    searchQuery.set(null);
  }

  // ── Cleanup ──────────────────────────────────────────────────────────────
  onDestroy(() => {
    if (previewUrl) URL.revokeObjectURL(previewUrl);
    clearTimeout(debounce);
  });

  // ── Formatters ───────────────────────────────────────────────────────────
  function fmtBytes(n) {
    if (!n) return '0 B';
    const u = ['B','KB','MB','GB'];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return `${n < 10 && i > 0 ? n.toFixed(1) : Math.round(n)} ${u[i]}`;
  }

  $: threshold     = Math.round($searchParams.min_similarity * 100);
  $: resultCount   = $searchResults?.total ?? 0;
  $: resultMs      = $searchResults?.duration_ms ?? 0;
  $: results       = $searchResults?.results ?? [];
  $: hasQuery      = !!queryFile;
  $: hasResults    = results.length > 0;
  $: searchedEmpty = $searchResults && !$isSearching && results.length === 0;
</script>

<!-- ════════════════════════════════════════════════════════════════════════ -->

{#if !hasQuery}

  <!-- ── Initial upload zone ── -->
  <div class="upload-page">
    <div class="upload-center">
      <UploadZone on:file={handleFile} />
      <p class="hint">Searches your indexed local file system for visually similar media.</p>
    </div>
  </div>

{:else}

  <!-- ── Query bar (sticky) ── -->
  <div class="qbar">
    <!-- Thumbnail / video icon -->
    <div class="qthumb">
      {#if previewUrl}
        <img src={previewUrl} alt="" class="qimg" />
      {:else}
        <!-- Video placeholder -->
        <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24"
             fill="none" stroke="#444" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <polygon points="23 7 16 12 23 17 23 7"/>
          <rect x="1" y="5" width="15" height="14" rx="2" ry="2"/>
        </svg>
      {/if}
    </div>

    <!-- Meta -->
    <div class="qmeta">
      <span class="qname">{queryFile.name}</span>
      <div class="qpills">
        {#if $searchQuery?.width}
          <span class="pill">{$searchQuery.width}×{$searchQuery.height}</span>
        {/if}
        <span class="pill">{fmtBytes(queryFile.size)}</span>
        {#if $searchQuery?.file_type}
          <span class="pill type">{$searchQuery.file_type}</span>
        {/if}
      </div>
      <div class="qstats">
        {#if $isSearching}
          <span class="searching">Searching…</span>
        {:else if $searchResults}
          <span class="cnt">{resultCount}</span>
          <span class="cts">results</span>
          <span class="ctt">· {resultMs}ms</span>
        {/if}
      </div>
    </div>

    <!-- Controls -->
    <div class="qctl">
      <!-- Similarity threshold -->
      <div class="thr">
        <span class="thr-lbl">≥</span>
        <input
          type="range" min="0" max="100" step="1"
          value={threshold}
          on:input={onThreshold}
          on:change={runSearch}
          class="slider"
        />
        <span class="thr-val">{threshold}%</span>
      </div>

      <!-- Filter toggle -->
      <button
        class="btn"
        class:btn-active={filterOpen}
        on:click={() => filterOpen = !filterOpen}
        title="Toggle filters"
      >
        <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24"
             fill="none" stroke="currentColor" stroke-width="2.2"
             stroke-linecap="round" stroke-linejoin="round">
          <line x1="4" y1="6" x2="20" y2="6"/>
          <line x1="8" y1="12" x2="16" y2="12"/>
          <line x1="11" y1="18" x2="13" y2="18"/>
        </svg>
        Filters
      </button>

      <!-- Clear -->
      <button class="btn icon-btn" on:click={reset} title="Clear query">
        <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24"
             fill="none" stroke="currentColor" stroke-width="2.2"
             stroke-linecap="round" stroke-linejoin="round">
          <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
        </svg>
      </button>
    </div>
  </div>

  <!-- ── Filter panel ── -->
  {#if filterOpen}
    <FilterPanel params={$searchParams} on:change={onParamChange} />
  {/if}

  <!-- ── Results / skeleton / empty ── -->
  {#if $isSearching && !hasResults}
    <div class="grid">
      {#each { length: 16 } as _}
        <div class="skel"></div>
      {/each}
    </div>

  {:else if hasResults}
    <div class="grid">
      {#each results as result (result.file.id)}
        <FileCard {result} on:removed={runSearch} />
      {/each}
    </div>

  {:else if searchedEmpty}
    <div class="empty">
      <span class="empty-icon">
        <svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24"
             fill="none" stroke="#2a2a2a" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
          <line x1="8" y1="11" x2="14" y2="11"/>
        </svg>
      </span>
      <p class="empty-h">No matches above {threshold}%</p>
      <p class="empty-s">Lower the threshold or expand filters to see more results.</p>
    </div>
  {/if}

{/if}

<style>
  /* ── Upload page ─────────────────────────────── */
  .upload-page {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 40px 24px;
  }

  .upload-center {
    width: 100%;
    max-width: 540px;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 18px;
  }

  .hint {
    font-size: 12px;
    color: #2e2e2e;
    text-align: center;
  }

  /* ── Query bar ───────────────────────────────── */
  .qbar {
    position: sticky;
    top: 0;
    z-index: 50;
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 12px 18px;
    background: #080808;
    border-bottom: 1px solid #131313;
  }

  .qthumb {
    width: 52px;
    height: 52px;
    flex-shrink: 0;
    border-radius: 6px;
    background: #111;
    border: 1px solid #1c1c1c;
    overflow: hidden;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .qimg {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
  }

  .qmeta {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .qname {
    font-size: 13px;
    font-weight: 500;
    color: #c8c8c8;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .qpills {
    display: flex;
    gap: 5px;
    flex-wrap: wrap;
  }

  .pill {
    font-size: 10px;
    padding: 2px 7px;
    background: #111;
    border: 1px solid #1c1c1c;
    border-radius: 3px;
    color: #555;
    font-variant-numeric: tabular-nums;
  }

  .pill.type { text-transform: capitalize; }

  .qstats {
    display: flex;
    align-items: baseline;
    gap: 5px;
    font-size: 11px;
    color: #3a3a3a;
  }

  .searching { color: #3b82f6; animation: pulse 1.3s ease-in-out infinite; }

  @keyframes pulse { 0%,100%{opacity:1} 50%{opacity:0.35} }

  .cnt { font-size: 13px; font-weight: 600; color: #777; }
  .cts { color: #3a3a3a; }
  .ctt { color: #282828; }

  /* ── Controls ────────────────────────────────── */
  .qctl {
    display: flex;
    align-items: center;
    gap: 7px;
    flex-shrink: 0;
  }

  .thr {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .thr-lbl {
    font-size: 14px;
    color: #2e2e2e;
    line-height: 1;
  }

  .slider {
    width: 110px;
    height: 3px;
    -webkit-appearance: none;
    appearance: none;
    background: #1f1f1f;
    border-radius: 2px;
    outline: none;
    cursor: pointer;
  }

  .slider::-webkit-slider-thumb {
    -webkit-appearance: none;
    width: 13px;
    height: 13px;
    background: #ededed;
    border-radius: 50%;
    cursor: pointer;
    border: 2px solid #080808;
    box-shadow: 0 0 0 1.5px #444;
  }

  .thr-val {
    font-size: 12px;
    color: #777;
    font-variant-numeric: tabular-nums;
    width: 33px;
    text-align: right;
  }

  .btn {
    display: flex;
    align-items: center;
    gap: 5px;
    background: none;
    border: 1px solid #1c1c1c;
    color: #4a4a4a;
    font-size: 12px;
    padding: 6px 10px;
    border-radius: 5px;
    cursor: pointer;
    transition: border-color 0.12s, color 0.12s, background 0.12s;
  }

  .btn:hover    { border-color: #2e2e2e; color: #888; }
  .btn-active   { border-color: #2e2e2e; color: #c0c0c0; background: #0e0e0e; }
  .icon-btn     { padding: 6px 8px; }

  /* ── Results grid ────────────────────────────── */
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(185px, 1fr));
    gap: 1px;
    background: #0d0d0d;
  }

  /* ── Skeleton loading ────────────────────────── */
  .skel {
    background: #0a0a0a;
    aspect-ratio: 1;
    animation: shim 1.6s ease-in-out infinite;
  }

  @keyframes shim {
    0%,100% { background: #0a0a0a; }
    50%      { background: #0e0e0e; }
  }

  /* ── Empty state ─────────────────────────────── */
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    min-height: 280px;
    gap: 12px;
    padding: 60px 40px;
  }

  .empty-icon { opacity: 0.5; }
  .empty-h    { font-size: 14px; color: #3a3a3a; }
  .empty-s    { font-size: 12px; color: #252525; text-align: center; max-width: 320px; }
</style>

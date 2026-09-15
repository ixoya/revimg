<script>
  import { createEventDispatcher } from 'svelte';
  import { DEFAULT_PARAMS } from '$lib/stores/app.js';

  export let params;

  const dispatch = createEventDispatcher();

  // Work on a deep copy so we don't mutate the store directly
  let p = JSON.parse(JSON.stringify(params));

  function emit() { dispatch('change', JSON.parse(JSON.stringify(p))); }

  // ── Weight helpers ───────────────────────────────────────────────────────
  function wPct(k) { return Math.round(p.weights[k] * 100); }
  function setW(k, v) { p.weights[k] = v / 100; p = p; emit(); }

  // ── File type toggles ────────────────────────────────────────────────────
  function toggleType(t) {
    if (p.file_types.includes(t))
      p.file_types = p.file_types.filter(x => x !== t);
    else
      p.file_types = [...p.file_types, t];
    emit();
  }

  // ── Extension tag input ──────────────────────────────────────────────────
  let extInput = '';
  function addExt() {
    let e = extInput.trim().toLowerCase();
    if (!e) return;
    if (!e.startsWith('.')) e = '.' + e;
    if (!p.extensions.includes(e)) p.extensions = [...p.extensions, e];
    extInput = '';
    emit();
  }
  function removeExt(e) { p.extensions = p.extensions.filter(x => x !== e); emit(); }

  // ── Date helpers ─────────────────────────────────────────────────────────
  function toDateStr(unix) {
    if (!unix) return '';
    return new Date(unix * 1000).toISOString().slice(0, 10);
  }
  function fromDateStr(s) {
    if (!s) return 0;
    return Math.floor(new Date(s).getTime() / 1000);
  }

  // ── Reset ────────────────────────────────────────────────────────────────
  function reset() {
    p = JSON.parse(JSON.stringify(DEFAULT_PARAMS));
    extInput = '';
    emit();
  }

  const WEIGHTS = [
    { key: 'phash', label: 'pHash (DCT)',   title: 'Perceptual hash using Discrete Cosine Transform — best overall' },
    { key: 'dhash', label: 'dHash (Diff)',  title: 'Difference hash comparing adjacent pixels — good for crops' },
    { key: 'ahash', label: 'aHash (Avg)',   title: 'Average hash — fast, tolerates brightness changes' },
    { key: 'color', label: 'Color Hist',    title: 'HSV colour histogram intersection — catches colour similarity' },
  ];
</script>

<div class="panel">
  <div class="sections">

    <!-- Algorithm Weights -->
    <section class="sec">
      <h3 class="sec-title">Algorithm Weights</h3>
      {#each WEIGHTS as w}
        <div class="weight-row" title={w.title}>
          <span class="w-label">{w.label}</span>
          <input
            type="range" min="0" max="100" step="5"
            value={wPct(w.key)}
            on:input={e => setW(w.key, +e.target.value)}
            class="range"
          />
          <span class="w-pct">{wPct(w.key)}%</span>
        </div>
      {/each}
    </section>

    <!-- File Type -->
    <section class="sec">
      <h3 class="sec-title">File Type</h3>
      <div class="chip-row">
        {#each ['image','video'] as t}
          <button
            class="chip"
            class:on={p.file_types.includes(t)}
            on:click={() => toggleType(t)}
          >{t}</button>
        {/each}
        {#if p.file_types.length}
          <button class="chip-clear" on:click={() => { p.file_types = []; emit(); }}>clear</button>
        {/if}
      </div>
    </section>

    <!-- Extensions -->
    <section class="sec">
      <h3 class="sec-title">Extensions</h3>
      <div class="tags">
        {#each p.extensions as ext}
          <span class="tag">
            {ext}
            <button class="tag-x" on:click={() => removeExt(ext)}>×</button>
          </span>
        {/each}
      </div>
      <input
        class="inp sm"
        type="text"
        placeholder=".jpg, .png …"
        bind:value={extInput}
        on:keydown={e => { if (e.key==='Enter'||e.key===',') { e.preventDefault(); addExt(); } }}
      />
    </section>

    <!-- Dimensions -->
    <section class="sec">
      <h3 class="sec-title">Dimensions (px)</h3>
      <div class="range-rows">
        <div class="range-row">
          <span class="rl">W</span>
          <input class="inp n" type="number" min="0" placeholder="min"
            bind:value={p.min_width} on:change={emit} />
          <span class="dash">—</span>
          <input class="inp n" type="number" min="0" placeholder="max"
            bind:value={p.max_width} on:change={emit} />
        </div>
        <div class="range-row">
          <span class="rl">H</span>
          <input class="inp n" type="number" min="0" placeholder="min"
            bind:value={p.min_height} on:change={emit} />
          <span class="dash">—</span>
          <input class="inp n" type="number" min="0" placeholder="max"
            bind:value={p.max_height} on:change={emit} />
        </div>
      </div>
    </section>

    <!-- File Size -->
    <section class="sec">
      <h3 class="sec-title">File Size (bytes)</h3>
      <div class="range-row">
        <input class="inp n wide" type="number" min="0" placeholder="min"
          bind:value={p.min_size} on:change={emit} />
        <span class="dash">—</span>
        <input class="inp n wide" type="number" min="0" placeholder="max"
          bind:value={p.max_size} on:change={emit} />
      </div>
    </section>

    <!-- Date Modified -->
    <section class="sec">
      <h3 class="sec-title">Date Modified</h3>
      <div class="range-row">
        <input class="inp dt" type="date"
          value={toDateStr(p.min_date)}
          on:change={e => { p.min_date = fromDateStr(e.target.value); emit(); }} />
        <span class="dash">—</span>
        <input class="inp dt" type="date"
          value={toDateStr(p.max_date)}
          on:change={e => { p.max_date = fromDateStr(e.target.value); emit(); }} />
      </div>
    </section>

    <!-- Path / filename patterns -->
    <section class="sec">
      <h3 class="sec-title">Patterns</h3>
      <input class="inp" type="text" placeholder="Path contains…"
        bind:value={p.path_pattern} on:input={emit} />
      <input class="inp" type="text" placeholder="Filename (e.g. IMG_*.jpg)"
        bind:value={p.filename_pattern} on:input={emit}
        style="margin-top:6px" />
    </section>

    <!-- Video filters -->
    <section class="sec">
      <h3 class="sec-title">Video</h3>
      <div class="range-rows">
        <div class="range-row">
          <span class="rl">FPS</span>
          <input class="inp n" type="number" min="0" step="0.1" placeholder="min"
            bind:value={p.min_fps} on:change={emit} />
          <span class="dash">—</span>
          <input class="inp n" type="number" min="0" step="0.1" placeholder="max"
            bind:value={p.max_fps} on:change={emit} />
        </div>
        <div class="range-row">
          <span class="rl">kbps</span>
          <input class="inp n wide" type="number" min="0" placeholder="min bitrate"
            bind:value={p.min_bitrate} on:change={emit} />
          <span class="dash">—</span>
          <input class="inp n wide" type="number" min="0" placeholder="max bitrate"
            bind:value={p.max_bitrate} on:change={emit} />
        </div>
        <div class="range-row">
          <span class="rl">sec</span>
          <input class="inp n wide" type="number" min="0" placeholder="min dur (s)"
            value={p.min_duration_ms ? p.min_duration_ms/1000 : ''}
            on:change={e => { p.min_duration_ms = e.target.value ? +e.target.value*1000 : 0; emit(); }} />
          <span class="dash">—</span>
          <input class="inp n wide" type="number" min="0" placeholder="max dur (s)"
            value={p.max_duration_ms ? p.max_duration_ms/1000 : ''}
            on:change={e => { p.max_duration_ms = e.target.value ? +e.target.value*1000 : 0; emit(); }} />
        </div>
      </div>
    </section>

    <!-- Max results -->
    <section class="sec">
      <h3 class="sec-title">Max Results</h3>
      <input class="inp n" type="number" min="1" max="2000"
        bind:value={p.max_results} on:change={emit} style="width:80px" />
    </section>

  </div>

  <div class="footer">
    <button class="reset-btn" on:click={reset}>Reset all filters</button>
  </div>
</div>

<style>
  .panel {
    border-bottom: 1px solid #161616;
    background: #060606;
  }

  .sections {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    border-top: 1px solid #161616;
  }

  .sec {
    padding: 14px 18px;
    border-right: 1px solid #111;
    border-bottom: 1px solid #111;
  }

  .sec-title {
    font-size: 9.5px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: #444;
    margin-bottom: 10px;
  }

  /* ── Weights ─────────────────────────────────── */
  .weight-row {
    display: flex;
    align-items: center;
    gap: 7px;
    margin-bottom: 6px;
    cursor: default;
  }

  .w-label {
    font-size: 10.5px;
    color: #555;
    width: 82px;
    flex-shrink: 0;
  }

  .range {
    flex: 1;
    height: 2px;
    -webkit-appearance: none;
    appearance: none;
    background: #1f1f1f;
    border-radius: 1px;
    outline: none;
    cursor: pointer;
  }

  .range::-webkit-slider-thumb {
    -webkit-appearance: none;
    width: 11px;
    height: 11px;
    background: #ededed;
    border-radius: 50%;
    cursor: pointer;
  }

  .w-pct {
    font-size: 10.5px;
    color: #777;
    font-variant-numeric: tabular-nums;
    width: 28px;
    text-align: right;
  }

  /* ── Chips ───────────────────────────────────── */
  .chip-row {
    display: flex;
    gap: 5px;
    flex-wrap: wrap;
  }

  .chip {
    background: none;
    border: 1px solid #1f1f1f;
    color: #555;
    font-size: 11px;
    padding: 3px 10px;
    border-radius: 4px;
    transition: all 0.12s;
    text-transform: capitalize;
  }

  .chip:hover { border-color: #333; color: #888; }
  .chip.on    { border-color: #444; background: #161616; color: #ccc; }

  .chip-clear {
    background: none;
    border: none;
    color: #333;
    font-size: 11px;
    padding: 3px 6px;
    cursor: pointer;
    transition: color 0.12s;
  }
  .chip-clear:hover { color: #666; }

  /* ── Tags ────────────────────────────────────── */
  .tags {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
    margin-bottom: 6px;
    min-height: 4px;
  }

  .tag {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    background: #111;
    border: 1px solid #1f1f1f;
    border-radius: 3px;
    padding: 2px 6px;
    font-size: 10.5px;
    color: #777;
    font-family: 'JetBrains Mono', monospace;
  }

  .tag-x {
    background: none;
    border: none;
    color: #444;
    font-size: 12px;
    line-height: 1;
    padding: 0;
    cursor: pointer;
    transition: color 0.12s;
  }
  .tag-x:hover { color: #ef4444; }

  /* ── Range rows ──────────────────────────────── */
  .range-rows { display: flex; flex-direction: column; gap: 7px; }

  .range-row {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .rl {
    font-size: 10.5px;
    color: #444;
    width: 32px;
    flex-shrink: 0;
  }

  .dash { color: #2a2a2a; font-size: 11px; }

  /* ── Inputs ──────────────────────────────────── */
  .inp {
    background: #0e0e0e;
    border: 1px solid #1a1a1a;
    color: #ccc;
    border-radius: 5px;
    padding: 5px 9px;
    font-size: 11.5px;
    outline: none;
    width: 100%;
    transition: border-color 0.12s;
  }

  .inp:focus { border-color: #333; }
  .inp::placeholder { color: #2e2e2e; }

  .inp.sm  { padding: 4px 7px; font-size: 11px; }
  .inp.n   { width: 62px; text-align: right; font-variant-numeric: tabular-nums; }
  .inp.wide { width: 80px; }
  .inp.dt  { flex: 1; width: auto; }

  /* ── Footer ──────────────────────────────────── */
  .footer {
    padding: 10px 18px;
    border-top: 1px solid #111;
    display: flex;
    justify-content: flex-end;
  }

  .reset-btn {
    background: none;
    border: 1px solid #1a1a1a;
    color: #444;
    font-size: 11px;
    padding: 5px 12px;
    border-radius: 5px;
    cursor: pointer;
    transition: all 0.12s;
  }
  .reset-btn:hover { border-color: #333; color: #888; }
</style>

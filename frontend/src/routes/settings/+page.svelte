<script>
  import { onMount } from 'svelte';
  import { getConfig, putConfig } from '$lib/api.js';
  import { addToast } from '$lib/stores/app.js';

  let cfg    = null;
  let saving = false;
  let dirty  = false;

  onMount(async () => {
    try {
      cfg = await getConfig();
    } catch (e) {
      addToast(e.message, 'error');
    }
  });

  function mark() { dirty = true; }

  async function save() {
    saving = true;
    try {
      cfg = await putConfig(cfg);
      dirty = false;
      addToast('Settings saved', 'success');
    } catch (e) {
      addToast(e.message, 'error');
    } finally {
      saving = false;
    }
  }

  function fmtBytes(n) {
    if (!n) return '0';
    const u = ['B','KB','MB','GB'];
    let i = 0, v = n;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${u[i]}`;
  }

  const commonIgnored = ['node_modules', '.git', '__pycache__', '.venv', 'venv', '.cache', 'vendor', '.next', '.nuxt', 'dist', 'build', '.svelte-kit'];
  let newIgnored = '';

  function toggleIgnored(dir) {
    if (cfg.ignored_dirs == null) cfg.ignored_dirs = [];
    const i = cfg.ignored_dirs.indexOf(dir);
    if (i >= 0) cfg.ignored_dirs.splice(i, 1);
    else cfg.ignored_dirs.push(dir);
  }

  function addIgnored() {
    const d = newIgnored.trim();
    if (!d) return;
    if (cfg.ignored_dirs == null) cfg.ignored_dirs = [];
    if (!cfg.ignored_dirs.includes(d)) cfg.ignored_dirs.push(d);
    newIgnored = '';
  }

  function removeIgnored(dir) {
    if (cfg.ignored_dirs == null) return;
    const i = cfg.ignored_dirs.indexOf(dir);
    if (i >= 0) cfg.ignored_dirs.splice(i, 1);
  }
</script>

<div class="page">
  <div class="header">
    <div>
      <h1 class="title">Settings</h1>
      <p class="sub">Configuration is persisted to <code>config.json</code> in the data directory.</p>
    </div>
    {#if dirty}
      <button class="btn-save" on:click={save} disabled={saving}>
        {saving ? 'Saving…' : 'Save changes'}
      </button>
    {/if}
  </div>

  {#if !cfg}
    <div class="loading">Loading…</div>
  {:else}

    <!-- ── Server ── -->
    <section class="sec">
      <h2 class="sec-title">Server</h2>
      <div class="fields">
        <div class="field">
          <label class="lbl" for="cfg-listen">Listen address</label>
          <input id="cfg-listen" class="inp mono" type="text" bind:value={cfg.listen_addr} on:input={mark} />
          <p class="desc">Restart required to apply. Default: 127.0.0.1:7777</p>
        </div>
        <div class="field">
          <label class="lbl" for="cfg-max-upload">Max upload size</label>
          <div class="inp-row">
            <input id="cfg-max-upload" class="inp n" type="number" min="0" bind:value={cfg.max_file_size} on:input={mark} />
            <span class="inp-hint">{fmtBytes(cfg.max_file_size)}</span>
          </div>
          <p class="desc">Maximum file size for query uploads (bytes).</p>
        </div>
        <div class="field">
          <label class="lbl" for="cfg-thumb">Thumbnail size (px)</label>
          <input id="cfg-thumb" class="inp n" type="number" min="64" max="1024" bind:value={cfg.thumb_size} on:input={mark} />
          <p class="desc">Max width/height of generated thumbnails.</p>
        </div>
        <div class="field">
          <label class="lbl" for="cfg-log">Log file path</label>
          <input id="cfg-log" class="inp mono" type="text" bind:value={cfg.log_path} on:input={mark} placeholder="/var/log/revimg.log" />
          <p class="desc">Absolute path, or filename saved under data dir. Empty = stdout only. Restart required.</p>
        </div>
      </div>
    </section>

    <!-- ── Indexing ── -->
    <section class="sec">
      <h2 class="sec-title">Indexing</h2>
      <div class="fields">
        <div class="field">
          <label class="lbl" for="cfg-workers">Worker threads</label>
          <input id="cfg-workers" class="inp n" type="number" min="1" max="64" bind:value={cfg.num_workers} on:input={mark} />
          <p class="desc">Parallel file processing workers. Default: CPU count.</p>
        </div>
        <div class="field">
          <span class="lbl" id="lbl-hash-size">Hash grid size</span>
          <div class="inp-row" role="group" aria-labelledby="lbl-hash-size">
            {#each [8, 16, 32] as s}
              <button
                class="size-btn"
                class:on={cfg.hash_size === s}
                on:click={() => { cfg.hash_size = s; mark(); }}
              >{s}×{s}</button>
            {/each}
          </div>
          <p class="desc">
            pHash DCT input resolution. Larger = more discriminative but slower indexing.
            <strong>8×8 recommended</strong> for most use cases.
          </p>
        </div>
        <div class="field">
          <span class="lbl" id="lbl-color-bins">Color histogram bins</span>
          <div class="inp-row" role="group" aria-labelledby="lbl-color-bins">
            {#each [4, 8, 16] as b}
              <button
                class="size-btn"
                class:on={cfg.color_bins === b}
                on:click={() => { cfg.color_bins = b; mark(); }}
              >{b}</button>
            {/each}
          </div>
          <p class="desc">Bins per HSV channel. Total bins = bins³. 8 = 512 bins.</p>
        </div>
        <div class="field">
          <span class="lbl" id="lbl-ignored">Ignored directories</span>
          <div class="chip-row">
            {#each commonIgnored as dir}
              <button
                class="chip"
                class:on={cfg.ignored_dirs?.includes(dir)}
                on:click={() => { toggleIgnored(dir); mark(); }}
              >{dir}</button>
            {/each}
          </div>
          {#if cfg.ignored_dirs?.length > commonIgnored.length}
            <div class="chip-row">
              {#each cfg.ignored_dirs.filter(d => !commonIgnored.includes(d)) as custom}
                <button class="chip on" on:click={() => { removeIgnored(custom); mark(); }}>
                  {custom} <span class="chip-x">×</span>
                </button>
              {/each}
            </div>
          {/if}
          <div class="inp-row" style="margin-top:6px">
            <input class="inp" type="text" placeholder="Add custom…" bind:value={newIgnored}
              on:keydown={e => { if (e.key === 'Enter') { addIgnored(); mark(); }}} />
            <button class="add-btn" on:click={() => { addIgnored(); mark(); }} disabled={!newIgnored?.trim()}>+</button>
          </div>
          <p class="desc">Skip directories with these names during scanning. Already indexed files are removed.</p>
        </div>
        <div class="field">
          <label class="lbl">
            <span>Preserve index on unmount</span>
            <input type="checkbox" bind:checked={cfg.preserve_on_unmount} on:change={mark} class="chk" />
          </label>
          <p class="desc">When enabled, the indexer won't delete files from the DB if their parent directory disappears. Prevents data loss when external drives are unmounted.</p>
        </div>
      </div>
    </section>

    <!-- ── Video ── -->
    <section class="sec">
      <h2 class="sec-title">Video</h2>
      <div class="fields">
        <div class="field">
          <label class="lbl">
            <span>Video indexing</span>
            <input type="checkbox" bind:checked={cfg.video_enabled} on:change={mark} class="chk" />
          </label>
          <p class="desc">Requires ffmpeg and ffprobe to be installed and in PATH.</p>
        </div>
        <div class="field">
          <label class="lbl" for="cfg-ffmpeg">ffmpeg path</label>
          <input id="cfg-ffmpeg" class="inp mono" type="text" bind:value={cfg.ffmpeg_path} on:input={mark} />
        </div>
        <div class="field">
          <label class="lbl" for="cfg-ffprobe">ffprobe path</label>
          <input id="cfg-ffprobe" class="inp mono" type="text" bind:value={cfg.ffprobe_path} on:input={mark} />
        </div>
        <div class="field">
          <label class="lbl" for="cfg-fps">Sample FPS</label>
          <input id="cfg-fps" class="inp n" type="number" min="0.1" max="30" step="0.1"
            bind:value={cfg.video_fps} on:input={mark} />
          <p class="desc">Frames per second to sample for hashing. 1.0 = 1 frame/sec.</p>
        </div>
        <div class="field">
          <label class="lbl" for="cfg-max-frames">Max frames</label>
          <input id="cfg-max-frames" class="inp n" type="number" min="1" max="256"
            bind:value={cfg.max_frames} on:input={mark} />
          <p class="desc">Maximum sampled frames per video. Higher = better accuracy, slower.</p>
        </div>
      </div>
    </section>

    <!-- ── Search defaults ── -->
    <section class="sec">
      <h2 class="sec-title">Search Defaults</h2>
      <div class="fields">
        <div class="field">
          <label class="lbl" for="cfg-threshold">Default similarity threshold</label>
          <div class="inp-row">
            <input
              id="cfg-threshold"
              type="range" min="0" max="100" step="1"
              value={Math.round((cfg.default_threshold ?? 0.85) * 100)}
              on:input={e => { cfg.default_threshold = +e.target.value / 100; mark(); }}
              class="slider"
            />
            <span class="slider-val">
              {Math.round((cfg.default_threshold ?? 0.85) * 100)}%
            </span>
          </div>
        </div>
        <div class="field">
          <label class="lbl" for="cfg-max-results">Default max results</label>
          <input id="cfg-max-results" class="inp n" type="number" min="1" max="5000"
            bind:value={cfg.max_results} on:input={mark} />
        </div>
      </div>
    </section>

    <!-- ── Algorithm weights ── -->
    <section class="sec">
      <h2 class="sec-title">Default Algorithm Weights</h2>
      <div class="fields">
        {#each [
          { key: 'weight_phash', label: 'pHash (DCT)',  desc: 'DCT frequency-domain perceptual hash' },
          { key: 'weight_dhash', label: 'dHash (Diff)', desc: 'Difference hash (horizontal gradient)' },
          { key: 'weight_ahash', label: 'aHash (Avg)',  desc: 'Average hash — fastest, least precise' },
          { key: 'weight_color', label: 'Color Hist',   desc: 'HSV colour histogram intersection' },
        ] as w}
          <div class="field inline">
            <label class="lbl" for={'cfg-' + w.key}>{w.label}</label>
            <div class="inp-row">
              <input
                id={'cfg-' + w.key}
                type="range" min="0" max="100" step="5"
                value={Math.round((cfg[w.key] ?? 0) * 100)}
                on:input={e => { cfg[w.key] = +e.target.value / 100; mark(); }}
                class="slider"
              />
              <span class="slider-val">{Math.round((cfg[w.key] ?? 0) * 100)}%</span>
            </div>
            <p class="desc">{w.desc}</p>
          </div>
        {/each}
      </div>
    </section>

    <!-- ── Index health ── -->
    <section class="sec">
      <h2 class="sec-title">Index Health</h2>
      <div class="fields">
        <div class="field">
          <label class="lbl">
            <span>Background repair</span>
            <input type="checkbox" bind:checked={cfg.repair_enabled} on:change={mark} class="chk" />
          </label>
          <p class="desc">Slow pass that re-indexes files missing fingerprints, then verifies the rest. Runs after scans and daily.</p>
        </div>
        <div class="field">
          <label class="lbl" for="cfg-repair-interval">Cycle interval (hours)</label>
          <input id="cfg-repair-interval" class="inp n" type="number" min="0" max="720"
            bind:value={cfg.repair_interval_h} on:input={mark} />
          <p class="desc">Hours between background passes. 0 disables the timer (manual + post-scan only).</p>
        </div>
        <div class="field">
          <label class="lbl" for="cfg-repair-threads">ffmpeg threads</label>
          <input id="cfg-repair-threads" class="inp n" type="number" min="1" max="64"
            bind:value={cfg.repair_threads} on:input={mark} />
          <p class="desc">Decode threads per file during repair. 1 keeps the machine usable.</p>
        </div>
        <div class="field">
          <label class="lbl" for="cfg-repair-delay">Delay between files (ms)</label>
          <input id="cfg-repair-delay" class="inp n" type="number" min="0" max="60000"
            bind:value={cfg.repair_delay_ms} on:input={mark} />
        </div>
        <div class="field">
          <label class="lbl" for="cfg-sha-budget">Bitrot budget (GB/cycle)</label>
          <input id="cfg-sha-budget" class="inp n" type="number" min="0" max="10000"
            value={Math.round((cfg.repair_sha_budget_bytes ?? 0) / 1073741824)}
            on:input={e => { cfg.repair_sha_budget_bytes = (+e.target.value || 0) * 1073741824; mark(); }} />
          <p class="desc">How much file content to re-read for bitrot checks per cycle, oldest-first.</p>
        </div>
      </div>
    </section>

  {/if}
</div>

<style>
  .page {
    max-width: 780px;
    padding-bottom: 60px;
  }

  /* ── Header ──────────────────────────────────── */
  .header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 20px;
    padding: 32px 28px 24px;
    border-bottom: 1px solid #131313;
  }

  .title {
    font-size: 20px;
    font-weight: 600;
    color: #ededed;
    letter-spacing: -0.02em;
    margin-bottom: 6px;
  }

  .sub {
    font-size: 13px;
    color: #4a4a4a;
    line-height: 1.5;
  }

  code {
    font-family: 'JetBrains Mono', monospace;
    font-size: 11px;
    background: #111;
    border: 1px solid #1c1c1c;
    padding: 1px 5px;
    border-radius: 3px;
    color: #777;
  }

  .btn-save {
    background: #ededed;
    border: none;
    color: #000;
    font-size: 12px;
    font-weight: 600;
    padding: 8px 18px;
    border-radius: 6px;
    cursor: pointer;
    white-space: nowrap;
    flex-shrink: 0;
    transition: background 0.12s;
  }

  .btn-save:hover    { background: #fff; }
  .btn-save:disabled { opacity: 0.35; cursor: not-allowed; }

  .loading {
    padding: 40px 28px;
    font-size: 13px;
    color: #333;
  }

  /* ── Sections ────────────────────────────────── */
  .sec {
    padding: 24px 28px;
    border-bottom: 1px solid #0e0e0e;
  }

  .sec-title {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: #444;
    margin-bottom: 18px;
  }

  /* ── Fields ──────────────────────────────────── */
  .fields {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }

  .field.inline { gap: 5px; }

  .lbl {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 12.5px;
    font-weight: 500;
    color: #888;
  }

  .desc {
    font-size: 11px;
    color: #333;
    line-height: 1.5;
  }

  strong { color: #555; font-weight: 600; }

  /* ── Inputs ──────────────────────────────────── */
  .inp {
    background: #0c0c0c;
    border: 1px solid #1c1c1c;
    color: #c8c8c8;
    border-radius: 6px;
    padding: 7px 11px;
    font-size: 12.5px;
    outline: none;
    width: 100%;
    max-width: 380px;
    transition: border-color 0.12s;
  }

  .inp:focus   { border-color: #333; }
  .inp.n       { max-width: 100px; text-align: right; font-variant-numeric: tabular-nums; }
  .inp.mono    { font-family: 'JetBrains Mono', monospace; font-size: 12px; }

  .inp-row {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .inp-hint {
    font-size: 11px;
    color: #444;
    font-variant-numeric: tabular-nums;
  }

  /* ── Checkbox ────────────────────────────────── */
  .chk {
    width: 15px;
    height: 15px;
    accent-color: #ededed;
    cursor: pointer;
  }

  /* ── Size buttons ────────────────────────────── */
  .size-btn {
    background: none;
    border: 1px solid #1c1c1c;
    color: #555;
    font-size: 11.5px;
    font-family: 'JetBrains Mono', monospace;
    padding: 4px 12px;
    border-radius: 4px;
    cursor: pointer;
    transition: all 0.12s;
  }

  .size-btn:hover { border-color: #333; color: #888; }
  .size-btn.on    { border-color: #444; background: #141414; color: #d0d0d0; }

  /* ── Chip buttons (ignored dirs) ─────────────── */
  .chip-row {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .chip {
    background: none;
    border: 1px solid #1c1c1c;
    color: #555;
    font-size: 11px;
    font-family: 'JetBrains Mono', monospace;
    padding: 3px 10px;
    border-radius: 4px;
    cursor: pointer;
    transition: all 0.12s;
  }

  .chip:hover { border-color: #333; color: #888; }
  .chip.on    { border-color: #444; background: #141414; color: #d0d0d0; }

  .chip-x {
    display: inline-block;
    margin-left: 4px;
    color: #666;
    font-weight: 700;
    font-size: 13px;
    line-height: 1;
  }

  .chip.on:hover .chip-x { color: #e44; }

  .add-btn {
    background: none;
    border: 1px solid #1c1c1c;
    color: #555;
    font-size: 14px;
    padding: 3px 11px;
    border-radius: 4px;
    cursor: pointer;
    transition: all 0.12s;
  }

  .add-btn:hover          { border-color: #333; color: #888; }
  .add-btn:disabled       { opacity: 0.25; cursor: not-allowed; }
  .add-btn:disabled:hover { border-color: #1c1c1c; color: #555; }

  /* ── Sliders ─────────────────────────────────── */
  .slider {
    width: 200px;
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

  .slider-val {
    font-size: 12px;
    color: #666;
    font-variant-numeric: tabular-nums;
    width: 34px;
    text-align: right;
  }
</style>

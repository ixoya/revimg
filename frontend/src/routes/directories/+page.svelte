<script>
  import { onMount, onDestroy } from 'svelte';
  import { getDirs, addDir, deleteDir, scanDir, toggleDir } from '$lib/api.js';
import { getRepair, startRepair, cancelRepair } from '$lib/api.js';
import { addToast } from '$lib/stores/app.js';

  let dirs    = [];
  let loading = true;
  let adding  = false;
  let newPath = '';
  let newRecursive = true;

  let pollTimer = null;

  let repair = null;
  let lastCycle = null;

  async function loadRepair() {
    try {
      const r = await getRepair();
      repair = r.progress;
      lastCycle = r.last_cycle;
    } catch { /* ignore */ }
  }

  async function start() {
    try {
      await startRepair();
      addToast('Repair started — runs quietly in the background', 'info');
      await loadRepair();
    } catch (e) {
      addToast(e.message, 'error');
    }
  }

  async function cancel() {
    try {
      await cancelRepair();
      await loadRepair();
    } catch (e) {
      addToast(e.message, 'error');
    }
  }

  // ── Data ─────────────────────────────────────────────────────────────────
  async function load() {
    try {
      dirs = await getDirs();
    } catch (e) {
      addToast(e.message, 'error');
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    load();
    loadRepair();
    // Poll while any scan is active so progress updates; repair status always
    pollTimer = setInterval(() => {
      loadRepair();
      if (dirs.some(d => d.progress && !d.progress.done)) load();
    }, 1500);
  });

  onDestroy(() => clearInterval(pollTimer));

  // ── Actions ───────────────────────────────────────────────────────────────
  async function add() {
    const path = newPath.trim();
    if (!path) return;
    adding = true;
    try {
      await addDir(path, newRecursive, []);
      newPath = '';
      await load();
      addToast('Directory added — scan started', 'success');
    } catch (e) {
      addToast(e.message, 'error');
    } finally {
      adding = false;
    }
  }

  async function remove(id, path) {
    if (!confirm(`Remove "${path}" from the index?\n\nFiles already indexed will be removed from the database.`)) return;
    try {
      await deleteDir(id);
      await load();
      addToast('Directory removed', 'info');
    } catch (e) {
      addToast(e.message, 'error');
    }
  }

  async function rescan(id) {
    try {
      await scanDir(id);
      addToast('Scan started', 'info');
      setTimeout(load, 400);
    } catch (e) {
      addToast(e.message, 'error');
    }
  }

  async function toggle(id, enabled) {
    try {
      await toggleDir(id, !enabled);
      await load();
    } catch (e) {
      addToast(e.message, 'error');
    }
  }

  // ── Helpers ───────────────────────────────────────────────────────────────
  function fmtDate(unix) {
    if (!unix) return 'Never';
    return new Date(unix * 1000).toLocaleString();
  }

  function scanPct(p) {
    if (!p || !p.total) return 0;
    return Math.min(100, Math.round((p.processed / p.total) * 100));
  }

  function scanLabel(p) {
    if (!p) return '';
    const done = p.processed ?? 0;
    const tot  = p.total ?? 0;
    const errs = p.errors ?? 0;
    let s = `${done.toLocaleString()} / ${tot.toLocaleString()}`;
    if (errs > 0) s += ` · ${errs} error${errs !== 1 ? 's' : ''}`;
    return s;
  }
</script>

<div class="page">

  <!-- ── Header ── -->
  <div class="header">
    <div>
      <h1 class="title">Libraries</h1>
      <p class="sub">Directories revimg watches and indexes for similarity search.</p>
    </div>
  </div>

  <!-- ── Add directory ── -->
  <div class="add-card">
    <h2 class="card-label">Add directory</h2>
    <div class="add-row">
      <input
        class="path-inp"
        type="text"
        placeholder="/home/user/Pictures"
        bind:value={newPath}
        on:keydown={e => e.key === 'Enter' && add()}
      />
      <label class="chk-label">
        <input type="checkbox" bind:checked={newRecursive} class="chk" />
        Recursive
      </label>
      <button class="btn-primary" on:click={add} disabled={adding || !newPath.trim()}>
        {adding ? 'Adding…' : 'Add'}
      </button>
    </div>
    <p class="add-hint">
      Recursive scan includes all sub-directories.
      revimg will also watch for new/changed files automatically.
    </p>
  </div>

  <!-- ── Index health ── -->
  <div class="add-card">
    <h2 class="card-label">Index health</h2>
    <div class="add-row">
      {#if repair?.running}
        <span class="prog-label">
          Repairing… {repair.checked.toLocaleString()}{repair.total ? ` / ${repair.total.toLocaleString()}` : ''} checked
          · {repair.repaired} fixed · {repair.errors} errors
        </span>
        <button class="btn-ghost" on:click={cancel}>Cancel</button>
      {:else}
        <button class="btn-primary" on:click={start}>Repair now</button>
        {#if lastCycle?.last_cycle_completed_at}
          <span class="meta-date">
            Last pass: {fmtDate(lastCycle.last_cycle_completed_at)}
            · {lastCycle.last_cycle_checked.toLocaleString()} checked
            · {lastCycle.last_cycle_repaired.toLocaleString()} fixed
          </span>
        {/if}
      {/if}
    </div>
    <p class="add-hint">
      Slow background pass: re-indexes files missing fingerprints, then verifies
      the rest oldest-first. Yields to scans automatically. Runs on its own after
      scans and daily — this button just starts one now.
    </p>
  </div>

  <!-- ── Directory list ── -->
  {#if loading}
    <div class="loading">Loading…</div>

  {:else if dirs.length === 0}
    <div class="empty">
      <p class="empty-h">No directories added</p>
      <p class="empty-s">Add a directory above to start indexing your local media.</p>
    </div>

  {:else}
    <div class="dir-list">
      {#each dirs as dir (dir.id)}
        <div class="dir-row" class:dim={!dir.enabled}>

          <!-- Status dot -->
          <div class="dot-wrap">
            {#if dir.progress && !dir.progress.done}
              <span class="dot scanning"></span>
            {:else if dir.enabled}
              <span class="dot active"></span>
            {:else}
              <span class="dot off"></span>
            {/if}
          </div>

          <!-- Info -->
          <div class="dir-info">
            <div class="dir-path">{dir.path}</div>

            <div class="dir-meta">
              <span class="meta-chip">{dir.recursive ? 'recursive' : 'top-level'}</span>
              {#if dir.last_scan_count}
                <span class="meta-chip">{dir.last_scan_count.toLocaleString()} files</span>
              {/if}
              {#if dir.extensions?.length}
                <span class="meta-chip">{dir.extensions.join(' ')}</span>
              {/if}
              <span class="meta-date">Last scan: {fmtDate(dir.last_scan_at)}</span>
            </div>

            <!-- Scan progress bar -->
            {#if dir.progress && !dir.progress.done}
              <div class="prog-wrap">
                <div class="prog-track">
                  <div class="prog-fill" style="width:{scanPct(dir.progress)}%"></div>
                </div>
                <span class="prog-label">{scanLabel(dir.progress)}</span>
              </div>
            {/if}
          </div>

          <!-- Actions -->
          <div class="dir-actions">
            <button class="act-btn" title="Re-scan" on:click={() => rescan(dir.id)}>
              <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24"
                   fill="none" stroke="currentColor" stroke-width="2"
                   stroke-linecap="round" stroke-linejoin="round">
                <polyline points="23 4 23 10 17 10"/>
                <polyline points="1 20 1 14 7 14"/>
                <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>
              </svg>
            </button>

            <button class="act-btn" title="{dir.enabled ? 'Disable' : 'Enable'}"
                    on:click={() => toggle(dir.id, dir.enabled)}>
              {#if dir.enabled}
                <!-- Toggle ON -->
                <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24"
                     fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <rect x="1" y="5" width="22" height="14" rx="7"/>
                  <circle cx="16" cy="12" r="3" fill="currentColor"/>
                </svg>
              {:else}
                <!-- Toggle OFF -->
                <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24"
                     fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <rect x="1" y="5" width="22" height="14" rx="7"/>
                  <circle cx="8" cy="12" r="3" fill="currentColor"/>
                </svg>
              {/if}
            </button>

            <button class="act-btn danger" title="Remove" on:click={() => remove(dir.id, dir.path)}>
              <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24"
                   fill="none" stroke="currentColor" stroke-width="2"
                   stroke-linecap="round" stroke-linejoin="round">
                <polyline points="3 6 5 6 21 6"/>
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/>
              </svg>
            </button>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .page {
    padding: 0;
    max-width: 900px;
  }

  /* ── Header ──────────────────────────────────── */
  .header {
    padding: 32px 28px 0;
    border-bottom: 1px solid #131313;
    padding-bottom: 24px;
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

  /* ── Add card ────────────────────────────────── */
  .add-card {
    padding: 24px 28px;
    border-bottom: 1px solid #131313;
  }

  .card-label {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.09em;
    color: #444;
    margin-bottom: 12px;
  }

  .add-row {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }

  .path-inp {
    flex: 1;
    min-width: 260px;
    background: #0c0c0c;
    border: 1px solid #1c1c1c;
    color: #d0d0d0;
    border-radius: 6px;
    padding: 8px 12px;
    font-size: 13px;
    outline: none;
    font-family: 'JetBrains Mono', monospace;
    transition: border-color 0.12s;
  }

  .path-inp:focus { border-color: #333; }
  .path-inp::placeholder { color: #2c2c2c; }

  .chk-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: #555;
    cursor: pointer;
    user-select: none;
  }

  .chk {
    width: 14px;
    height: 14px;
    accent-color: #ededed;
    cursor: pointer;
  }

  .btn-primary {
    background: #ededed;
    border: none;
    color: #000;
    font-size: 12px;
    font-weight: 600;
    padding: 8px 18px;
    border-radius: 6px;
    cursor: pointer;
    transition: background 0.12s;
  }

  .btn-primary:hover    { background: #fff; }
  .btn-primary:disabled { opacity: 0.35; cursor: not-allowed; }

  .btn-ghost {
    background: none;
    border: 1px solid #2a2a2a;
    color: #888;
    font-size: 12px;
    font-weight: 600;
    padding: 7px 16px;
    border-radius: 6px;
    cursor: pointer;
    transition: all 0.12s;
  }

  .btn-ghost:hover { border-color: #444; color: #ccc; }

  .add-hint {
    font-size: 11px;
    color: #2e2e2e;
    margin-top: 10px;
    line-height: 1.6;
  }

  /* ── States ──────────────────────────────────── */
  .loading {
    padding: 40px 28px;
    font-size: 13px;
    color: #333;
  }

  .empty {
    padding: 60px 28px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .empty-h { font-size: 14px; color: #3a3a3a; }
  .empty-s { font-size: 12px; color: #252525; }

  /* ── Directory list ──────────────────────────── */
  .dir-list {
    display: flex;
    flex-direction: column;
  }

  .dir-row {
    display: flex;
    align-items: flex-start;
    gap: 14px;
    padding: 18px 28px;
    border-bottom: 1px solid #0e0e0e;
    transition: background 0.12s;
  }

  .dir-row:hover { background: #060606; }
  .dir-row.dim   { opacity: 0.45; }

  /* Status dots */
  .dot-wrap  { padding-top: 4px; flex-shrink: 0; }

  .dot {
    display: block;
    width: 7px;
    height: 7px;
    border-radius: 50%;
  }

  .dot.active   { background: #22c55e; }
  .dot.scanning { background: #3b82f6; animation: blink 1.2s ease-in-out infinite; }
  .dot.off      { background: #2a2a2a; }

  @keyframes blink { 0%,100%{opacity:1} 50%{opacity:0.2} }

  /* Info */
  .dir-info {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .dir-path {
    font-size: 13px;
    font-weight: 500;
    color: #c0c0c0;
    font-family: 'JetBrains Mono', monospace;
    word-break: break-all;
  }

  .dir-meta {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    align-items: center;
  }

  .meta-chip {
    font-size: 10.5px;
    padding: 2px 7px;
    background: #111;
    border: 1px solid #1c1c1c;
    border-radius: 3px;
    color: #555;
  }

  .meta-date {
    font-size: 10.5px;
    color: #333;
  }

  /* Progress */
  .prog-wrap {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 2px;
  }

  .prog-track {
    flex: 1;
    max-width: 240px;
    height: 2px;
    background: #1a1a1a;
    border-radius: 1px;
    overflow: hidden;
  }

  .prog-fill {
    height: 100%;
    background: #3b82f6;
    border-radius: 1px;
    transition: width 0.4s ease;
  }

  .prog-label {
    font-size: 10.5px;
    color: #555;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  /* Actions */
  .dir-actions {
    display: flex;
    gap: 4px;
    flex-shrink: 0;
    padding-top: 1px;
  }

  .act-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    background: none;
    border: 1px solid #1a1a1a;
    border-radius: 5px;
    color: #444;
    cursor: pointer;
    transition: all 0.12s;
  }

  .act-btn:hover { border-color: #333; color: #999; background: #0e0e0e; }
  .act-btn.danger:hover { border-color: rgba(239,68,68,0.4); color: #f87171; background: rgba(239,68,68,0.08); }
</style>

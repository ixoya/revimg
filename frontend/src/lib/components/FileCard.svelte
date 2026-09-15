<script>
  import { createEventDispatcher } from 'svelte';
  import { thumbUrl, openUrl, deleteFile } from '$lib/api.js';
  import { addToast } from '$lib/stores/app.js';

  export let result; // { file, score, score_phash, score_ahash, score_dhash, score_color, score_video }

  const dispatch = createEventDispatcher();

  $: f    = result.file;
  $: pct  = Math.round(result.score * 100);

  let hover = false;

  // ── Score colour ────────────────────────────────────────────────────────────
  function scoreColor(s) {
    if (s >= 0.95) return '#22c55e';
    if (s >= 0.85) return '#3b82f6';
    if (s >= 0.70) return '#f59e0b';
    return '#ef4444';
  }

  $: badgeColor = scoreColor(result.score);

  // ── Sub-score display ───────────────────────────────────────────────────────
  $: breakdown = [
    { label: 'pHash', val: result.score_phash },
    { label: 'dHash', val: result.score_dhash },
    { label: 'aHash', val: result.score_ahash },
    { label: 'Color', val: result.score_color },
    result.score_video ? { label: 'Video', val: result.score_video } : null,
  ].filter(Boolean);

  // ── Formatters ──────────────────────────────────────────────────────────────
  function fmtBytes(n) {
    if (!n) return '0 B';
    const u = ['B','KB','MB','GB'];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return `${n < 10 && i > 0 ? n.toFixed(1) : Math.round(n)} ${u[i]}`;
  }

  function fmtDate(unix) {
    if (!unix) return '';
    return new Date(unix * 1000).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
  }

  function fmtDuration(ms) {
    if (!ms) return '';
    const s = Math.floor(ms / 1000);
    const m = Math.floor(s / 60);
    const h = Math.floor(m / 60);
    if (h > 0) return `${h}:${String(m % 60).padStart(2,'0')}:${String(s % 60).padStart(2,'0')}`;
    return `${m}:${String(s % 60).padStart(2,'0')}`;
  }

  // ── Actions ─────────────────────────────────────────────────────────────────
  function openInBrowser() {
    window.open(openUrl(f.id), '_blank', 'noopener');
  }

  async function copyPath() {
    try {
      await navigator.clipboard.writeText(f.path);
      addToast('Path copied', 'success');
    } catch {
      addToast(f.path, 'info', 6000);
    }
  }

  async function removeFromIndex() {
    try {
      await deleteFile(f.id);
      addToast(`Removed ${f.filename}`, 'info');
      dispatch('removed', f.id);
    } catch (e) {
      addToast(e.message, 'error');
    }
  }
</script>

<!-- svelte-ignore a11y-no-static-element-interactions -->
<div
  class="card"
  on:mouseenter={() => hover = true}
  on:mouseleave={() => hover = false}
>
  <!-- Thumbnail -->
  <div class="thumb-wrap">
    <img
      src={thumbUrl(f.id)}
      alt={f.filename}
      loading="lazy"
      class="thumb"
      on:error={e => { e.target.style.visibility = 'hidden'; }}
    />

    <!-- Video badge -->
    {#if f.file_type === 'video'}
      <span class="badge-video">
        <svg xmlns="http://www.w3.org/2000/svg" width="9" height="9" viewBox="0 0 24 24"
             fill="currentColor"><polygon points="5 3 19 12 5 21 5 3"/></svg>
        VIDEO
      </span>
    {/if}

    <!-- Duration badge -->
    {#if f.duration_ms}
      <span class="badge-dur">{fmtDuration(f.duration_ms)}</span>
    {/if}

    <!-- Similarity badge -->
    <span class="badge-score" style="--c:{badgeColor}">{pct}%</span>

    <!-- Hover overlay -->
    {#if hover}
      <div class="overlay">
        <!-- Actions row -->
        <div class="ov-actions">
          <button class="ov-btn" title="Open file" on:click|stopPropagation={openInBrowser}>
            <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24"
                 fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>
              <polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/>
            </svg>
          </button>
          <button class="ov-btn" title="Copy path" on:click|stopPropagation={copyPath}>
            <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24"
                 fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
              <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
            </svg>
          </button>
          <button class="ov-btn ov-danger" title="Remove from index" on:click|stopPropagation={removeFromIndex}>
            <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24"
                 fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="3 6 5 6 21 6"/>
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/>
            </svg>
          </button>
        </div>

        <!-- Score breakdown -->
        <div class="ov-breakdown">
          {#each breakdown as b}
            {#if b.val != null}
              <div class="ov-row">
                <span class="ov-label">{b.label}</span>
                <span class="ov-val" style="color:{scoreColor(b.val)}">{Math.round(b.val * 100)}%</span>
              </div>
            {/if}
          {/each}
        </div>
      </div>
    {/if}
  </div>

  <!-- Card footer info -->
  <div class="info">
    <p class="filename" title={f.path}>{f.filename}</p>
    <p class="meta">
      {#if f.width && f.height}<span>{f.width}×{f.height}</span>{/if}
      {#if f.width && f.height}<span class="sep">·</span>{/if}
      <span>{fmtBytes(f.size)}</span>
      {#if f.codec}<span class="sep">·</span><span class="mono">{f.codec}</span>{/if}
    </p>
    {#if f.modified_at}
      <p class="date">{fmtDate(f.modified_at)}</p>
    {/if}
    <!-- Dominant colours -->
    {#if f.dominant_colors?.length}
      <div class="colors">
        {#each f.dominant_colors.slice(0, 5) as hex}
          <span class="color-dot" style="background:{hex}" title={hex}></span>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
  .card {
    background: #0a0a0a;
    display: flex;
    flex-direction: column;
    cursor: default;
    transition: background 0.12s ease;
  }

  .card:hover { background: #0f0f0f; }

  /* ── Thumbnail ────────────────────────────────── */
  .thumb-wrap {
    position: relative;
    aspect-ratio: 1;
    background: #111;
    overflow: hidden;
  }

  .thumb {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
    transition: transform 0.22s ease;
  }

  .card:hover .thumb { transform: scale(1.04); }

  /* ── Badges ──────────────────────────────────── */
  .badge-score {
    position: absolute;
    top: 7px;
    right: 7px;
    background: rgba(0,0,0,0.82);
    border: 1.5px solid var(--c);
    color: var(--c);
    font-size: 10px;
    font-weight: 700;
    padding: 2px 6px;
    border-radius: 4px;
    font-variant-numeric: tabular-nums;
    letter-spacing: 0.04em;
    backdrop-filter: blur(3px);
  }

  .badge-video {
    position: absolute;
    bottom: 7px;
    left: 7px;
    display: flex;
    align-items: center;
    gap: 3px;
    font-size: 9px;
    font-weight: 700;
    letter-spacing: 0.07em;
    padding: 2px 5px;
    border-radius: 3px;
    background: rgba(59,130,246,0.15);
    border: 1px solid rgba(59,130,246,0.35);
    color: #60a5fa;
  }

  .badge-dur {
    position: absolute;
    bottom: 7px;
    right: 7px;
    background: rgba(0,0,0,0.7);
    color: #aaa;
    font-size: 10px;
    padding: 2px 5px;
    border-radius: 3px;
    font-family: 'JetBrains Mono', monospace;
  }

  /* ── Hover overlay ───────────────────────────── */
  .overlay {
    position: absolute;
    inset: 0;
    background: rgba(0,0,0,0.72);
    backdrop-filter: blur(3px);
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    padding: 9px;
    animation: fade 0.14s ease;
  }

  @keyframes fade { from { opacity: 0; } to { opacity: 1; } }

  .ov-actions { display: flex; gap: 5px; }

  .ov-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    background: rgba(255,255,255,0.08);
    border: 1px solid rgba(255,255,255,0.12);
    border-radius: 5px;
    color: #ccc;
    transition: background 0.12s;
  }

  .ov-btn:hover { background: rgba(255,255,255,0.18); color: #fff; }
  .ov-danger:hover { background: rgba(239,68,68,0.25); border-color: rgba(239,68,68,0.4); color: #f87171; }

  .ov-breakdown { display: flex; flex-direction: column; gap: 2px; }

  .ov-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .ov-label { font-size: 10px; color: #777; }
  .ov-val   { font-size: 10px; font-weight: 600; font-variant-numeric: tabular-nums; }

  /* ── Card info ───────────────────────────────── */
  .info {
    padding: 9px 11px 10px;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }

  .filename {
    font-size: 11.5px;
    font-weight: 500;
    color: #c0c0c0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .meta {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 10.5px;
    color: #4a4a4a;
  }

  .mono { font-family: 'JetBrains Mono', monospace; font-size: 10px; }
  .sep  { color: #2a2a2a; }

  .date {
    font-size: 10px;
    color: #333;
  }

  .colors {
    display: flex;
    gap: 3px;
    margin-top: 2px;
  }

  .color-dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    border: 1px solid rgba(255,255,255,0.07);
    flex-shrink: 0;
  }
</style>

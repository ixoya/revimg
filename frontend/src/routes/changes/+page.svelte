<script>
  import { onMount, onDestroy } from 'svelte';

  let events = [];
  let loading = true;
  let timer;

  async function poll() {
    try {
      const resp = await fetch('/api/changes?n=500');
      if (resp.ok) {
        events = await resp.json();
      }
    } catch { /* ignore */ }
    loading = false;
  }

  onMount(() => {
    poll();
    timer = setInterval(poll, 1000);
  });
  onDestroy(() => clearInterval(timer));

  function label(ev) {
    switch (ev.type) {
      case 'created':        return 'Created';
      case 'deleted':        return 'Deleted';
      case 'modified':       return 'Modified';
      case 'renamed_old':    return 'Renamed (old)';
      case 'renamed_new':    return 'Renamed (new)';
      case 'error':          return 'Error';
      case 'scan_started':   return 'Scan started';
      case 'scan_completed': return 'Scan completed';
      case 'repair_started':   return 'Repair started';
      case 'repair_completed': return 'Repair completed';
      default:               return ev.type;
    }
  }

  function cls(ev) {
    switch (ev.type) {
      case 'deleted':     return 'del';
      case 'error':       return 'err';
      case 'created':     return 'add';
      case 'modified':    return 'mod';
      case 'renamed_old': return 'del';
      case 'renamed_new': return 'add';
      default:            return '';
    }
  }

  function ago(t) {
    const s = Math.floor((Date.now() - new Date(t).getTime()) / 1000);
    if (s < 5)   return 'just now';
    if (s < 60)  return s + 's ago';
    const m = Math.floor(s / 60);
    if (m < 60)  return m + 'm ago';
    const h = Math.floor(m / 60);
    return h + 'h ago';
  }
</script>

<div class="page">
  <div class="header">
    <h1 class="title">Activity</h1>
    <p class="sub">Recent file system changes detected by the watcher and scanner.</p>
  </div>

  {#if loading}
    <div class="loading">Loading…</div>
  {:else if events.length === 0}
    <div class="empty">No changes recorded yet. Start a scan or modify files in a watched directory.</div>
  {:else}
    <div class="list" id="event-list">
      {#each events as ev}
        <div class="row {cls(ev)}">
          <span class="badge {cls(ev)}">{label(ev)}</span>
          <span class="path" title={ev.path}>{ev.path}</span>
          {#if ev.message}
            <span class="msg">{ev.message}</span>
          {/if}
          <span class="time">{ago(ev.time)}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .page {
    max-width: 900px;
    padding-bottom: 60px;
  }

  .header {
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

  .loading, .empty {
    padding: 40px 28px;
    font-size: 13px;
    color: #333;
  }

  #event-list {
    display: flex;
    flex-direction: column;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 8px 28px;
    font-size: 12.5px;
    border-bottom: 1px solid #0a0a0a;
    transition: background 0.1s;
  }

  .row:hover { background: #060606; }

  .badge {
    flex-shrink: 0;
    font-size: 10px;
    font-weight: 600;
    padding: 2px 7px;
    border-radius: 3px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .badge.add { background: #0a1a0a; color: #2e7d32; }
  .badge.del { background: #1a0a0a; color: #c62828; }
  .badge.mod { background: #0a0a1a; color: #1565c0; }
  .badge.err { background: #1a0a0a; color: #e65100; }

  .path {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: #888;
    font-family: 'JetBrains Mono', monospace;
    font-size: 11.5px;
  }

  .msg {
    flex-shrink: 0;
    color: #555;
    font-size: 11px;
    font-style: italic;
  }

  .time {
    flex-shrink: 0;
    color: #444;
    font-size: 11px;
    font-variant-numeric: tabular-nums;
    width: 60px;
    text-align: right;
  }
</style>

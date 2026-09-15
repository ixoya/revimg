<script>
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { stats, status, toasts } from '$lib/stores/app.js';
  import { getStats, getStatus } from '$lib/api.js';
  import Toast from '$lib/components/Toast.svelte';

  // ── SVG icons ──────────────────────────────────────────────────────────────

  const icons = {
    search: `<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>`,
    folder: `<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>`,
    gear:   `<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`,
    activity: `<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>`,
  };

  const nav = [
    { href: '/',            label: 'Search',    icon: icons.search },
    { href: '/directories', label: 'Libraries', icon: icons.folder },
    { href: '/changes',     label: 'Activity',  icon: icons.activity },
    { href: '/settings',    label: 'Settings',  icon: icons.gear   },
  ];

  // ── Data polling ────────────────────────────────────────────────────────────

  let poll;
  onMount(() => {
    const fetch = async () => {
      try {
        const [s, st] = await Promise.all([getStats(), getStatus()]);
        stats.set(s);
        status.set(st);
      } catch { /* server may be starting */ }
    };
    fetch();
    poll = setInterval(fetch, 5000);
    return () => clearInterval(poll);
  });

  // ── Helpers ─────────────────────────────────────────────────────────────────

  $: path = $page?.url?.pathname ?? '/';

  function fmtN(n) {
    return n == null ? '—' : n.toLocaleString();
  }

  function fmtBytes(n) {
    if (!n) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return `${n < 10 && i > 0 ? n.toFixed(1) : Math.round(n)} ${u[i]}`;
  }

  $: scanning = $status?.scans
    ? Object.values($status.scans).some(s => !s.done)
    : false;
</script>

<div class="app">
  <!-- ── Sidebar ── -->
  <aside class="sidebar">
    <div class="logo">
      <span class="logo-mark">◈</span>
      <span class="logo-name">revimg</span>
    </div>

    <nav class="nav">
      {#each nav as item}
        <a href={item.href} class="nav-link" class:active={path === item.href}>
          <span class="nav-icon">{@html item.icon}</span>
          {item.label}
        </a>
      {/each}
    </nav>

    <div class="sidebar-footer">
      {#if $stats}
        <div class="meta-row">
          <span class="meta-key">Files</span>
          <span class="meta-val">{fmtN($stats.total_files)}</span>
        </div>
        <div class="meta-row">
          <span class="meta-key">Images</span>
          <span class="meta-val">{fmtN($stats.total_images)}</span>
        </div>
        <div class="meta-row">
          <span class="meta-key">Videos</span>
          <span class="meta-val">{fmtN($stats.total_videos)}</span>
        </div>
        <div class="meta-row">
          <span class="meta-key">Size</span>
          <span class="meta-val">{fmtBytes($stats.total_size)}</span>
        </div>
        {#if scanning}
          <div class="scanning">
            <span class="scan-dot"></span>
            Indexing…
          </div>
        {/if}
      {:else}
        <div class="meta-row"><span class="meta-key" style="color:#3a3a3a">Connecting…</span></div>
      {/if}
    </div>
  </aside>

  <!-- ── Main content ── -->
  <main class="main">
    <slot />
  </main>
</div>

<!-- ── Toast stack ── -->
<div class="toasts">
  {#each $toasts as t (t.id)}
    <Toast msg={t.msg} type={t.type} />
  {/each}
</div>

<style>
  /* ── Global reset ─────────────────────────────── */
  :global(*, *::before, *::after) { box-sizing: border-box; margin: 0; padding: 0; }
  :global(html)  { font-size: 14px; }
  :global(body)  {
    background: #000;
    color: #ededed;
    font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
  }
  :global(a)       { color: inherit; text-decoration: none; }
  :global(button)  { font-family: inherit; cursor: pointer; }
  :global(input, select, textarea) { font-family: inherit; }
  :global(::-webkit-scrollbar)       { width: 5px; height: 5px; }
  :global(::-webkit-scrollbar-track) { background: transparent; }
  :global(::-webkit-scrollbar-thumb) { background: #2a2a2a; border-radius: 3px; }
  :global(::-webkit-scrollbar-thumb:hover) { background: #444; }
  :global(::selection)  { background: rgba(255,255,255,0.12); }

  /* ── Layout ───────────────────────────────────── */
  .app {
    display: flex;
    height: 100vh;
    overflow: hidden;
  }

  /* ── Sidebar ──────────────────────────────────── */
  .sidebar {
    width: 210px;
    flex-shrink: 0;
    background: #080808;
    border-right: 1px solid #161616;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .logo {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 18px 16px 15px;
    border-bottom: 1px solid #161616;
    flex-shrink: 0;
  }

  .logo-mark {
    font-size: 17px;
    color: #fff;
    line-height: 1;
    flex-shrink: 0;
  }

  .logo-name {
    font-size: 14px;
    font-weight: 600;
    letter-spacing: -0.025em;
    color: #fff;
  }

  .nav {
    padding: 10px 8px;
    display: flex;
    flex-direction: column;
    gap: 1px;
    flex: 1;
  }

  .nav-link {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 7px 10px;
    border-radius: 5px;
    color: #555;
    font-size: 13px;
    font-weight: 500;
    transition: color 0.12s ease, background 0.12s ease;
  }

  .nav-link:hover  { color: #aaa; background: #111; }
  .nav-link.active { color: #ededed; background: #161616; }

  .nav-icon {
    display: flex;
    align-items: center;
    flex-shrink: 0;
  }

  .sidebar-footer {
    padding: 14px 16px;
    border-top: 1px solid #161616;
    display: flex;
    flex-direction: column;
    gap: 5px;
    flex-shrink: 0;
  }

  .meta-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .meta-key { font-size: 11px; color: #444; }
  .meta-val { font-size: 11px; color: #666; font-variant-numeric: tabular-nums; }

  .scanning {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    color: #3b82f6;
    margin-top: 5px;
  }

  .scan-dot {
    width: 6px;
    height: 6px;
    background: #3b82f6;
    border-radius: 50%;
    animation: blink 1.2s ease-in-out infinite;
    flex-shrink: 0;
  }

  @keyframes blink {
    0%, 100% { opacity: 1; }
    50%       { opacity: 0.25; }
  }

  /* ── Main ─────────────────────────────────────── */
  .main {
    flex: 1;
    overflow-y: auto;
    overflow-x: hidden;
    background: #000;
  }

  /* ── Toasts ───────────────────────────────────── */
  .toasts {
    position: fixed;
    bottom: 20px;
    right: 20px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    z-index: 9999;
    pointer-events: none;
  }
</style>

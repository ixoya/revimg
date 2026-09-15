<script>
  export let msg;
  export let type = 'info'; // 'info' | 'success' | 'error' | 'warning'

  const COLOR = {
    info:    '#3b82f6',
    success: '#22c55e',
    error:   '#ef4444',
    warning: '#f59e0b',
  };

  $: color = COLOR[type] ?? COLOR.info;
</script>

<div class="toast" style="--c:{color}">
  <span class="dot"></span>
  <span class="msg">{msg}</span>
</div>

<style>
  .toast {
    pointer-events: auto;
    display: flex;
    align-items: center;
    gap: 9px;
    background: #111;
    border: 1px solid #1f1f1f;
    border-left: 3px solid var(--c);
    border-radius: 7px;
    padding: 10px 14px;
    min-width: 200px;
    max-width: 360px;
    box-shadow: 0 8px 32px rgba(0,0,0,0.6);
    animation: slide-in 0.18s ease;
  }

  @keyframes slide-in {
    from { opacity: 0; transform: translateX(16px); }
    to   { opacity: 1; transform: translateX(0); }
  }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--c);
    flex-shrink: 0;
  }

  .msg {
    font-size: 12px;
    color: #b0b0b0;
    line-height: 1.4;
  }
</style>

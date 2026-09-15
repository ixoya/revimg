<script>
  import { createEventDispatcher } from 'svelte';

  const dispatch = createEventDispatcher();

  let dragging = false;
  let input;

  const ACCEPT_IMG = '.jpg,.jpeg,.png,.gif,.bmp,.tiff,.tif,.webp';
  const ACCEPT_VID = '.mp4,.mkv,.mov,.avi,.webm,.flv,.wmv,.m4v,.mpg,.mpeg,.ts,.mts';

  function onDragOver(e) {
    e.preventDefault();
    dragging = true;
  }

  function onDragLeave(e) {
    if (!e.currentTarget.contains(e.relatedTarget)) dragging = false;
  }

  function onDrop(e) {
    e.preventDefault();
    dragging = false;
    const file = e.dataTransfer?.files?.[0];
    if (file) dispatch('file', file);
  }

  function onPick(e) {
    const file = e.target.files?.[0];
    if (file) dispatch('file', file);
    e.target.value = '';
  }

  function onClick() { input.click(); }

  function onKeyDown(e) {
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onClick(); }
  }
</script>

<!-- svelte-ignore a11y-no-static-element-interactions -->
<div
  class="zone"
  class:dragging
  on:dragover={onDragOver}
  on:dragleave={onDragLeave}
  on:drop={onDrop}
  on:click={onClick}
  on:keydown={onKeyDown}
  role="button"
  tabindex="0"
  aria-label="Upload image or video for reverse search"
>
  <input
    bind:this={input}
    type="file"
    accept="{ACCEPT_IMG},{ACCEPT_VID}"
    style="display:none"
    on:change={onPick}
  />

  <!-- Upload arrow icon -->
  <div class="icon" class:dragging>
    <svg xmlns="http://www.w3.org/2000/svg" width="36" height="36" viewBox="0 0 24 24"
         fill="none" stroke="currentColor" stroke-width="1.25"
         stroke-linecap="round" stroke-linejoin="round">
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
      <polyline points="17 8 12 3 7 8"/>
      <line x1="12" y1="3" x2="12" y2="15"/>
    </svg>
  </div>

  <p class="headline">
    {dragging ? 'Drop to search' : 'Drop image or video here'}
  </p>
  <p class="sub">
    or <span class="link">click to browse</span>
  </p>
  <p class="formats">
    JPEG · PNG · GIF · BMP · TIFF · WebP &nbsp;·&nbsp; MP4 · MKV · MOV · AVI · WebM
  </p>
</div>

<style>
  .zone {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    width: 100%;
    max-width: 520px;
    padding: 60px 48px;
    border: 1.5px dashed #222;
    border-radius: 14px;
    cursor: pointer;
    transition: border-color 0.18s ease, background 0.18s ease;
    outline: none;
    user-select: none;
  }

  .zone:hover  { border-color: #383838; background: #080808; }
  .zone:focus  { border-color: #444; }
  .zone.dragging {
    border-color: #3b82f6;
    background: rgba(59,130,246,0.04);
  }

  .icon {
    color: #2a2a2a;
    transition: color 0.18s ease, transform 0.18s ease;
  }

  .zone:hover .icon { color: #444; }
  .zone.dragging .icon { color: #3b82f6; transform: translateY(-3px); }

  .headline {
    font-size: 15px;
    font-weight: 500;
    color: #666;
    transition: color 0.18s;
  }

  .zone:hover .headline  { color: #888; }
  .zone.dragging .headline { color: #60a5fa; }

  .sub {
    font-size: 13px;
    color: #333;
  }

  .link {
    color: #555;
    text-decoration: underline;
    text-decoration-color: #333;
    transition: color 0.15s;
  }

  .zone:hover .link { color: #777; }

  .formats {
    font-size: 11px;
    color: #2a2a2a;
    text-align: center;
    line-height: 1.7;
    margin-top: 4px;
  }
</style>

<script>
  import { onMount, createEventDispatcher } from 'svelte';
  import Player from './Player.svelte';

  export let shareInfo;
  export let token;

  const dispatch = createEventDispatcher();

  let needsPassword = shareInfo.requiresPassword;
  let passwordInput = '';
  let passwordError = '';
  let passwordLoading = false;

  $: showPasswordForm = needsPassword;
  $: isSeasonOrSeries = shareInfo.itemType === 'Season' || shareInfo.itemType === 'Series';

  let isPlaying = false;
  let playbackData = null;
  let playError = '';
  let copiedMsg = '';
  let copyBusy = false;
  let imageLoaded = !shareInfo.posterUrl;

  // Video quality presets. MaxBitrate == null means "don't cap" (Auto).
  const qualityPresets = [
    { id: 'auto',  label: 'Auto (up to 8 Mbps)', maxBitrate: 8000000, maxHeight: null },
    { id: '1080p', label: '1080p — 8 Mbps',      maxBitrate: 8000000, maxHeight: 1080 },
    { id: '720p',  label: '720p — 4 Mbps',       maxBitrate: 4000000, maxHeight: 720 },
    { id: '480p',  label: '480p — 2 Mbps',       maxBitrate: 2000000, maxHeight: 480 },
  ];
  let selectedQualityId = 'auto';
  let selectedAudioIndex = null;

  // When the share info loads, pick the default audio track: prefer
  // IsDefault=true, fall back to the first track, leave null if none.
  $: if (shareInfo.audioTracks && shareInfo.audioTracks.length > 0 && selectedAudioIndex === null) {
    const def = shareInfo.audioTracks.find(t => t.isDefault);
    selectedAudioIndex = def ? def.index : shareInfo.audioTracks[0].index;
  }

  function audioLabel(track) {
    const bits = [];
    if (track.language) bits.push(track.language.toUpperCase());
    if (track.codec)    bits.push(track.codec.toUpperCase());
    if (track.channels) bits.push(track.channels === 2 ? 'Stereo' : `${track.channels}ch`);
    const tail = bits.join(' · ');
    if (track.title) return tail ? `${track.title} (${tail})` : track.title;
    return tail || `Track ${track.index}`;
  }

  function buildStreamParams() {
    const q = qualityPresets.find(p => p.id === selectedQualityId) || qualityPresets[0];
    const p = new URLSearchParams();
    if (q.maxBitrate) p.set('VideoBitrate', String(q.maxBitrate));
    if (q.maxBitrate) p.set('MaxStreamingBitrate', String(q.maxBitrate));
    if (q.maxHeight)  p.set('MaxHeight', String(q.maxHeight));
    if (selectedAudioIndex !== null && selectedAudioIndex !== undefined) {
      p.set('AudioStreamIndex', String(selectedAudioIndex));
    }
    return p.toString();
  }

  function appendParams(url, paramStr) {
    if (!paramStr) return url;
    return url + (url.includes('?') ? '&' : '?') + paramStr;
  }
  let showFullCast = false;
  let currentPlayingTitle = '';

  // Episode list for Season/Series
  let episodes = [];
  let episodesLoading = false;
  let episodesError = '';

  onMount(async () => {
    const timeout = setTimeout(() => {
      imageLoaded = true;
    }, 500);

    // Load episodes for Season/Series
    if (isSeasonOrSeries && !needsPassword) {
      await loadEpisodes();
    }

    return () => clearTimeout(timeout);
  });

  // Load episodes when password is validated
  $: if (isSeasonOrSeries && !needsPassword && episodes.length === 0 && !episodesLoading) {
    loadEpisodes();
  }

  async function loadEpisodes() {
    if (!isSeasonOrSeries) return;
    episodesLoading = true;
    episodesError = '';
    try {
      const response = await fetch(`/api/public/shares/${token}/episodes`, {
        credentials: 'include'
      });
      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        episodesError = data.error || 'Failed to load episodes';
        return;
      }
      const data = await response.json();
      episodes = data.episodes || [];
    } catch (e) {
      episodesError = 'Failed to load episodes';
    } finally {
      episodesLoading = false;
    }
  }

  function formatDuration(seconds) {
    if (!seconds) return '';
    const hours = Math.floor(seconds / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    if (hours > 0) return `${hours}h ${minutes}m`;
    return `${minutes}m`;
  }

  function formatExpiry(expiresAt) {
    const expiry = new Date(expiresAt);
    const now = new Date();
    const diff = expiry - now;
    if (diff <= 0) return 'Expired';
    const hours = Math.floor(diff / (1000 * 60 * 60));
    const days = Math.floor(hours / 24);
    if (days > 0) return `${days}d ${hours % 24}h`;
    if (hours > 0) return `${hours}h`;
    return `${Math.floor(diff / (1000 * 60))}m`;
  }

  function formatRating(rating) {
    return rating.toFixed(1);
  }

  function getPlaysRemaining() {
    if (!shareInfo.maxTotalPlays) return null;
    return shareInfo.maxTotalPlays - shareInfo.totalPlays;
  }

  function getPlaysPercentage() {
    if (!shareInfo.maxTotalPlays) return 100;
    return ((shareInfo.maxTotalPlays - shareInfo.totalPlays) / shareInfo.maxTotalPlays) * 100;
  }

  async function submitPassword() {
    if (!passwordInput.trim()) {
      passwordError = 'Please enter a password';
      return;
    }
    passwordLoading = true;
    passwordError = '';
    try {
      const response = await fetch(`/api/public/shares/${token}/password`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password: passwordInput }),
        credentials: 'include'
      });
      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        passwordError = data.error || 'Incorrect password';
      } else {
        needsPassword = false;
        passwordInput = '';
      }
    } catch (e) {
      passwordError = 'Failed to validate password';
    } finally {
      passwordLoading = false;
    }
  }

  async function startPlayback() {
    playError = '';
    try {
      const response = await fetch(`/api/public/shares/${token}/play`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include'
      });
      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        playError = data.error || 'Failed to start playback';
        return;
      }
      playbackData = await response.json();
      playbackData.playbackUrl = appendParams(playbackData.playbackUrl, buildStreamParams());
      currentPlayingTitle = shareInfo.title;
      isPlaying = true;
    } catch (e) {
      playError = 'Failed to connect to server';
    }
  }

  async function startEpisodePlayback(episode) {
    playError = '';
    try {
      const response = await fetch(`/api/public/shares/${token}/episodes/${episode.id}/play`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include'
      });
      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        playError = data.error || 'Failed to start playback';
        return;
      }
      playbackData = await response.json();
      playbackData.playbackUrl = appendParams(playbackData.playbackUrl, buildStreamParams());
      currentPlayingTitle = `E${episode.indexNumber}: ${episode.name}`;
      isPlaying = true;
    } catch (e) {
      playError = 'Failed to connect to server';
    }
  }

  function handlePlayerClose() {
    isPlaying = false;
    playbackData = null;
    currentPlayingTitle = '';
  }

  // Shared helper: POST to a /play endpoint and copy the returned URL.
  async function copyStreamUrlFromEndpoint(endpoint, label) {
    if (copyBusy) return;
    copyBusy = true;
    playError = '';
    copiedMsg = '';
    try {
      const response = await fetch(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include'
      });
      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        playError = data.error || 'Failed to create stream URL';
        return;
      }
      const data = await response.json();
      const finalUrl = appendParams(data.playbackUrl, buildStreamParams());
      await navigator.clipboard.writeText(finalUrl);
      copiedMsg = `${label} copied`;
      setTimeout(() => { copiedMsg = ''; }, 3000);
    } catch (e) {
      playError = 'Failed to copy stream URL';
    } finally {
      copyBusy = false;
    }
  }

  function copyStreamUrl() {
    return copyStreamUrlFromEndpoint(
      `/api/public/shares/${token}/play`,
      'Stream URL'
    );
  }

  function copyEpisodeStreamUrl(episode, event) {
    event.stopPropagation(); // don't trigger episode-card's Play handler
    return copyStreamUrlFromEndpoint(
      `/api/public/shares/${token}/episodes/${episode.id}/play`,
      `E${episode.indexNumber || '?'} stream URL`
    );
  }

  function handleImageLoad() {
    imageLoaded = true;
  }

  function handleImageError() {
    imageLoaded = true;
  }
</script>

<div class="share-container">
  {#if isPlaying && playbackData}
    <Player {playbackData} title={currentPlayingTitle || shareInfo.title} on:close={handlePlayerClose} />
  {:else}
    <div class="backdrop-container">
      <div class="backdrop" style="background-image: url('{shareInfo.backdropUrl || shareInfo.posterUrl}')"></div>
      <div class="backdrop-gradient"></div>
    </div>

    <div class="content" class:loaded={imageLoaded}>
      <!-- Logo (if available) -->
      {#if shareInfo.logoUrl}
        <div class="logo-container">
          <img src="{shareInfo.logoUrl}?maxWidth=500" alt="" class="logo" />
        </div>
      {/if}

      <div class="media-layout">
        <!-- Poster -->
        {#if shareInfo.posterUrl}
          <div class="poster-section">
            <div class="poster-wrapper">
              <img
                src="{shareInfo.posterUrl}?maxWidth=400"
                alt={shareInfo.title}
                on:load={handleImageLoad}
                on:error={handleImageError}
                class:loaded={imageLoaded}
              />
              {#if shareInfo.videoQuality}
                <div class="quality-badge">{shareInfo.videoQuality.resolution}</div>
              {/if}
            </div>
          </div>
        {/if}

        <!-- Info Section -->
        <div class="info-section">
          <!-- Title (only if no logo) -->
          {#if !shareInfo.logoUrl}
            <h1 class="title">{shareInfo.title}</h1>
          {/if}

          <!-- Meta badges row -->
          <div class="meta-badges">
            {#if shareInfo.year}
              <span class="badge">{shareInfo.year}</span>
            {/if}
            {#if shareInfo.officialRating}
              <span class="badge rating-badge">{shareInfo.officialRating}</span>
            {/if}
            {#if shareInfo.runtimeSeconds}
              <span class="badge">{formatDuration(shareInfo.runtimeSeconds)}</span>
            {/if}
            {#if shareInfo.videoQuality}
              <span class="badge quality">{shareInfo.videoQuality.resolution}</span>
            {/if}
          </div>

          <!-- Ratings -->
          {#if shareInfo.communityRating > 0 || shareInfo.criticRating > 0}
            <div class="ratings-row">
              {#if shareInfo.communityRating > 0}
                <div class="rating-item">
                  <div class="rating-icon star">
                    <svg viewBox="0 0 24 24" fill="currentColor">
                      <path d="M12 17.27L18.18 21l-1.64-7.03L22 9.24l-7.19-.61L12 2 9.19 8.63 2 9.24l5.46 4.73L5.82 21z"/>
                    </svg>
                  </div>
                  <div class="rating-value">{formatRating(shareInfo.communityRating)}</div>
                  <div class="rating-label">User Score</div>
                </div>
              {/if}
              {#if shareInfo.criticRating > 0}
                <div class="rating-item">
                  <div class="rating-icon tomato" class:fresh={shareInfo.criticRating >= 60}>
                    {shareInfo.criticRating}%
                  </div>
                  <div class="rating-label">Critics</div>
                </div>
              {/if}
            </div>
          {/if}

          <!-- Genres -->
          {#if shareInfo.genres && shareInfo.genres.length > 0}
            <div class="genres">
              {#each shareInfo.genres.slice(0, 4) as genre}
                <span class="genre-tag">{genre}</span>
              {/each}
            </div>
          {/if}

          <!-- Overview -->
          {#if shareInfo.overview}
            <p class="overview">{shareInfo.overview}</p>
          {/if}

          <!-- Directors -->
          {#if shareInfo.directors && shareInfo.directors.length > 0}
            <div class="credits-row">
              <span class="credits-label">Director{shareInfo.directors.length > 1 ? 's' : ''}</span>
              <span class="credits-value">{shareInfo.directors.join(', ')}</span>
            </div>
          {/if}

          <!-- Studios -->
          {#if shareInfo.studios && shareInfo.studios.length > 0}
            <div class="credits-row">
              <span class="credits-label">Studio</span>
              <span class="credits-value">{shareInfo.studios[0]}</span>
            </div>
          {/if}

          <!-- Cast -->
          {#if shareInfo.actors && shareInfo.actors.length > 0}
            <div class="cast-section">
              <div class="cast-header">
                <span class="credits-label">Cast</span>
                {#if shareInfo.actors.length > 4}
                  <button class="show-more-btn" on:click={() => showFullCast = !showFullCast}>
                    {showFullCast ? 'Show less' : `+${shareInfo.actors.length - 4} more`}
                  </button>
                {/if}
              </div>
              <div class="cast-list">
                {#each (showFullCast ? shareInfo.actors : shareInfo.actors.slice(0, 4)) as actor}
                  <div class="actor">
                    <span class="actor-name">{actor.name}</span>
                    {#if actor.role}
                      <span class="actor-role">{actor.role}</span>
                    {/if}
                  </div>
                {/each}
              </div>
            </div>
          {/if}

          <!-- Video Quality Details -->
          {#if shareInfo.videoQuality}
            <div class="quality-info">
              <svg viewBox="0 0 24 24" fill="currentColor">
                <path d="M21 3H3c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h18c1.1 0 2-.9 2-2V5c0-1.1-.9-2-2-2zm0 16H3V5h18v14zM9 8h2v8H9zm4 0h2v8h-2z"/>
              </svg>
              <span>{shareInfo.videoQuality.width}x{shareInfo.videoQuality.height}</span>
              {#if shareInfo.videoQuality.codec}
                <span class="divider">|</span>
                <span>{shareInfo.videoQuality.codec.toUpperCase()}</span>
              {/if}
              {#if shareInfo.videoQuality.audioCodec}
                <span class="divider">|</span>
                <span>{shareInfo.videoQuality.audioCodec.toUpperCase()}</span>
              {/if}
            </div>
          {/if}

          <!-- Plays Remaining -->
          {#if shareInfo.maxTotalPlays}
            <div class="plays-info">
              <div class="plays-header">
                <span class="plays-label">Plays remaining</span>
                <span class="plays-count">{getPlaysRemaining()} / {shareInfo.maxTotalPlays}</span>
              </div>
              <div class="plays-bar">
                <div
                  class="plays-fill"
                  style="width: {getPlaysPercentage()}%"
                  class:low={getPlaysPercentage() <= 33}
                  class:medium={getPlaysPercentage() > 33 && getPlaysPercentage() <= 66}
                ></div>
              </div>
            </div>
          {/if}

          <!-- Expiry Warning -->
          <div class="expiry-info">
            <svg viewBox="0 0 24 24" fill="currentColor">
              <path d="M6 2v6h.01L6 8.01 10 12l-4 4 .01.01H6V22h12v-5.99h-.01L18 16l-4-4 4-3.99-.01-.01H18V2H6z"/>
            </svg>
            <span>Expires in {formatExpiry(shareInfo.expiresAt)}</span>
          </div>

          <!-- Password or Play Button -->
          {#if showPasswordForm}
            <div class="password-section">
              <div class="password-header">
                <svg viewBox="0 0 24 24" fill="currentColor">
                  <path d="M18 8h-1V6c0-2.76-2.24-5-5-5S7 3.24 7 6v2H6c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V10c0-1.1-.9-2-2-2zm-6 9c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2zm3.1-9H8.9V6c0-1.71 1.39-3.1 3.1-3.1 1.71 0 3.1 1.39 3.1 3.1v2z"/>
                </svg>
                <span>Password Required</span>
              </div>
              <form on:submit|preventDefault={submitPassword}>
                <div class="input-group">
                  <input
                    type="password"
                    bind:value={passwordInput}
                    placeholder="Enter password"
                    disabled={passwordLoading}
                    class:error={passwordError}
                  />
                  <button type="submit" disabled={passwordLoading} class="unlock-btn">
                    {#if passwordLoading}
                      <div class="btn-spinner"></div>
                    {:else}
                      Unlock
                    {/if}
                  </button>
                </div>
                {#if passwordError}
                  <p class="error-msg">{passwordError}</p>
                {/if}
              </form>
            </div>
          {:else}
            {#if isSeasonOrSeries}
              <!-- Episode List for Season/Series -->
              <div class="episodes-section">
                <h3 class="episodes-header">
                  {shareInfo.itemType === 'Season' ? 'Episodes' : 'Seasons'}
                  {#if episodes.length > 0}
                    <span class="episodes-count">({episodes.length})</span>
                  {/if}
                </h3>

                {#if episodesLoading}
                  <div class="episodes-loading">
                    <div class="loading-spinner"></div>
                    <span>Loading {shareInfo.itemType === 'Season' ? 'episodes' : 'seasons'}...</span>
                  </div>
                {:else if episodesError}
                  <p class="error-msg">{episodesError}</p>
                {:else if episodes.length === 0}
                  <p class="episodes-empty">No {shareInfo.itemType === 'Season' ? 'episodes' : 'seasons'} found</p>
                {:else}
                  <div class="episodes-list">
                    {#each episodes as episode}
                      <button class="episode-card" on:click={() => startEpisodePlayback(episode)}>
                        <div class="episode-number">
                          {episode.indexNumber || '?'}
                        </div>
                        <div class="episode-info">
                          <div class="episode-title">{episode.name}</div>
                          {#if episode.runtimeSeconds}
                            <div class="episode-meta">{formatDuration(episode.runtimeSeconds)}</div>
                          {/if}
                        </div>
                        <div class="episode-actions">
                          <span
                            class="episode-copy"
                            role="button"
                            tabindex="0"
                            on:click={(e) => copyEpisodeStreamUrl(episode, e)}
                            on:keydown={(e) => { if (e.key === 'Enter' || e.key === ' ') copyEpisodeStreamUrl(episode, e); }}
                            title="Copy stream URL for VLC/mpv/IINA"
                          >
                            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                              <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
                              <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/>
                            </svg>
                          </span>
                          <div class="episode-play">
                            <svg viewBox="0 0 24 24" fill="currentColor">
                              <path d="M8 5v14l11-7z"/>
                            </svg>
                          </div>
                        </div>
                      </button>
                    {/each}
                  </div>
                {/if}

                {#if playError}
                  <p class="error-msg">{playError}</p>
                {/if}
              </div>
            {:else}
              <div class="stream-options">
                <label class="option-row">
                  <span class="option-label">Quality</span>
                  <select bind:value={selectedQualityId} class="option-select">
                    {#each qualityPresets as preset}
                      <option value={preset.id}>{preset.label}</option>
                    {/each}
                  </select>
                </label>
                {#if shareInfo.audioTracks && shareInfo.audioTracks.length > 1}
                  <label class="option-row">
                    <span class="option-label">Audio</span>
                    <select bind:value={selectedAudioIndex} class="option-select">
                      {#each shareInfo.audioTracks as track}
                        <option value={track.index}>{audioLabel(track)}</option>
                      {/each}
                    </select>
                  </label>
                {/if}
              </div>
              <div class="play-actions">
                <button class="play-button" on:click={startPlayback}>
                  <div class="play-icon">
                    <svg viewBox="0 0 24 24" fill="currentColor">
                      <path d="M8 5v14l11-7z"/>
                    </svg>
                  </div>
                  <span>Play Now</span>
                </button>
                <button class="copy-url-button" on:click={copyStreamUrl} disabled={copyBusy} title="Copy stream URL for VLC/mpv/IINA">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
                    <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/>
                  </svg>
                  <span>{copyBusy ? 'Copying…' : 'Copy URL'}</span>
                </button>
              </div>
              {#if copiedMsg}
                <p class="copied-msg">{copiedMsg}</p>
              {/if}
              {#if playError}
                <p class="error-msg">{playError}</p>
              {/if}
            {/if}
          {/if}
        </div>
      </div>

      <div class="footer">
        <span>Shared via Jellyfin Share</span>
      </div>
    </div>
  {/if}
</div>

<style>
  .share-container {
    min-height: 100vh;
    position: relative;
  }

  .backdrop-container {
    position: fixed;
    inset: 0;
    z-index: 0;
  }

  .backdrop {
    position: absolute;
    inset: -100px;
    background-size: cover;
    background-position: center top;
    filter: blur(60px) saturate(1.3) brightness(0.4);
    transform: scale(1.3);
  }

  .backdrop-gradient {
    position: absolute;
    inset: 0;
    background: linear-gradient(
      to bottom,
      rgba(0, 0, 0, 0.3) 0%,
      rgba(0, 0, 0, 0.6) 50%,
      rgba(0, 0, 0, 0.95) 100%
    );
  }

  .content {
    position: relative;
    z-index: 1;
    min-height: 100vh;
    padding: 2rem;
    display: flex;
    flex-direction: column;
    opacity: 0;
    transform: translateY(20px);
    transition: all 0.6s ease;
  }

  .content.loaded {
    opacity: 1;
    transform: translateY(0);
  }

  .logo-container {
    text-align: center;
    margin-bottom: 1.5rem;
    animation: fadeInDown 0.8s ease;
  }

  .logo {
    max-width: 400px;
    max-height: 120px;
    width: auto;
    filter: drop-shadow(0 4px 20px rgba(0,0,0,0.5));
  }

  @keyframes fadeInDown {
    from {
      opacity: 0;
      transform: translateY(-20px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .media-layout {
    display: flex;
    gap: 3rem;
    max-width: 1100px;
    margin: 0 auto;
    flex: 1;
    align-items: flex-start;
  }

  .poster-section {
    flex-shrink: 0;
    position: sticky;
    top: 2rem;
  }

  .poster-wrapper {
    position: relative;
    width: 280px;
    border-radius: 12px;
    overflow: hidden;
    box-shadow: 0 25px 80px -20px rgba(0, 0, 0, 0.8);
  }

  .poster-wrapper img {
    width: 100%;
    display: block;
    opacity: 0;
    transition: opacity 0.5s ease;
  }

  .poster-wrapper img.loaded {
    opacity: 1;
  }

  .quality-badge {
    position: absolute;
    top: 12px;
    right: 12px;
    background: rgba(0, 0, 0, 0.8);
    color: #00d4ff;
    padding: 4px 10px;
    border-radius: 4px;
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.05em;
    border: 1px solid rgba(0, 212, 255, 0.3);
  }

  .info-section {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .title {
    font-size: 2.8rem;
    font-weight: 800;
    line-height: 1.1;
    margin: 0;
    color: #fff;
    text-shadow: 0 4px 30px rgba(0,0,0,0.5);
  }

  .meta-badges {
    display: flex;
    flex-wrap: wrap;
    gap: 0.6rem;
  }

  .badge {
    background: rgba(255,255,255,0.1);
    border: 1px solid rgba(255,255,255,0.2);
    padding: 0.35rem 0.75rem;
    border-radius: 4px;
    font-size: 0.8rem;
    font-weight: 600;
    color: rgba(255,255,255,0.9);
  }

  .badge.rating-badge {
    border-color: rgba(255,200,100,0.4);
    color: rgb(255, 200, 100);
  }

  .badge.quality {
    background: linear-gradient(135deg, rgba(0,212,255,0.2), rgba(0,150,200,0.2));
    border-color: rgba(0,212,255,0.4);
    color: #00d4ff;
  }

  .ratings-row {
    display: flex;
    gap: 2rem;
    margin: 0.5rem 0;
  }

  .rating-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .rating-icon {
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .rating-icon.star {
    color: #ffd700;
  }

  .rating-icon.star svg {
    width: 24px;
    height: 24px;
  }

  .rating-icon.tomato {
    background: #6c757d;
    color: white;
    font-size: 0.7rem;
    font-weight: 700;
    padding: 4px 8px;
    border-radius: 4px;
  }

  .rating-icon.tomato.fresh {
    background: #fa320a;
  }

  .rating-value {
    font-size: 1.4rem;
    font-weight: 700;
    color: #fff;
  }

  .rating-label {
    font-size: 0.75rem;
    color: rgba(255,255,255,0.5);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .genres {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  .genre-tag {
    background: transparent;
    border: 1px solid rgba(255,255,255,0.3);
    padding: 0.3rem 0.8rem;
    border-radius: 20px;
    font-size: 0.8rem;
    color: rgba(255,255,255,0.8);
    transition: all 0.2s;
  }

  .genre-tag:hover {
    background: rgba(255,255,255,0.1);
    border-color: rgba(255,255,255,0.5);
  }

  .overview {
    color: rgba(255,255,255,0.75);
    line-height: 1.7;
    font-size: 0.95rem;
    margin: 0;
  }

  .credits-row {
    display: flex;
    gap: 0.75rem;
    font-size: 0.9rem;
  }

  .credits-label {
    color: rgba(255,255,255,0.5);
    min-width: 70px;
  }

  .credits-value {
    color: rgba(255,255,255,0.9);
  }

  .cast-section {
    margin-top: 0.5rem;
  }

  .cast-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 0.5rem;
  }

  .show-more-btn {
    background: none;
    border: none;
    color: #00d4ff;
    font-size: 0.8rem;
    cursor: pointer;
    padding: 0;
  }

  .show-more-btn:hover {
    text-decoration: underline;
  }

  .cast-list {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
  }

  .actor {
    background: rgba(255,255,255,0.05);
    border-radius: 8px;
    padding: 0.5rem 0.75rem;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }

  .actor-name {
    font-size: 0.85rem;
    color: rgba(255,255,255,0.9);
  }

  .actor-role {
    font-size: 0.75rem;
    color: rgba(255,255,255,0.5);
  }

  .quality-info {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.8rem;
    color: rgba(255,255,255,0.5);
    background: rgba(255,255,255,0.05);
    padding: 0.6rem 1rem;
    border-radius: 8px;
    width: fit-content;
  }

  .quality-info svg {
    width: 18px;
    height: 18px;
  }

  .quality-info .divider {
    opacity: 0.3;
  }

  .plays-info {
    background: rgba(255,255,255,0.05);
    border-radius: 10px;
    padding: 1rem;
    border: 1px solid rgba(255,255,255,0.08);
  }

  .plays-header {
    display: flex;
    justify-content: space-between;
    margin-bottom: 0.6rem;
    font-size: 0.85rem;
  }

  .plays-label {
    color: rgba(255,255,255,0.5);
  }

  .plays-count {
    color: rgba(255,255,255,0.8);
    font-weight: 600;
  }

  .plays-bar {
    height: 6px;
    background: rgba(255,255,255,0.1);
    border-radius: 3px;
    overflow: hidden;
  }

  .plays-fill {
    height: 100%;
    background: linear-gradient(90deg, #00d4ff, #00ff88);
    border-radius: 3px;
    transition: width 0.3s ease;
  }

  .plays-fill.medium {
    background: linear-gradient(90deg, #ffcc00, #ff9500);
  }

  .plays-fill.low {
    background: linear-gradient(90deg, #ff6b6b, #ff4757);
  }

  .expiry-info {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: rgba(255, 180, 100, 0.9);
    font-size: 0.85rem;
  }

  .expiry-info svg {
    width: 18px;
    height: 18px;
  }

  .password-section {
    margin-top: 1rem;
  }

  .password-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: rgba(255,255,255,0.7);
    font-size: 0.9rem;
    margin-bottom: 1rem;
  }

  .password-header svg {
    width: 20px;
    height: 20px;
  }

  .input-group {
    display: flex;
    gap: 0.5rem;
  }

  .input-group input {
    flex: 1;
    padding: 1rem 1.25rem;
    border: 2px solid rgba(255,255,255,0.15);
    border-radius: 12px;
    background: rgba(0,0,0,0.3);
    color: #fff;
    font-size: 1rem;
    transition: all 0.2s;
  }

  .input-group input:focus {
    outline: none;
    border-color: rgba(0, 212, 255, 0.5);
    background: rgba(0,0,0,0.4);
  }

  .input-group input.error {
    border-color: rgba(255, 107, 107, 0.5);
  }

  .input-group input::placeholder {
    color: rgba(255,255,255,0.3);
  }

  .unlock-btn {
    padding: 1rem 1.5rem;
    border: none;
    border-radius: 12px;
    background: linear-gradient(135deg, #00d4ff, #0099cc);
    color: #000;
    font-weight: 700;
    cursor: pointer;
    transition: all 0.2s;
    flex-shrink: 0;
  }

  .unlock-btn:hover:not(:disabled) {
    transform: scale(1.02);
    box-shadow: 0 8px 25px rgba(0, 212, 255, 0.3);
  }

  .unlock-btn:disabled {
    opacity: 0.7;
    cursor: not-allowed;
  }

  .btn-spinner {
    width: 20px;
    height: 20px;
    border: 2px solid rgba(0,0,0,0.2);
    border-top-color: #000;
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  .play-button {
    display: inline-flex;
    align-items: center;
    gap: 1rem;
    padding: 1rem 2.5rem 1rem 1rem;
    background: linear-gradient(135deg, #00d4ff 0%, #0099cc 100%);
    color: #000;
    border: none;
    border-radius: 16px;
    font-size: 1.15rem;
    font-weight: 700;
    cursor: pointer;
    transition: all 0.3s;
    margin-top: 0.5rem;
    align-self: flex-start;
  }

  .play-button:hover {
    transform: translateY(-2px) scale(1.02);
    box-shadow: 0 20px 50px -15px rgba(0, 212, 255, 0.5);
  }

  .play-button:active {
    transform: scale(0.98);
  }

  .play-icon {
    width: 44px;
    height: 44px;
    background: rgba(0,0,0,0.15);
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .play-icon svg {
    width: 22px;
    height: 22px;
    margin-left: 3px;
  }

  .error-msg {
    color: #ff6b6b;
    font-size: 0.85rem;
    margin: 0.75rem 0 0 0;
  }

  .play-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.75rem;
    margin-top: 0.5rem;
  }

  .stream-options {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    margin-top: 0.75rem;
  }

  .option-row {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    background: rgba(255, 255, 255, 0.05);
    border: 1px solid rgba(255, 255, 255, 0.12);
    border-radius: 10px;
    padding: 0.35rem 0.5rem 0.35rem 0.9rem;
  }

  .option-label {
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: rgba(255, 255, 255, 0.55);
  }

  .option-select {
    background: transparent;
    color: #fff;
    border: none;
    outline: none;
    font-size: 0.9rem;
    font-weight: 500;
    padding: 0.35rem 0.5rem;
    cursor: pointer;
    min-width: 140px;
  }
  .option-select option {
    background: #1a1a1a;
    color: #fff;
  }

  .copy-url-button {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.75rem 1.25rem;
    background: rgba(255, 255, 255, 0.08);
    color: #fff;
    border: 1px solid rgba(255, 255, 255, 0.18);
    border-radius: 12px;
    font-size: 0.95rem;
    font-weight: 600;
    cursor: pointer;
    transition: all 0.2s;
  }
  .copy-url-button:hover:not(:disabled) {
    background: rgba(255, 255, 255, 0.14);
    border-color: rgba(255, 255, 255, 0.3);
  }
  .copy-url-button:disabled {
    opacity: 0.55;
    cursor: default;
  }
  .copy-url-button svg {
    width: 18px;
    height: 18px;
  }

  .copied-msg {
    color: #00d4ff;
    font-size: 0.85rem;
    margin: 0.75rem 0 0 0;
  }

  .episode-actions {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .episode-copy {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    border-radius: 8px;
    color: rgba(255, 255, 255, 0.6);
    cursor: pointer;
    transition: all 0.2s;
  }
  .episode-copy:hover {
    background: rgba(255, 255, 255, 0.1);
    color: #00d4ff;
  }
  .episode-copy svg {
    width: 16px;
    height: 16px;
  }

  /* Episodes Section */
  .episodes-section {
    margin-top: 1rem;
  }

  .episodes-header {
    font-size: 1.2rem;
    font-weight: 600;
    color: rgba(255, 255, 255, 0.9);
    margin: 0 0 1rem 0;
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .episodes-count {
    font-size: 0.9rem;
    color: rgba(255, 255, 255, 0.5);
    font-weight: 400;
  }

  .episodes-loading {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    color: rgba(255, 255, 255, 0.6);
    font-size: 0.9rem;
    padding: 1rem;
  }

  .loading-spinner {
    width: 20px;
    height: 20px;
    border: 2px solid rgba(255, 255, 255, 0.2);
    border-top-color: #00d4ff;
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  .episodes-empty {
    color: rgba(255, 255, 255, 0.5);
    font-size: 0.9rem;
    padding: 1rem;
    margin: 0;
  }

  .episodes-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    max-height: 400px;
    overflow-y: auto;
    padding-right: 0.5rem;
  }

  .episodes-list::-webkit-scrollbar {
    width: 6px;
  }

  .episodes-list::-webkit-scrollbar-track {
    background: rgba(255, 255, 255, 0.05);
    border-radius: 3px;
  }

  .episodes-list::-webkit-scrollbar-thumb {
    background: rgba(255, 255, 255, 0.2);
    border-radius: 3px;
  }

  .episodes-list::-webkit-scrollbar-thumb:hover {
    background: rgba(255, 255, 255, 0.3);
  }

  .episode-card {
    display: flex;
    align-items: center;
    gap: 1rem;
    padding: 0.75rem 1rem;
    background: rgba(255, 255, 255, 0.05);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 10px;
    cursor: pointer;
    transition: all 0.2s ease;
    width: 100%;
    text-align: left;
    color: inherit;
  }

  .episode-card:hover {
    background: rgba(255, 255, 255, 0.1);
    border-color: rgba(0, 212, 255, 0.3);
    transform: translateX(4px);
  }

  .episode-card:active {
    transform: translateX(2px);
  }

  .episode-number {
    width: 36px;
    height: 36px;
    background: rgba(0, 212, 255, 0.15);
    border: 1px solid rgba(0, 212, 255, 0.3);
    border-radius: 8px;
    display: flex;
    align-items: center;
    justify-content: center;
    font-weight: 700;
    font-size: 0.9rem;
    color: #00d4ff;
    flex-shrink: 0;
  }

  .episode-info {
    flex: 1;
    min-width: 0;
  }

  .episode-title {
    font-size: 0.95rem;
    font-weight: 500;
    color: rgba(255, 255, 255, 0.9);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .episode-meta {
    font-size: 0.8rem;
    color: rgba(255, 255, 255, 0.5);
    margin-top: 0.15rem;
  }

  .episode-play {
    width: 32px;
    height: 32px;
    background: rgba(0, 212, 255, 0.2);
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    transition: all 0.2s ease;
  }

  .episode-play svg {
    width: 14px;
    height: 14px;
    color: #00d4ff;
    margin-left: 2px;
  }

  .episode-card:hover .episode-play {
    background: rgba(0, 212, 255, 0.4);
    transform: scale(1.1);
  }

  .footer {
    text-align: center;
    padding: 2rem;
    color: rgba(255,255,255,0.2);
    font-size: 0.8rem;
    margin-top: auto;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  @media (max-width: 900px) {
    .media-layout {
      flex-direction: column;
      align-items: center;
      text-align: center;
    }

    .poster-section {
      position: static;
    }

    .poster-wrapper {
      width: 200px;
    }

    .info-section {
      align-items: center;
    }

    .title {
      font-size: 2rem;
    }

    .meta-badges, .genres, .ratings-row {
      justify-content: center;
    }

    .credits-row {
      flex-direction: column;
      gap: 0.25rem;
    }

    .credits-label {
      min-width: unset;
    }

    .cast-list {
      justify-content: center;
    }

    .quality-info {
      margin: 0 auto;
    }

    .play-button {
      align-self: center;
    }

    .input-group {
      flex-direction: column;
    }

    .episodes-list {
      max-height: 350px;
    }

    .episode-card:hover {
      transform: none;
    }
  }

  @media (max-width: 480px) {
    .content {
      padding: 1rem;
    }

    .poster-wrapper {
      width: 160px;
    }

    .title {
      font-size: 1.5rem;
    }

    .logo {
      max-width: 280px;
    }

    .episodes-list {
      max-height: 300px;
    }

    .episode-card {
      padding: 0.6rem 0.75rem;
      gap: 0.75rem;
    }

    .episode-number {
      width: 32px;
      height: 32px;
      font-size: 0.8rem;
    }

    .episode-title {
      font-size: 0.9rem;
    }

    .episode-play {
      width: 28px;
      height: 28px;
    }

    .episode-play svg {
      width: 12px;
      height: 12px;
    }
  }
</style>

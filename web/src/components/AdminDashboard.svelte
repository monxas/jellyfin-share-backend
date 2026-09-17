<script>
  import { onMount, createEventDispatcher } from 'svelte';

  export let apiKey;

  const dispatch = createEventDispatcher();

  let shares = [];
  let loading = true;
  let error = null;
  let selectedShare = null;
  let showDetails = false;
  let shareDetails = null;
  let detailsLoading = false;

  onMount(() => {
    loadShares();
  });

  async function loadShares() {
    loading = true;
    error = null;
    try {
      const response = await fetch('/api/admin/shares', {
        headers: { 'X-Backend-Key': apiKey }
      });
      if (!response.ok) throw new Error('Failed to load shares');
      const data = await response.json();
      shares = data.shares || [];
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  async function viewDetails(share) {
    selectedShare = share;
    showDetails = true;
    detailsLoading = true;
    try {
      const response = await fetch(`/api/admin/shares/${share.id}`, {
        headers: { 'X-Backend-Key': apiKey }
      });
      if (!response.ok) throw new Error('Failed to load details');
      shareDetails = await response.json();
    } catch (e) {
      shareDetails = null;
    } finally {
      detailsLoading = false;
    }
  }

  async function revokeShare(share) {
    if (!confirm(`Revoke share "${share.title}"? This will immediately terminate all active sessions.`)) {
      return;
    }

    try {
      const response = await fetch(`/api/admin/shares/${share.id}/revoke`, {
        method: 'POST',
        headers: { 'X-Backend-Key': apiKey }
      });
      if (!response.ok) throw new Error('Failed to revoke');
      loadShares();
      if (showDetails && selectedShare?.id === share.id) {
        showDetails = false;
      }
    } catch (e) {
      alert('Failed to revoke share');
    }
  }

  function closeDetails() {
    showDetails = false;
    selectedShare = null;
    shareDetails = null;
  }

  function logout() {
    dispatch('logout');
  }

  function getStatus(share) {
    if (share.revokedAt) return 'revoked';
    if (share.expiresAt && new Date(share.expiresAt) < new Date()) return 'expired';
    return 'active';
  }

  function formatDate(dateStr) {
    if (!dateStr) return 'Never';
    return new Date(dateStr).toLocaleString();
  }

  function formatTimeAgo(dateStr) {
    const date = new Date(dateStr);
    const now = new Date();
    const diff = now - date;
    const minutes = Math.floor(diff / 60000);
    const hours = Math.floor(minutes / 60);
    const days = Math.floor(hours / 24);

    if (days > 0) return `${days}d ago`;
    if (hours > 0) return `${hours}h ago`;
    if (minutes > 0) return `${minutes}m ago`;
    return 'just now';
  }

  function formatExpiry(dateStr) {
    if (!dateStr) return 'Never';
    const date = new Date(dateStr);
    const now = new Date();
    const diff = date - now;

    if (diff <= 0) return 'Expired';

    const hours = Math.floor(diff / (1000 * 60 * 60));
    const days = Math.floor(hours / 24);

    if (days > 0) return `${days}d ${hours % 24}h`;
    if (hours > 0) return `${hours}h`;
    return `${Math.floor(diff / (1000 * 60))}m`;
  }

  function copyLink(share) {
    const url = `${window.location.origin}/s/${share.publicToken}`;
    navigator.clipboard.writeText(url);
  }
</script>

<div class="admin-container">
  <header>
    <div class="header-left">
      <h1>Share Management</h1>
      <span class="count">{shares.length} shares</span>
    </div>
    <div class="header-right">
      <button class="refresh-btn" on:click={loadShares} disabled={loading}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M23 4v6h-6M1 20v-6h6M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15"/>
        </svg>
      </button>
      <button class="logout-btn" on:click={logout}>Logout</button>
    </div>
  </header>

  {#if loading}
    <div class="loading">
      <div class="spinner"></div>
      <span>Loading shares...</span>
    </div>
  {:else if error}
    <div class="error-banner">
      <span>{error}</span>
      <button on:click={loadShares}>Retry</button>
    </div>
  {:else if shares.length === 0}
    <div class="empty">
      <p>No shares found</p>
    </div>
  {:else}
    <div class="table-container">
      <table>
        <thead>
          <tr>
            <th>Title</th>
            <th>Status</th>
            <th>Plays</th>
            <th>Viewers</th>
            <th>Created</th>
            <th>Expires</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each shares as share}
            {@const status = getStatus(share)}
            <tr class:revoked={status === 'revoked'} class:expired={status === 'expired'}>
              <td class="title-cell">
                <div class="title-info">
                  <span class="title">{share.title}</span>
                  <span class="type">{share.itemType}</span>
                </div>
                {#if share.hasPassword}
                  <span class="password-badge" title="Password protected">
                    <svg viewBox="0 0 24 24" fill="currentColor" width="14" height="14">
                      <path d="M18 8h-1V6c0-2.76-2.24-5-5-5S7 3.24 7 6v2H6c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V10c0-1.1-.9-2-2-2z"/>
                    </svg>
                  </span>
                {/if}
              </td>
              <td>
                <span class="status-badge {status}">{status}</span>
              </td>
              <td class="plays-cell">
                {share.totalPlays}{#if share.maxTotalPlays} / {share.maxTotalPlays}{/if}
              </td>
              <td class="viewers-cell">
                <span class:active={share.currentConcurrentViewers > 0}>
                  {share.currentConcurrentViewers}{#if share.maxConcurrentViewers} / {share.maxConcurrentViewers}{/if}
                </span>
              </td>
              <td class="date-cell">{formatTimeAgo(share.createdAt)}</td>
              <td class="date-cell">
                {#if status === 'revoked'}
                  <span class="muted">Revoked</span>
                {:else}
                  {formatExpiry(share.expiresAt)}
                {/if}
              </td>
              <td class="actions-cell">
                <button class="action-btn" on:click={() => copyLink(share)} title="Copy link">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
                    <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/>
                  </svg>
                </button>
                <button class="action-btn" on:click={() => viewDetails(share)} title="View details">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <circle cx="12" cy="12" r="10"/>
                    <path d="M12 16v-4M12 8h.01"/>
                  </svg>
                </button>
                {#if status === 'active'}
                  <button class="action-btn danger" on:click={() => revokeShare(share)} title="Revoke">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <circle cx="12" cy="12" r="10"/>
                      <path d="M15 9l-6 6M9 9l6 6"/>
                    </svg>
                  </button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

{#if showDetails}
  <div class="modal-overlay" on:click={closeDetails}>
    <div class="modal" on:click|stopPropagation>
      <div class="modal-header">
        <h2>{selectedShare?.title}</h2>
        <button class="close-btn" on:click={closeDetails}>&times;</button>
      </div>

      {#if detailsLoading}
        <div class="modal-loading">
          <div class="spinner"></div>
        </div>
      {:else if shareDetails}
        <div class="modal-content">
          <div class="detail-section">
            <h3>Share Info</h3>
            <div class="detail-grid">
              <div class="detail-item">
                <span class="label">Token</span>
                <code>{shareDetails.share.publicToken}</code>
              </div>
              <div class="detail-item">
                <span class="label">Item Type</span>
                <span>{shareDetails.share.itemType}</span>
              </div>
              <div class="detail-item">
                <span class="label">Total Plays</span>
                <span>{shareDetails.share.totalPlays}{#if shareDetails.share.maxTotalPlays} / {shareDetails.share.maxTotalPlays.Int64}{/if}</span>
              </div>
              <div class="detail-item">
                <span class="label">Current Viewers</span>
                <span>{shareDetails.share.currentConcurrentViewers}{#if shareDetails.share.maxConcurrentViewers} / {shareDetails.share.maxConcurrentViewers.Int64}{/if}</span>
              </div>
              <div class="detail-item">
                <span class="label">Created</span>
                <span>{formatDate(shareDetails.share.createdAt)}</span>
              </div>
              <div class="detail-item">
                <span class="label">Expires</span>
                <span>{formatDate(shareDetails.share.expiresAt)}</span>
              </div>
            </div>
          </div>

          {#if shareDetails.sessions && shareDetails.sessions.length > 0}
            <div class="detail-section">
              <h3>Sessions ({shareDetails.sessions.length})</h3>
              <div class="sessions-list">
                {#each shareDetails.sessions as session}
                  <div class="session-item" class:active={!session.finishedAt}>
                    <div class="session-info">
                      <span class="session-status" class:active={!session.finishedAt}>
                        {session.finishedAt ? 'Ended' : 'Active'}
                      </span>
                      <span class="session-time">
                        Started {formatTimeAgo(session.startedAt)}
                      </span>
                    </div>
                    {#if session.userAgent}
                      <div class="session-ua">{session.userAgent.String}</div>
                    {/if}
                  </div>
                {/each}
              </div>
            </div>
          {:else}
            <div class="detail-section">
              <h3>Sessions</h3>
              <p class="no-sessions">No sessions recorded</p>
            </div>
          {/if}
        </div>
      {/if}
    </div>
  </div>
{/if}

<style>
  .admin-container {
    min-height: 100vh;
    background: linear-gradient(135deg, #1a1a2e 0%, #16213e 100%);
    padding: 2rem;
  }

  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 2rem;
    padding-bottom: 1rem;
    border-bottom: 1px solid rgba(255, 255, 255, 0.1);
  }

  .header-left {
    display: flex;
    align-items: baseline;
    gap: 1rem;
  }

  h1 {
    margin: 0;
    font-size: 1.75rem;
    color: #fff;
  }

  .count {
    color: rgba(255, 255, 255, 0.5);
    font-size: 0.9rem;
  }

  .header-right {
    display: flex;
    gap: 0.75rem;
  }

  .refresh-btn {
    background: rgba(255, 255, 255, 0.1);
    border: none;
    border-radius: 8px;
    padding: 0.5rem;
    cursor: pointer;
    color: rgba(255, 255, 255, 0.7);
    transition: all 0.2s;
  }

  .refresh-btn:hover {
    background: rgba(255, 255, 255, 0.15);
    color: #fff;
  }

  .refresh-btn svg {
    width: 20px;
    height: 20px;
  }

  .logout-btn {
    background: rgba(255, 107, 107, 0.2);
    border: 1px solid rgba(255, 107, 107, 0.3);
    border-radius: 8px;
    padding: 0.5rem 1rem;
    color: #ff6b6b;
    cursor: pointer;
    font-size: 0.85rem;
    transition: all 0.2s;
  }

  .logout-btn:hover {
    background: rgba(255, 107, 107, 0.3);
  }

  .loading, .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 4rem;
    color: rgba(255, 255, 255, 0.5);
    gap: 1rem;
  }

  .spinner {
    width: 32px;
    height: 32px;
    border: 3px solid rgba(255, 255, 255, 0.1);
    border-top-color: #00d4ff;
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  .error-banner {
    background: rgba(255, 107, 107, 0.1);
    border: 1px solid rgba(255, 107, 107, 0.3);
    border-radius: 8px;
    padding: 1rem;
    display: flex;
    justify-content: space-between;
    align-items: center;
    color: #ff6b6b;
  }

  .error-banner button {
    background: rgba(255, 255, 255, 0.1);
    border: none;
    border-radius: 4px;
    padding: 0.5rem 1rem;
    color: #fff;
    cursor: pointer;
  }

  .table-container {
    background: rgba(255, 255, 255, 0.03);
    border: 1px solid rgba(255, 255, 255, 0.08);
    border-radius: 12px;
    overflow: hidden;
  }

  table {
    width: 100%;
    border-collapse: collapse;
  }

  th {
    text-align: left;
    padding: 1rem;
    color: rgba(255, 255, 255, 0.5);
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    border-bottom: 1px solid rgba(255, 255, 255, 0.08);
    font-weight: 600;
  }

  td {
    padding: 1rem;
    color: rgba(255, 255, 255, 0.9);
    border-bottom: 1px solid rgba(255, 255, 255, 0.05);
    font-size: 0.9rem;
  }

  tr:last-child td {
    border-bottom: none;
  }

  tr.revoked td, tr.expired td {
    opacity: 0.5;
  }

  .title-cell {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .title-info {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
  }

  .title {
    font-weight: 500;
  }

  .type {
    font-size: 0.75rem;
    color: rgba(255, 255, 255, 0.4);
  }

  .password-badge {
    color: #ffd700;
  }

  .status-badge {
    display: inline-block;
    padding: 0.25rem 0.6rem;
    border-radius: 4px;
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
  }

  .status-badge.active {
    background: rgba(0, 255, 136, 0.15);
    color: #00ff88;
  }

  .status-badge.expired {
    background: rgba(255, 200, 100, 0.15);
    color: rgb(255, 200, 100);
  }

  .status-badge.revoked {
    background: rgba(255, 107, 107, 0.15);
    color: #ff6b6b;
  }

  .viewers-cell .active {
    color: #00ff88;
    font-weight: 600;
  }

  .date-cell {
    color: rgba(255, 255, 255, 0.6);
  }

  .muted {
    color: rgba(255, 255, 255, 0.3);
  }

  .actions-cell {
    display: flex;
    gap: 0.5rem;
  }

  .action-btn {
    background: rgba(255, 255, 255, 0.08);
    border: none;
    border-radius: 6px;
    padding: 0.4rem;
    cursor: pointer;
    color: rgba(255, 255, 255, 0.7);
    transition: all 0.2s;
  }

  .action-btn:hover {
    background: rgba(255, 255, 255, 0.15);
    color: #fff;
  }

  .action-btn.danger:hover {
    background: rgba(255, 107, 107, 0.2);
    color: #ff6b6b;
  }

  .action-btn svg {
    width: 18px;
    height: 18px;
    display: block;
  }

  /* Modal */
  .modal-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.7);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 2rem;
    z-index: 1000;
  }

  .modal {
    background: #1a1a2e;
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 16px;
    width: 100%;
    max-width: 600px;
    max-height: 80vh;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .modal-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1.25rem 1.5rem;
    border-bottom: 1px solid rgba(255, 255, 255, 0.1);
  }

  .modal-header h2 {
    margin: 0;
    font-size: 1.25rem;
    color: #fff;
  }

  .close-btn {
    background: none;
    border: none;
    color: rgba(255, 255, 255, 0.5);
    font-size: 1.5rem;
    cursor: pointer;
    line-height: 1;
  }

  .close-btn:hover {
    color: #fff;
  }

  .modal-loading {
    display: flex;
    justify-content: center;
    padding: 3rem;
  }

  .modal-content {
    padding: 1.5rem;
    overflow-y: auto;
  }

  .detail-section {
    margin-bottom: 1.5rem;
  }

  .detail-section:last-child {
    margin-bottom: 0;
  }

  .detail-section h3 {
    margin: 0 0 1rem 0;
    font-size: 0.85rem;
    color: rgba(255, 255, 255, 0.5);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .detail-grid {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 1rem;
  }

  .detail-item {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .detail-item .label {
    font-size: 0.75rem;
    color: rgba(255, 255, 255, 0.4);
  }

  .detail-item code {
    background: rgba(0, 0, 0, 0.3);
    padding: 0.25rem 0.5rem;
    border-radius: 4px;
    font-size: 0.85rem;
    color: #00d4ff;
  }

  .sessions-list {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .session-item {
    background: rgba(255, 255, 255, 0.03);
    border: 1px solid rgba(255, 255, 255, 0.08);
    border-radius: 8px;
    padding: 0.75rem;
  }

  .session-item.active {
    border-color: rgba(0, 255, 136, 0.3);
  }

  .session-info {
    display: flex;
    gap: 0.75rem;
    align-items: center;
  }

  .session-status {
    font-size: 0.75rem;
    padding: 0.2rem 0.5rem;
    border-radius: 4px;
    background: rgba(255, 255, 255, 0.1);
    color: rgba(255, 255, 255, 0.6);
  }

  .session-status.active {
    background: rgba(0, 255, 136, 0.15);
    color: #00ff88;
  }

  .session-time {
    font-size: 0.85rem;
    color: rgba(255, 255, 255, 0.6);
  }

  .session-ua {
    font-size: 0.75rem;
    color: rgba(255, 255, 255, 0.4);
    margin-top: 0.5rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .no-sessions {
    color: rgba(255, 255, 255, 0.4);
    font-size: 0.9rem;
    margin: 0;
  }
</style>

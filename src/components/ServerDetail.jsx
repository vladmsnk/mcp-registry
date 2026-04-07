import { useState } from 'react';
import { Button } from './Button';
import { StatusBadge } from './StatusBadge';
import { TagBadge } from './TagBadge';

export function ServerDetail({ server, onBack, onDelete, onSync }) {
  const [syncing, setSyncing] = useState(false);
  const [syncResult, setSyncResult] = useState(null);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const handleSync = async () => {
    setSyncing(true);
    setSyncResult(null);
    try {
      const res = await fetch(`/api/servers/${server.id}/sync`, { method: 'POST' });
      const data = await res.json();
      if (res.ok) {
        setSyncResult({ ok: true, message: `Synced ${data.synced} tools` });
        onSync();
      } else {
        setSyncResult({ ok: false, message: data.error || 'Sync failed' });
      }
    } catch {
      setSyncResult({ ok: false, message: 'Network error' });
    } finally {
      setSyncing(false);
    }
  };

  const handleDelete = async () => {
    setDeleting(true);
    try {
      const res = await fetch(`/api/servers/${server.id}`, { method: 'DELETE' });
      if (res.ok || res.status === 204) {
        onDelete(server.id);
      }
    } catch {
      // ignore
    } finally {
      setDeleting(false);
      setConfirmDelete(false);
    }
  };

  const createdAt = new Date(server.createdAt).toLocaleString();

  return (
    <div className="screen">
      <div className="screen-label">Server Details</div>
      <div className="card">
        <div className="card-header register-header">
          <div className="header-left">
            <button className="back-btn" onClick={onBack}>
              <svg width="20" height="20" viewBox="0 0 20 20" fill="none">
                <path d="M13 4L7 10L13 16" stroke="#374151" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
            <div>
              <h1 className="card-title">{server.name}</h1>
              <p className="card-subtitle">{server.description || 'No description'}</p>
            </div>
          </div>
          <div className="detail-actions">
            <Button variant="secondary" onClick={handleSync}>
              {syncing ? 'Syncing...' : 'Sync Tools'}
            </Button>
            {!confirmDelete ? (
              <Button variant="danger" onClick={() => setConfirmDelete(true)}>Delete</Button>
            ) : (
              <Button variant="danger" onClick={handleDelete}>
                {deleting ? 'Deleting...' : 'Confirm Delete'}
              </Button>
            )}
          </div>
        </div>

        {syncResult && (
          <div className={`detail-alert ${syncResult.ok ? 'detail-alert-success' : 'detail-alert-error'}`}>
            {syncResult.message}
          </div>
        )}

        <div className="detail-body">
          <div className="detail-section">
            <h2 className="detail-section-title">Server Information</h2>
            <div className="detail-grid">
              <div className="detail-item">
                <span className="detail-label">Status</span>
                <StatusBadge active={server.active} />
              </div>
              <div className="detail-item">
                <span className="detail-label">Endpoint</span>
                <span className="detail-value cell-url">{server.endpoint}</span>
              </div>
              <div className="detail-item">
                <span className="detail-label">Owner</span>
                <span className="detail-value">{server.owner || '—'}</span>
              </div>
              <div className="detail-item">
                <span className="detail-label">Auth Type</span>
                <span className="detail-value">{server.authType || 'None'}</span>
              </div>
              <div className="detail-item">
                <span className="detail-label">Registered</span>
                <span className="detail-value">{createdAt}</span>
              </div>
              <div className="detail-item">
                <span className="detail-label">Tags</span>
                <span className="detail-value">
                  {server.tags && server.tags.length > 0 ? (
                    <span className="cell-tags">
                      {server.tags.map((tag) => <TagBadge key={tag} label={tag} />)}
                    </span>
                  ) : '—'}
                </span>
              </div>
            </div>
          </div>

          <div className="detail-section">
            <h2 className="detail-section-title">Keycloak Configuration</h2>
            {server.keycloakClientId ? (
              <div className="detail-grid">
                <div className="detail-item">
                  <span className="detail-label">Keycloak Client ID</span>
                  <span className="detail-value detail-mono">{server.keycloakClientId}</span>
                </div>
                <div className="detail-item">
                  <span className="detail-label">Token Exchange</span>
                  <span className="detail-value">
                    <span className="status status-active">
                      <span className="status-dot dot-active" />
                      Enabled
                    </span>
                  </span>
                </div>
                <div className="detail-item detail-item-wide">
                  <span className="detail-label">Token Exchange Audience</span>
                  <span className="detail-value detail-mono">{server.keycloakClientId}</span>
                </div>
                <div className="detail-item detail-item-wide">
                  <span className="detail-label">How it works</span>
                  <span className="detail-value detail-hint">
                    When an agent calls a tool on this server, the hub exchanges the agent's access token
                    for a scoped token with audience <strong>{server.keycloakClientId}</strong> via
                    OAuth 2.0 Token Exchange (RFC 8693). The downstream server receives a token it can
                    validate independently.
                  </span>
                </div>
              </div>
            ) : (
              <div className="detail-empty">
                Keycloak client not provisioned. This server was registered before auto-provisioning was enabled,
                or auth is disabled.
              </div>
            )}
          </div>

          <div className="detail-section">
            <h2 className="detail-section-title">MCP Connection</h2>
            <div className="detail-grid">
              <div className="detail-item detail-item-wide">
                <span className="detail-label">MCP Endpoint</span>
                <span className="detail-value detail-mono">{server.endpoint}</span>
              </div>
              <div className="detail-item detail-item-wide">
                <span className="detail-label">Hub Proxy</span>
                <span className="detail-value detail-hint">
                  Agents connect to the hub and call <strong>call_tool(server_id={server.id}, tool_name, arguments)</strong>.
                  The hub proxies the request to this server's MCP endpoint.
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

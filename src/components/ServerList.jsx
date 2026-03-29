import { useState, useMemo } from 'react';
import { Button } from './Button';
import { SearchBar } from './SearchBar';
import { FilterChips } from './FilterChips';
import { StatusBadge } from './StatusBadge';
import { TagBadge } from './TagBadge';

export function ServerList({ servers, onNavigateRegister }) {
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState('All');

  const filtered = useMemo(() => {
    return servers.filter((s) => {
      const matchesSearch =
        !search ||
        s.name.toLowerCase().includes(search.toLowerCase()) ||
        s.endpoint.toLowerCase().includes(search.toLowerCase()) ||
        s.owner.toLowerCase().includes(search.toLowerCase()) ||
        s.tags.some((t) => t.toLowerCase().includes(search.toLowerCase()));

      const matchesFilter =
        filter === 'All' ||
        (filter === 'Active' && s.active) ||
        (filter === 'Inactive' && !s.active);

      return matchesSearch && matchesFilter;
    });
  }, [servers, search, filter]);

  return (
    <div className="screen">
      <div className="screen-label">Servers</div>
      <div className="card">
        <div className="card-header">
          <div>
            <h1 className="card-title">MCP Registry</h1>
            <p className="card-subtitle">Manage your registered MCP servers.</p>
          </div>
          <Button onClick={onNavigateRegister}>
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path d="M8 3v10M3 8h10" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            </svg>
            Register Server
          </Button>
        </div>

        <div className="search-filters">
          <SearchBar value={search} onChange={setSearch} />
          <FilterChips active={filter} onChange={setFilter} />
        </div>

        {/* Desktop/Tablet table */}
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Endpoint</th>
                <th>Status</th>
                <th>Owner</th>
                <th>Tags</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((server) => (
                <tr key={server.id}>
                  <td className="cell-name">{server.name}</td>
                  <td className="cell-url">{server.endpoint}</td>
                  <td>
                    <StatusBadge active={server.active} />
                  </td>
                  <td className="cell-owner">{server.owner}</td>
                  <td className="cell-tags">
                    {server.tags.map((tag) => (
                      <TagBadge key={tag} label={tag} />
                    ))}
                  </td>
                </tr>
              ))}
              {filtered.length === 0 && (
                <tr>
                  <td colSpan="5" style={{ textAlign: 'center', color: '#9CA3AF', padding: '40px 24px' }}>
                    No servers found.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        <div className="table-footer">
          {filtered.length} {filtered.length === 1 ? 'server' : 'servers'}
        </div>

        {/* Mobile card layout */}
        <div className="mobile-cards">
          {filtered.map((server) => (
            <div key={server.id} className="mobile-card">
              <div className="mobile-card-header">
                <span className="mobile-card-name">{server.name}</span>
                <StatusBadge active={server.active} />
              </div>
              <div className="mobile-card-row">
                <span className="mobile-card-label">URL</span>
                <span className="mobile-card-value cell-url">{server.endpoint}</span>
              </div>
              <div className="mobile-card-row">
                <span className="mobile-card-label">Owner</span>
                <span className="mobile-card-value">{server.owner}</span>
              </div>
              <div className="mobile-card-tags">
                {server.tags.map((tag) => (
                  <TagBadge key={tag} label={tag} />
                ))}
              </div>
            </div>
          ))}
          {filtered.length === 0 && (
            <div style={{ textAlign: 'center', color: '#9CA3AF', padding: '40px 16px' }}>
              No servers found.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

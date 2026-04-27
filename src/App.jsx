import { useState, useEffect, useCallback } from 'react';
import { ServerList } from './components/ServerList';
import { ServerDetail } from './components/ServerDetail';
import { RegisterServer } from './components/RegisterServer';
import { UserHeader } from './components/UserHeader';

async function fetchWithAuth(url, options = {}) {
  let res = await fetch(url, options);
  if (res.status === 401) {
    const refresh = await fetch('/auth/refresh', { method: 'POST' });
    if (refresh.ok) {
      res = await fetch(url, options);
    }
  }
  return res;
}

export default function App() {
  const [user, setUser] = useState(null); // null=loading, false=unauthed, {...}=authed
  const [screen, setScreen] = useState('list');
  const [servers, setServers] = useState([]);
  const [selectedServer, setSelectedServer] = useState(null);

  useEffect(() => {
    fetch('/auth/me')
      .then(res => res.ok ? res.json() : Promise.reject())
      .then(setUser)
      .catch(() => setUser(false));
  }, []);

  const fetchServers = useCallback(async () => {
    try {
      const res = await fetchWithAuth('/api/servers');
      if (res.status === 401) { setUser(false); return; }
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) setServers(data);
    } catch {
      // backend not running
    }
  }, []);

  useEffect(() => {
    if (user) fetchServers();
  }, [user, fetchServers]);

  const handleRegister = async (serverData) => {
    const res = await fetchWithAuth('/api/servers', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(serverData),
    });

    if (res.ok) {
      await fetchServers();
      setScreen('list');
    }
  };

  const handleSelectServer = (server) => {
    setSelectedServer(server);
    setScreen('detail');
  };

  const handleDeleteServer = (serverId) => {
    setServers((prev) => prev.filter((s) => s.id !== serverId));
    setScreen('list');
    setSelectedServer(null);
  };

  const handleSyncServer = async () => {
    try {
      const res = await fetchWithAuth('/api/servers');
      if (!res.ok) return;
      const data = await res.json();
      if (!Array.isArray(data)) return;
      setServers(data);
      if (selectedServer) {
        const updated = data.find((s) => s.id === selectedServer.id);
        if (updated) setSelectedServer(updated);
      }
    } catch {
      // ignore
    }
  };

  const renderScreen = () => {
    if (screen === 'register') {
      return <RegisterServer onBack={() => setScreen('list')} onSubmit={handleRegister} />;
    }
    if (screen === 'detail' && selectedServer) {
      return (
        <ServerDetail
          server={selectedServer}
          onBack={() => { setScreen('list'); setSelectedServer(null); }}
          onDelete={handleDeleteServer}
          onSync={handleSyncServer}
        />
      );
    }
    return (
      <ServerList
        servers={servers}
        onNavigateRegister={() => setScreen('register')}
        onSelectServer={handleSelectServer}
      />
    );
  };

  if (user === null) {
    return (
      <div className="app-shell">
        <div className="login-screen">
          <p className="login-subtitle">Loading...</p>
        </div>
      </div>
    );
  }

  if (user === false) {
    return (
      <div className="app-shell">
        <div className="login-screen">
          <h1 className="login-title">MCP Registry</h1>
          <p className="login-subtitle">Sign in to manage your MCP servers</p>
          <a href="/auth/login" className="btn btn-primary">Sign in with SSO</a>
        </div>
      </div>
    );
  }

  return (
    <div className="app-shell">
      <UserHeader user={user} />
      {renderScreen()}
    </div>
  );
}
